package main

import (
	"flag"
	"fmt"
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
	requestRows = 6
	pathWidth   = 40
	// paneLines is the redrawn region: stats line, a gap, then the rows.
	paneLines = requestRows + 2
)

var version = "dev"

func printHeader(pub string, port, inspectPort int) {
	fmt.Println()
	fmt.Printf("  \033[1;36minlet\033[0m\n")
	fmt.Printf("  \033[90mSession Status\033[0m                \033[32monline\033[0m\n")
	fmt.Printf("  \033[90mForwarding\033[0m                    \033[1m%s\033[0m \033[36m➜\033[0m http://127.0.0.1:%d\n", pub, port)
	fmt.Printf("  \033[90mWeb Interface\033[0m                 http://127.0.0.1:%d\n", inspectPort)
	fmt.Println()
}

// renderRequests keeps the stats line and the last 6 requests on screen,
// redrawing them in place so the header never scrolls.
func renderRequests(ins *client.Inspector) {
	var seen int64 = -1
	first := true
	for {
		total, s2, s3, s4, s5 := ins.Stats()
		if total != seen {
			seen = total
			fmt.Print(requestView(ins.Events(), s2, s3, s4, s5, first))
			first = false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// requestView renders paneLines lines, cursor left on the line below the pane.
func requestView(evs []client.Event, s2, s3, s4, s5 int64, first bool) string {
	var b strings.Builder
	if !first {
		fmt.Fprintf(&b, "\033[%dA", paneLines)
	}
	b.WriteString("\033[2K")
	fmt.Fprintf(&b, "  \033[90mRequests\033[0m   \033[32m2xx %d\033[0m   \033[36m3xx %d\033[0m   \033[33m4xx %d\033[0m   \033[31m5xx %d\033[0m\n", s2, s3, s4, s5)
	b.WriteString("\033[2K\n")
	if len(evs) > requestRows {
		evs = evs[:requestRows]
	}
	for r := 0; r < requestRows; r++ {
		b.WriteString("\033[2K")
		if r < len(evs) {
			b.WriteString(eventLine(evs[len(evs)-1-r]))
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
	return fmt.Sprintf("  \033[90m%s\033[0m  \033[1m%-6s\033[0m %s%3d\033[0m  \033[90m%5dms  %7s\033[0m  %s",
		timeNow(), e.Method, st, e.Status, e.Time, humanSize(e.Size), path)
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
		usage()
		return
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("inlet %s\n", version)
		return
	case "auth":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: inlet auth <token>")
			os.Exit(1)
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
		_, reg, err := client.Connect(cc.Server, cc.Token, newURL, port, ins)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		pub := reg.PublicURL
		if pub == "" {
			su, err := url.Parse(cc.Server)
			if err == nil {
				scheme := "http"
				host := su.Hostname()
				if su.Scheme == "wss" {
					scheme = "https"
				}
				// best effort when INLET_PUBLIC_BASE is unset: assume the public
				// listener is on :8080 next to the control endpoint.
				pub = fmt.Sprintf("%s://%s:8080/%s", scheme, host, reg.ID)
			}
		}
		ins.SetPublic(pub)
		ins.SetTarget(fmt.Sprintf("http://127.0.0.1:%d", port))
		// own the screen while running, restore the shell's scrollback on exit
		fmt.Print("\033[?1049h\033[?25l\033[H\033[2J")
		printHeader(pub, port, cc.InspectPort)
		go renderRequests(ins)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		fmt.Print("\033[?25h\033[?1049l")
	default:
		usage()
	}
}

// parseHTTPArgs accepts the port before or after the flags, since flag stops
// parsing at the first positional argument.
func parseHTTPArgs(args []string) (port, inspect int, newURL bool) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(args[1:], args[0])
	}
	fs := flag.NewFlagSet("http", flag.ExitOnError)
	fs.BoolVar(&newURL, "new-url", false, "force new url")
	fs.IntVar(&inspect, "inspect", 0, "inspect port")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: inlet http <port>")
		os.Exit(1)
	}
	if _, err := fmt.Sscanf(fs.Arg(0), "%d", &port); err != nil || port == 0 {
		fmt.Fprintln(os.Stderr, "usage: inlet http <port>")
		os.Exit(1)
	}
	return port, inspect, newURL
}

func usage() {
	fmt.Print(`inlet - path based http tunnel

Usage:
  inlet auth <token>
  inlet http <port> [--new-url] [--inspect <port>]
  inlet version
`)
}
