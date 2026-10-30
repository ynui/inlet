package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/ynui/inlet/internal/config"
)

func cmdSignup(args []string) {
	if wantsHelp(args) {
		fmt.Print(signupUsage)
		return
	}
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, signupUsage) }
	server := fs.String("server", "", "control server URL")
	invite := fs.String("invite", "", "invite code")
	label := fs.String("label", "", "token label")
	fs.Parse(args)

	cc := CConfig{}
	_ = config.Load("config.json", &cc)
	if *server != "" {
		cc.Server = *server
	}
	if cc.Server == "" {
		cc.Server = "ws://localhost:8081"
	}
	if cc.InspectPort == 0 {
		cc.InspectPort = 4040
	}

	endpoint, err := signupURL(cc.Server)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	httpClient := &http.Client{Timeout: 15 * time.Second}

	if *invite == "" {
		resp, err := httpClient.Get(endpoint)
		if err != nil {
			fmt.Fprintln(os.Stderr, "signup:", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "signup: %s\n", resp.Status)
			os.Exit(1)
		}
		var info struct {
			Open bool `json:"open"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&info)
		if !info.Open {
			fmt.Fprintln(os.Stderr, "signup: invite required")
			os.Exit(1)
		}
	}

	body, _ := json.Marshal(map[string]string{"invite": *invite, "label": *label})
	resp, err := httpClient.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "signup:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		fmt.Fprintf(os.Stderr, "signup: %s: %s\n", resp.Status, bytes.TrimSpace(msg))
		os.Exit(1)
	}
	var out struct {
		Token     string `json:"token"`
		ID        string `json:"id"`
		PublicURL string `json:"publicUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fmt.Fprintln(os.Stderr, "signup:", err)
		os.Exit(1)
	}
	cc.Token = out.Token
	if err := config.Save("config.json", cc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("token saved\npublic url: %s\ntoken: %s\n", out.PublicURL, out.Token)
}

func signupURL(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported server scheme: %s", u.Scheme)
	}
	u.Path = "/_inlet/signup"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

const signupUsage = `inlet signup - get a token from the server

Usage:
  inlet signup [--server <url>] [--invite <code>] [--label <name>]

  --server <url>   control server URL (default ws://localhost:8081)
  --invite <code>  redeem a single-use invite code
  --label <name>   label stored on the server (shown in its token list)

Without --invite the server must have open signup enabled; the client asks
first and exits if it is disabled. On success the token is saved to
~/.config/inlet/config.json and printed once.
`
