package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/config"
)

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, doctorUsage) }
	port := fs.Int("port", 0, "also check that a local app is listening on this port")
	if err := fs.Parse(args); err != nil {
		return
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "inlet doctor: unexpected argument")
		fs.Usage()
		os.Exit(2)
	}
	if *port < 0 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "inlet doctor: port must be between 1 and 65535")
		os.Exit(2)
	}

	cc := CConfig{}
	if err := config.Load("config.json", &cc); err != nil {
		fmt.Fprintln(os.Stderr, "inlet doctor: load config:", err)
		os.Exit(1)
	}
	configPath, err := config.Path("config.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "inlet doctor: config path:", err)
		os.Exit(1)
	}
	server := cc.Server
	if server == "" {
		server = defaultServerURL
	} else {
		server, err = normalizeServerURL(server)
		if err != nil {
			fmt.Fprintln(os.Stderr, "inlet doctor: invalid server URL:", err)
			os.Exit(1)
		}
	}
	tokenState := "not configured"
	if cc.Token != "" {
		tokenState = "configured (value hidden)"
	}
	fmt.Printf("config: %s\nserver: %s\ntoken: %s\n", configPath, server, tokenState)

	failed := false
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	controlURL, err := clientHTTPURL(server, "/_inlet/control")
	if err != nil {
		fmt.Fprintln(os.Stderr, "control endpoint: invalid URL:", err)
		failed = true
	} else {
		resp, body, reqErr := getDiagnostic(client, controlURL)
		if reqErr != nil {
			fmt.Fprintf(os.Stderr, "control endpoint: unreachable (%v)\ncheck that the server is running and Nginx forwards /_inlet/control\n", reqErr)
			failed = true
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusBadRequest {
				fmt.Printf("control endpoint: reachable (%s)\n", resp.Status)
			} else {
				fmt.Fprintf(os.Stderr, "control endpoint: unexpected response (%s): %s\ncheck the Nginx route for /_inlet/control\n", resp.Status, body)
				failed = true
			}
		}
	}

	signupEndpoint, err := signupURL(server)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signup endpoint: invalid URL:", err)
		failed = true
	} else {
		resp, body, reqErr := getDiagnostic(client, signupEndpoint)
		if reqErr != nil {
			fmt.Fprintf(os.Stderr, "signup endpoint: unreachable (%v)\ncheck that Nginx forwards /_inlet/signup\n", reqErr)
			failed = true
		} else {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				fmt.Fprintf(os.Stderr, "signup endpoint: unexpected response (%s): %s\n", resp.Status, body)
				failed = true
			} else {
				var info struct {
					Open bool `json:"open"`
				}
				if err := json.Unmarshal([]byte(body), &info); err != nil {
					fmt.Fprintln(os.Stderr, "signup endpoint: invalid server response:", err)
					failed = true
				} else if info.Open {
					fmt.Println("signup: open (no invite required)")
				} else {
					fmt.Println("signup: invite required")
				}
			}
		}
	}

	if *port > 0 {
		address := net.JoinHostPort("127.0.0.1", fmt.Sprint(*port))
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "local app: not reachable at http://%s (%v)\n", address, err)
			failed = true
		} else {
			conn.Close()
			fmt.Printf("local app: reachable at http://%s\n", address)
		}
	}

	if failed {
		os.Exit(1)
	}
}

func clientHTTPURL(server, path string) (string, error) {
	u, err := url.Parse(server)
	if err != nil {
		return "", err
	}
	if u.Scheme == "wss" {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func getDiagnostic(client *http.Client, endpoint string) (*http.Response, string, error) {
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, "", err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if readErr != nil {
		resp.Body.Close()
		return nil, "", readErr
	}
	return resp, strings.TrimSpace(string(body)), nil
}

const doctorUsage = `inlet doctor - check client and server connectivity

Usage:
  inlet doctor [--port <port>]

Checks the saved server URL and its control/signup routes. Use --port to also
check that a local app is listening.
`
