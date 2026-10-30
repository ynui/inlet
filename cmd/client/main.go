package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
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

// printHeader paints the static header with absolute cursor positions so a
// reconnect can redraw it in place instead of stacking a second copy.
func printHeader(pub string, port, inspectPort int, up time.Duration) {
	fmt.Printf("\033[2;1H\033[2K  \033[1;36minlet\033[0m")
	fmt.Printf("\033[3;1H\033[2K  \033[90mSession Status\033[0m                \033[32monline\033[0m   \033[90mup %s\033[0m", humanDuration(up))
	fmt.Printf("\033[4;1H\033[2K  \033[90mForwarding\033[0m                    \033[1m%s\033[0m \033[36m➜\033[0m http://127.0.0.1:%d", pub, port)
	fmt.Printf("\033[5;1H\033[2K  \033[90mWeb Interface\033[0m                 http://127.0.0.1:%d", inspectPort)
	fmt.Print("\033[6;1H\033[2K\033[7;1H")
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
		fmt.Printf("inlet %s\n", version)
		return
	case "signup":
		cmdSignup(os.Args[2:])
		return
	case "auth":
		if wantsHelp(os.Args[2:]) {
			fmt.Print(authUsage)
			return
		}
		if len(os.Args) < 3 {
			authUsageError()
		}
		cc := CConfig{}
		_ = config.Load("config.json", &cc)
		cc.Token = os.Args[2]
		if cc.Server == "" {
			cc.Server = "ws://localhost:8081"
		}
		if cc.InspectPort == 0 {
			cc.InspectPort = 4040
		}
		if err := config.Save("config.json", cc); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("saved")
		return
	case "http":
		port, inspect, newURL := parseHTTPArgs(os.Args[2:])
		cc := CConfig{}
		if err := config.Load("config.json", &cc); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if cc.Token == "" {
			fmt.Fprintln(os.Stderr, "no token - run: inlet auth <token>")
			os.Exit(1)
		}
		if cc.Server == "" {
			cc.Server = "ws://localhost:8081"
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
		started := time.Now()
		printHeader("(connecting)", port, cc.InspectPort, 0)
		go func() {
			first := true
			for {
				up := time.Since(started)
				pub := ins.Public()
				if pub == "" {
					pub = "(connecting)"
				}
				printHeader(pub, port, cc.InspectPort, up)
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
			conn, reg, err := client.Connect(cc.Server, cc.Token, newURL, port, ins)
			if err != nil {
				if errors.Is(err, client.ErrAuth) {
					fmt.Print("\033[?25h\033[?1049l")
					fmt.Fprintf(os.Stderr, "inlet: %v\n", err)
					fmt.Fprintln(os.Stderr, "  your token is invalid or revoked. save a new one with: inlet auth <token>")
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "connect: %v\n", err)
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
			printHeader(pub, port, cc.InspectPort, time.Since(started))
			backoff = time.Second
			select {
			case <-conn.Done():
			case <-quit:
				conn.Close()
				break outer
			}
			conn.Close()
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
	if wantsHelp(args) {
		fmt.Print(httpUsage)
		os.Exit(0)
	}
	fs := flag.NewFlagSet("http", flag.ExitOnError)
	fs.BoolVar(&newURL, "new-url", false, "force new url")
	fs.IntVar(&inspect, "inspect", 0, "inspect port")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "inlet http: <port> required (try 'inlet http --help')")
		os.Exit(1)
	}
	if _, err := fmt.Sscanf(fs.Arg(0), "%d", &port); err != nil || port == 0 {
		fmt.Fprintf(os.Stderr, "inlet http: invalid port %q (try 'inlet http --help')\n", fs.Arg(0))
		os.Exit(1)
	}
	return port, inspect, newURL
}

func usage(w io.Writer) {
	fmt.Fprint(w, `inlet - path-based HTTP tunnel

Usage:
  inlet <command> [options]

Commands:
  signup     get a token from the server (invite or open signup)
  auth       save an existing token
  http       forward a local HTTP port through the tunnel
  version    print the version
  help       show this help

Run 'inlet <command> --help' for command options.

Config: ~/.config/inlet/config.json holds the server URL, token and inspector
port. The server defaults to ws://localhost:8081; a URL without a path gets
/_inlet/control appended.
`)
}

func authUsageError() {
	fmt.Fprint(os.Stderr, authUsage)
	os.Exit(1)
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

const authUsage = `inlet auth - save an existing token

Usage:
  inlet auth <token>

Saves <token> to ~/.config/inlet/config.json; the server URL defaults to
ws://localhost:8081. Obtain a token with 'inlet signup' or from the server
administrator.
`

const httpUsage = `inlet http - forward a local HTTP port through the tunnel

Usage:
  inlet http <port> [--new-url] [--inspect <port>]

  <port>            local HTTP port to forward (http://127.0.0.1:<port>)
  --new-url         rotate the public tunnel ID on connect
  --inspect <port>  local inspector port (default 4040)

Flags may appear before or after <port>. The terminal shows the public URL,
request counts and the last requests; the inspector at
http://127.0.0.1:<inspect> keeps the last 200 requests. Server URL and token
come from ~/.config/inlet/config.json. Ctrl-C exits.
`
