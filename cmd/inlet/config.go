package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"

	"github.com/ynui/inlet/internal/config"
)

// Release builds override this to the hosted service; source builds stay local.
var defaultServerURL = "ws://localhost:8080"

func cmdConfig(args []string) {
	root := flag.NewFlagSet("config", flag.ExitOnError)
	root.Usage = func() { fmt.Fprint(os.Stderr, configUsage) }
	if len(args) == 0 {
		root.Usage()
		return
	}
	switch args[0] {
	case "set-server":
		cmdConfigSetServer(args[1:])
	case "show":
		cmdConfigShow(args[1:])
	default:
		if err := root.Parse(args); err != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "inlet config: unknown command %q\n\n", root.Arg(0))
		root.Usage()
		os.Exit(2)
	}
}

func cmdConfigShow(args []string) {
	fs := flag.NewFlagSet("config show", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, configShowUsage) }
	if err := fs.Parse(args); err != nil {
		return
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "inlet config show: unexpected argument")
		fs.Usage()
		os.Exit(2)
	}
	cc := CConfig{}
	if err := config.Load("config.json", &cc); err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}
	server := cc.Server
	if server == "" {
		server = defaultServerURL + " (default)"
	}
	token := "not configured"
	if cc.Token != "" {
		token = "configured (value hidden)"
	}
	inspectPort := cc.InspectPort
	if inspectPort == 0 {
		inspectPort = 4040
	}
	path, err := config.Path("config.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "config path:", err)
		os.Exit(1)
	}
	fmt.Printf("server: %s\ntoken: %s\ninspector port: %d\nconfig: %s\n", server, token, inspectPort, path)
}

func cmdConfigSetServer(args []string) {
	fs := flag.NewFlagSet("config set-server", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, configSetServerUsage) }
	if err := fs.Parse(args); err != nil {
		return
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "inlet config set-server: expected one server URL")
		fs.Usage()
		os.Exit(2)
	}
	server, err := normalizeServerURL(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "inlet config set-server:", err)
		os.Exit(2)
	}

	cc := CConfig{}
	if err := config.Load("config.json", &cc); err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}
	cc.Server = server
	if err := config.Save("config.json", cc); err != nil {
		fmt.Fprintln(os.Stderr, "save config:", err)
		os.Exit(1)
	}
	path, err := config.Path("config.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "server URL saved, but could not resolve config path: %v\n", err)
		return
	}
	fmt.Printf("✓ Server saved: %s\nconfig: %s\n", server, path)
}

func normalizeServerURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("URL must start with ws://, wss://, http://, or https://")
	}
	if u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("enter a server origin, such as wss://tunnel.example.com")
	}
	if u.Path == "/" {
		u.Path = ""
		u.RawPath = ""
	}
	if u.Path != "" && u.Path != "/_inlet/control" {
		return "", fmt.Errorf("path must be empty or /_inlet/control")
	}
	return u.String(), nil
}

const configUsage = `inlet config - configure the client

Usage:
  inlet config show              show current settings (token hidden)
  inlet config set-server <url>   set the control server URL
  inlet config --help             show this help
`

const configShowUsage = `inlet config show - show current client settings

Usage:
  inlet config show

The token value is hidden.
`

const configSetServerUsage = `inlet config set-server - set the control server URL

Usage:
  inlet config set-server <url>

Use ws:// or http:// for a local HTTP server, or wss:// or https:// for a TLS
reverse proxy. HTTP schemes are converted to their WebSocket equivalents. The
server URL is saved locally and the control path is appended automatically
when omitted.
`
