package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/config"
)

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, doctorUsage) }
	bindFlag := fs.String("bind", "", "server HTTP bind to check (default: configured bind)")
	if err := fs.Parse(args); err != nil {
		return
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "inletd doctor: unexpected argument")
		fs.Usage()
		os.Exit(2)
	}

	sc := SConfig{}
	if err := config.Load("server.json", &sc); err != nil {
		fmt.Fprintln(os.Stderr, "load server config:", err)
		os.Exit(1)
	}
	addr := sc.Bind
	if addr == "" {
		addr = ":8080"
	}
	if *bindFlag != "" {
		addr = *bindFlag
	}
	if v := os.Getenv("INLET_BIND"); v != "" {
		addr = v
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server bind %q is not a host:port address: %v\n", addr, err)
		os.Exit(1)
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	healthURL := "http://" + net.JoinHostPort(host, port) + "/_inlet/healthz"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server health check failed (%s): %v\n", healthURL, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		fmt.Fprintf(os.Stderr, "server health check failed: HTTP %s %s\n", resp.Status, strings.TrimSpace(string(body)))
		os.Exit(1)
	}
	fmt.Printf("server health: ok (%s)\n", healthURL)
}

const doctorUsage = `inletd doctor - check whether the server is ready

Usage:
  inletd doctor [--bind <addr>]

Checks the local server health endpoint. Run inside the server container or
on the server host while inletd is running.
`
