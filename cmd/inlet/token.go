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

type clientTokenView struct {
	Prefix       string    `json:"prefix"`
	Label        string    `json:"label"`
	AccountLogin string    `json:"accountLogin"`
	TunnelID     string    `json:"tunnelID"`
	CreatedAt    time.Time `json:"createdAt"`
	LastUsedAt   time.Time `json:"lastUsedAt"`
	RevokedAt    time.Time `json:"revokedAt"`
}

func cmdToken(args []string) {
	if len(args) == 0 {
		tokenUsage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			tokenUsage(os.Stderr)
			os.Exit(2)
		}
		cmdTokenList()
	case "revoke":
		cmdTokenRevoke(args[1:])
	case "label":
		cmdTokenLabel(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "inlet token: unknown command %q\n", args[0])
		tokenUsage(os.Stderr)
		os.Exit(2)
	}
}

func cmdTokenLabel(args []string) {
	if len(args) != 2 || len(args[0]) != 12 {
		fmt.Fprintln(os.Stderr, "Usage: inlet token label <12-character-prefix> <label>")
		os.Exit(2)
	}
	var out struct {
		Label string `json:"label"`
	}
	if err := clientTokenRequest(http.MethodPost, "/_inlet/tokens/label", map[string]string{
		"prefix": args[0],
		"label":  args[1],
	}, &out); err != nil {
		clientTokenFail(err)
	}
	if out.Label == "" {
		fmt.Printf("✓ Cleared label for token %s.\n", args[0])
		return
	}
	fmt.Printf("✓ Token %s label set to %q.\n", args[0], out.Label)
}

func cmdTokenList() {
	var tokens []clientTokenView
	if err := clientTokenRequest(http.MethodGet, "/_inlet/tokens", nil, &tokens); err != nil {
		clientTokenFail(err)
	}
	if len(tokens) == 0 {
		fmt.Println("No tokens found.")
		return
	}
	if tokens[0].AccountLogin != "" {
		fmt.Printf("Tokens for GitHub account @%s:\n", tokens[0].AccountLogin)
	} else {
		fmt.Println("Your token and open-signup tokens associated with this signup IP:")
	}
	fmt.Printf("%-12s %-8s %-18s %-18s %-18s %s\n", "PREFIX", "STATE", "CREATED", "LAST USED", "TUNNEL ID", "LABEL")
	for _, tok := range tokens {
		state, created, last := "active", "unknown", "never"
		if !tok.RevokedAt.IsZero() {
			state = "revoked"
		}
		if !tok.CreatedAt.IsZero() {
			created = tok.CreatedAt.Local().Format("2006-01-02 15:04")
		}
		if !tok.LastUsedAt.IsZero() {
			last = tok.LastUsedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Printf("%-12s %-8s %-18s %-18s %-18s %s\n", tok.Prefix, state, created, last, tok.TunnelID, tok.Label)
	}
}

func cmdTokenRevoke(args []string) {
	fs := flag.NewFlagSet("token revoke", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, "Usage: inlet token revoke <12-character-prefix>\n") }
	_ = fs.Parse(args)
	if fs.NArg() != 1 || len(fs.Arg(0)) != 12 {
		fs.Usage()
		os.Exit(2)
	}
	var out struct {
		Revoked int64 `json:"revoked"`
	}
	if err := clientTokenRequest(http.MethodPost, "/_inlet/tokens/revoke", map[string]string{"prefix": fs.Arg(0)}, &out); err != nil {
		clientTokenFail(err)
	}
	fmt.Printf("✓ Revoked token %s.\n", fs.Arg(0))
}

func clientTokenRequest(method, path string, payload, result any) error {
	cc := CConfig{}
	if err := config.Load("config.json", &cc); err != nil {
		return fmt.Errorf("load client config: %w", err)
	}
	if cc.Token == "" {
		return fmt.Errorf("no token configured; run 'inlet login' or 'inlet signup' first")
	}
	server := cc.Server
	if server == "" {
		server = defaultServerURL
	}
	u, err := url.Parse(server)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return fmt.Errorf("unsupported server URL scheme %q", u.Scheme)
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""

	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cc.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &out)
		if out.Error != "" {
			return fmt.Errorf("%s: %s", resp.Status, out.Error)
		}
		return fmt.Errorf("%s", resp.Status)
	}
	if result != nil {
		if err := json.Unmarshal(b, result); err != nil {
			return fmt.Errorf("decode token response: %w", err)
		}
	}
	return nil
}

func clientTokenFail(err error) {
	fmt.Fprintln(os.Stderr, "inlet token:", err)
	os.Exit(1)
}

const tokenCommandUsage = `inlet token - manage your client tokens

Usage:
  inlet token list
  inlet token label <12-character-prefix> <label>
  inlet token revoke <12-character-prefix>

Use quotes around labels containing spaces. Labels may be up to 64 characters;
pass an empty label to clear it. The saved client token is used to authenticate
these requests.
`

func tokenUsage(w io.Writer) { fmt.Fprint(w, tokenCommandUsage) }
