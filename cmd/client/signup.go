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
	"github.com/ynui/inlet/internal/labels"
)

func cmdSignup(args []string) {
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, signupUsage) }
	server := fs.String("server", "", "control server URL")
	invite := fs.String("invite", "", "invite code")
	label := fs.String("label", "", "token label (up to 64 characters)")
	fs.Parse(args)
	labelValue, err := labels.Normalize(*label)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signup:", err)
		os.Exit(2)
	}

	cc := CConfig{}
	if err := config.Load("config.json", &cc); err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}
	if *server != "" {
		cc.Server = *server
	}
	if cc.Server == "" {
		cc.Server = defaultServerURL
	} else {
		serverURL, err := normalizeServerURL(cc.Server)
		if err != nil {
			fmt.Fprintln(os.Stderr, "signup: invalid server URL:", err)
			os.Exit(2)
		}
		cc.Server = serverURL
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
			fmt.Fprintln(os.Stderr, "signup: this server requires an invite; ask its administrator for a one-time code or pass --invite <code>")
			os.Exit(1)
		}
	}

	body, _ := json.Marshal(map[string]string{"invite": *invite, "label": labelValue})
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
		fmt.Fprintf(os.Stderr, "signup succeeded, but saving the client config failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "save this token before exiting; it will not be shown again: %s\n", out.Token)
		os.Exit(1)
	}
	message := "Signup complete"
	if *invite != "" {
		message = "Invite claimed"
	}
	fmt.Printf("✓ %s. Token saved.\nserver: %s\npublic url: %s\nnext: inlet http <local-port>\n", message, cc.Server, out.PublicURL)
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

var signupUsage = fmt.Sprintf(`inlet signup - get a token from the server

Usage:
  inlet signup [--server <url>] [--invite <code>] [--label <name>]

  --server <url>   control server URL (default %s)
  --invite <code>  redeem a single-use invite code
  --label <name>   label stored on the server (up to 64 characters)

Without --invite the server must have open signup enabled. This works alongside
GitHub login if the server offers both. On success the token and server URL are
saved locally. The token is not printed; run 'inlet http <local-port>' to start
a tunnel.
`, defaultServerURL)
