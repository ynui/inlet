package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ynui/inlet/internal/client"
	"github.com/ynui/inlet/internal/config"
)

func timeNow() string { return time.Now().Format("15:04:05") }

const (
	requestRows = 10
	pathWidth   = 40
	// paneLines is the redrawn region: stats line, a gap, then the rows.
	paneLines = requestRows + 2
)

var version = "dev"
var commit = "unknown"

// printHeader paints the static header with absolute cursor positions so a
// reconnect can redraw it in place instead of stacking a second copy.
func printHeader(pub string, port, inspectPort int, up time.Duration) {
	printConnectionHeader(pub, port, inspectPort, up, "online", "")
}

func printConnectionHeader(pub string, port, inspectPort int, up time.Duration, status, detail string) {
	statusColor := "\033[33m"
	if status == "online" {
		statusColor = "\033[32m"
	} else if status == "error" {
		statusColor = "\033[31m"
	}
	detail = strings.Join(strings.Fields(detail), " ")
	if runes := []rune(detail); len(runes) > 70 {
		detail = string(runes[:67]) + "..."
	}
	fmt.Printf("\033[2;1H\033[2K  \033[1;36minlet\033[0m")
	fmt.Printf("\033[3;1H\033[2K  \033[90mSession Status\033[0m                %s%s", statusColor, status)
	if detail != "" {
		fmt.Printf(": %s", detail)
	}
	fmt.Print("\033[0m")
	if status == "online" {
		fmt.Printf("   \033[90mup %s\033[0m", humanDuration(up))
	}
	fmt.Printf("\033[4;1H\033[2K  \033[90mForwarding\033[0m                    \033[1m%s\033[0m \033[36m➜\033[0m http://127.0.0.1:%d", pub, port)
	fmt.Printf("\033[5;1H\033[2K  \033[90mWeb Interface\033[0m                 http://127.0.0.1:%d", inspectPort)
	fmt.Print("\033[6;1H\033[2K")
	fmt.Print("\033[7;1H")
}

// requestView renders paneLines lines, cursor left on the line below the pane.
func requestView(evs []client.Event, s2, s3, s4, s5, total int64, first bool) string {
	var b strings.Builder
	if !first {
		fmt.Fprintf(&b, "\033[7;1H\033[J")
	}
	b.WriteString("\033[2K")
	fmt.Fprintf(&b, "  \033[90mRequests\033[0m   \033[90mtotal %d\033[0m   \033[32m2xx %d\033[0m   \033[36m3xx %d\033[0m   \033[33m4xx %d\033[0m   \033[31m5xx %d\033[0m\n", total, s2, s3, s4, s5)
	b.WriteString("\033[2K\n")
	if len(evs) > requestRows {
		evs = evs[:requestRows]
	}
	for r := 0; r < requestRows; r++ {
		b.WriteString("\033[2K")
		if r < len(evs) {
			b.WriteString(eventLine(evs[r]))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func eventLine(e client.Event) string {
	st := "\033[32m"
	switch {
	case e.Status >= 500:
		st = "\033[31m"
	case e.Status >= 400:
		st = "\033[33m"
	case e.Status >= 300:
		st = "\033[36m"
	}
	path := e.Path
	if len(path) > pathWidth {
		path = path[:pathWidth-3] + "..."
	}
	ts := time.UnixMilli(e.At).Format("15:04:05")
	if e.At == 0 {
		ts = timeNow()
	}
	return fmt.Sprintf("  \033[90m%s\033[0m  \033[1m%-7s\033[0m %s%3d\033[0m  \033[90m%5dms  %7s\033[0m  %s",
		ts, e.Method, st, e.Status, e.Time, humanSize(e.Size), path)
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

type CConfig struct {
	Server      string `json:"server"`
	Token       string `json:"token"`
	InspectPort int    `json:"inspectPort"`
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stdout)
		return
	}
	switch os.Args[1] {
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	case "version", "--version", "-v":
		fmt.Printf("inlet %s (%s)\n", strings.TrimPrefix(version, "v"), commit)
		return
	case "signup":
		cmdSignup(os.Args[2:])
		return
	case "login":
		cmdLogin(os.Args[2:])
		return
	case "config":
		cmdConfig(os.Args[2:])
		return
	case "doctor":
		cmdDoctor(os.Args[2:])
		return
	case "auth":
		cmdAuth(os.Args[2:])
	case "token":
		cmdToken(os.Args[2:])
		return
	case "http":
		port, inspect, newURL := parseHTTPArgs(os.Args[2:])
		cc := CConfig{}
		if err := config.Load("config.json", &cc); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if cc.Token == "" {
			fmt.Fprintln(os.Stderr, "no token configured - run 'inlet login' or 'inlet signup' (add --invite if required), or save one with 'inlet auth <token>'")
			os.Exit(1)
		}
		if cc.Server == "" {
			cc.Server = defaultServerURL
		} else {
			server, err := normalizeServerURL(cc.Server)
			if err != nil {
				fmt.Fprintln(os.Stderr, "invalid configured server URL:", err)
				fmt.Fprintln(os.Stderr, "set it with: inlet config set-server <url>")
				os.Exit(2)
			}
			cc.Server = server
		}
		if inspect != 0 {
			cc.InspectPort = inspect
		}
		if cc.InspectPort == 0 {
			cc.InspectPort = 4040
		}
		ins := client.NewInspector("", fmt.Sprintf("http://127.0.0.1:%d", port))
		go func() {
			if err := ins.Serve(fmt.Sprintf("127.0.0.1:%d", cc.InspectPort)); err != nil {
				fmt.Fprintln(os.Stderr, "inspector:", err)
			}
		}()
		// own the screen while running, restore the shell's scrollback on exit
		fmt.Print("\033[?1049h\033[?25l\033[H\033[2J")
		var statusMu sync.RWMutex
		connectionState := "connecting"
		connectionError := ""
		connectedAt := time.Time{}
		setConnectionState := func(state, detail string) {
			statusMu.Lock()
			connectionState = state
			connectionError = detail
			if state == "online" {
				connectedAt = time.Now()
			} else {
				connectedAt = time.Time{}
			}
			statusMu.Unlock()
		}
		printConnectionHeader("(connecting)", port, cc.InspectPort, 0, "connecting", "")
		go func() {
			first := true
			for {
				pub := ins.Public()
				if pub == "" {
					pub = "(connecting)"
				}
				statusMu.RLock()
				state, detail, since := connectionState, connectionError, connectedAt
				statusMu.RUnlock()
				up := time.Duration(0)
				if state == "online" && !since.IsZero() {
					up = time.Since(since)
				}
				printConnectionHeader(pub, port, cc.InspectPort, up, state, detail)
				total, s2, s3, s4, s5 := ins.Stats()
				fmt.Print(requestView(ins.Events(), s2, s3, s4, s5, total, first))
				first = false
				time.Sleep(time.Second)
			}
		}()

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		quit := make(chan struct{})
		go func() {
			<-sig
			close(quit)
		}()

		backoff := time.Second
	outer:
		for {
			select {
			case <-quit:
				break outer
			default:
			}
			setConnectionState("connecting", "")
			conn, reg, err := client.Connect(cc.Server, cc.Token, newURL, port, ins)
			if err != nil {
				if errors.Is(err, client.ErrAuth) {
					setConnectionState("error", fmt.Sprintf("%v; token may be invalid or revoked. Update it with: inlet auth <token>", err))
					<-quit
					break outer
				}
				setConnectionState("error", err.Error())
				select {
				case <-time.After(backoff):
				case <-quit:
					break outer
				}
				if backoff < 30*time.Second {
					backoff *= 2
				}
				continue
			}
			newURL = false
			pub := reg.PublicURL
			if pub == "" {
				su, err := url.Parse(cc.Server)
				if err == nil {
					scheme := "http"
					host := su.Hostname()
					if su.Scheme == "wss" {
						scheme = "https"
					}
					pub = fmt.Sprintf("%s://%s:8080/%s", scheme, host, reg.ID)
				}
			}
			ins.SetPublic(pub)
			ins.SetTarget(fmt.Sprintf("http://127.0.0.1:%d", port))
			setConnectionState("online", "")
			backoff = time.Second
			select {
			case <-conn.Done():
			case <-quit:
				conn.Close()
				break outer
			}
			conn.Close()
			setConnectionState("reconnecting", "control connection closed")
			select {
			case <-time.After(backoff):
			case <-quit:
				break outer
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
		fmt.Print("\033[?25h\033[?1049l")
	default:
		fmt.Fprintf(os.Stderr, "inlet: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(1)
	}
}

// parseHTTPArgs accepts the port before or after the flags, since flag stops
// parsing at the first positional argument.
func parseHTTPArgs(args []string) (port, inspect int, newURL bool) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		rest := append([]string{}, args[1:]...)
		args = append(rest, args[0])
	}
	fs := flag.NewFlagSet("http", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, httpUsage) }
	fs.BoolVar(&newURL, "new-url", false, "force new url")
	fs.IntVar(&inspect, "inspect", 0, "inspect port")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "inlet http: <port> required (try 'inlet http --help')")
		os.Exit(1)
	}
	parsedPort, err := strconv.Atoi(fs.Arg(0))
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		fmt.Fprintf(os.Stderr, "inlet http: invalid port %q (try 'inlet http --help')\n", fs.Arg(0))
		os.Exit(1)
	}
	port = parsedPort
	return port, inspect, newURL
}

func usage(w io.Writer) {
	fmt.Fprint(w, `inlet - path-based HTTP tunnel

Usage:
  inlet <command> [options]

Commands:
  signup     get a token from the server (invite or open signup)
  login      sign in with GitHub and get a client token
  config     configure the server URL
  doctor     check server connectivity and local setup
  auth       save an existing token
  token      list, label, or revoke your client tokens
  http       forward a local HTTP port through the tunnel
  version    print the version
  help       show this help

Run 'inlet <command> --help' for command options.

Client settings are saved locally. Set the server with
'inlet config set-server <url>' (default: %s).
`, defaultServerURL)
}

func cmdAuth(args []string) {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, authUsage) }
	if err := fs.Parse(args); err != nil {
		return
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "inlet auth: expected one token")
		fs.Usage()
		os.Exit(2)
	}
	cc := CConfig{}
	if err := config.Load("config.json", &cc); err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}
	cc.Token = fs.Arg(0)
	if cc.Server == "" {
		cc.Server = defaultServerURL
	}
	if cc.InspectPort == 0 {
		cc.InspectPort = 4040
	}
	if err := config.Save("config.json", cc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("✓ Token saved.")
}

var authUsage = fmt.Sprintf(`inlet auth - save an existing token

Usage:
  inlet auth <token>

Saves <token> locally; the server URL defaults to %s. Set another server with
'inlet config set-server <url>'.
Obtain a token with 'inlet signup' or from the server administrator.
`, defaultServerURL)

const httpUsage = `inlet http - forward a local HTTP port through the tunnel

Usage:
  inlet http <port> [--new-url] [--inspect <port>]

  <port>            local HTTP port to forward (http://127.0.0.1:<port>)
  --new-url         rotate the public tunnel ID on connect
  --inspect <port>  local inspector port (default 4040)

Flags may appear before or after <port>. The terminal shows the public URL,
request counts and the last requests; the inspector at
http://127.0.0.1:<inspect> keeps the last 200 requests. Server URL and token
come from your saved client settings. Ctrl-C exits.
`
