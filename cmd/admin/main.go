package main

import (
	"bytes"
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

var version = "dev"
var commit = "unknown"
var defaultServerURL = "ws://localhost:8080"

type adminConfig struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage(os.Stdout)
		return
	}
	if os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Printf("inlet-admin %s (%s)\n", strings.TrimPrefix(version, "v"), commit)
		return
	}

	switch os.Args[1] {
	case "auth":
		cmdAuth(os.Args[2:])
	case "config":
		cmdConfig(os.Args[2:])
	case "invite":
		cmdInvite(os.Args[2:])
	case "token":
		cmdToken(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "inlet-admin: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func loadConfig() (adminConfig, error) {
	var c adminConfig
	if err := config.Load("admin.json", &c); err != nil {
		return c, err
	}
	if c.Server == "" {
		c.Server = defaultServerURL
	}
	return c, nil
}

func saveConfig(c adminConfig) error { return config.Save("admin.json", c) }

func cmdAuth(args []string) {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	server := fs.String("server", "", "admin server URL")
	fs.Usage = func() { fmt.Fprint(os.Stderr, "Usage: inlet-admin auth [--server <url>] <admin-token>\n") }
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	c, err := loadConfig()
	if err != nil {
		fail(err)
	}
	if *server != "" {
		c.Server, err = normalizeServer(*server)
		if err != nil {
			fail(err)
		}
	}
	c.Token = fs.Arg(0)
	if err := saveConfig(c); err != nil {
		fail(err)
	}
	path, err := config.Path("admin.json")
	if err != nil {
		fail(err)
	}
	fmt.Printf("✓ Admin token saved to %s\nserver: %s\n", path, c.Server)
}

func cmdConfig(args []string) {
	if len(args) == 0 {
		configUsage(os.Stderr)
		return
	}
	switch args[0] {
	case "set-server":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "Usage: inlet-admin config set-server <url>")
			os.Exit(2)
		}
		c, err := loadConfig()
		if err != nil {
			fail(err)
		}
		c.Server, err = normalizeServer(args[1])
		if err != nil {
			fail(err)
		}
		if err := saveConfig(c); err != nil {
			fail(err)
		}
		fmt.Printf("✓ Admin server saved: %s\n", c.Server)
	case "show":
		c, err := loadConfig()
		if err != nil {
			fail(err)
		}
		token := "not configured"
		if c.Token != "" {
			token = "configured (value hidden)"
		}
		var remote struct {
			OpenSignup bool `json:"openSignup"`
		}
		if err := request(http.MethodGet, "/_inlet/admin/config", nil, &remote); err != nil {
			fail(err)
		}
		signup := "disabled"
		if remote.OpenSignup {
			signup = "enabled"
		}
		fmt.Printf("server: %s\nadmin token: %s\nopen signup: %s\n", c.Server, token, signup)
	case "set":
		cmdConfigSet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "inlet-admin config: unknown command %q\n", args[0])
		configUsage(os.Stderr)
		os.Exit(2)
	}
}

func cmdConfigSet(args []string) {
	fs := flag.NewFlagSet("config set", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, "Usage: inlet-admin config set open-signup <true|false>\n") }
	_ = fs.Parse(args)
	if fs.NArg() != 2 || fs.Arg(0) != "open-signup" {
		fs.Usage()
		os.Exit(2)
	}
	open, err := parseBool(fs.Arg(1))
	if err != nil {
		fail(fmt.Errorf("open-signup must be true or false"))
	}
	var out struct {
		OpenSignup bool `json:"openSignup"`
	}
	if err := request(http.MethodPatch, "/_inlet/admin/config", map[string]bool{"openSignup": open}, &out); err != nil {
		fail(err)
	}
	state := "disabled"
	if out.OpenSignup {
		state = "enabled"
	}
	fmt.Printf("✓ Open signup %s.\n", state)
}

func cmdInvite(args []string) {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprintln(os.Stderr, "Usage: inlet-admin invite create [--ttl 24h] [--label NAME]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("invite create", flag.ExitOnError)
	ttl := fs.Duration("ttl", 24*time.Hour, "invite lifetime")
	label := fs.String("label", "", "label copied to the claimed token")
	_ = fs.Parse(args[1:])
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "inlet-admin invite create: unexpected argument")
		os.Exit(2)
	}
	var out struct {
		Code string `json:"code"`
		TTL  string `json:"ttl"`
	}
	if err := request(http.MethodPost, "/_inlet/admin/invites", map[string]string{
		"label": *label,
		"ttl":   ttl.String(),
	}, &out); err != nil {
		fail(err)
	}
	fmt.Printf("✓ Invite created. One-time code (copy it now):\n%s\nexpires in: %s\n", out.Code, out.TTL)
}

func cmdToken(args []string) {
	if len(args) == 0 {
		tokenUsage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		cmdTokenList(args[1:])
	case "create":
		cmdTokenCreate(args[1:])
	case "revoke":
		cmdTokenRevoke(args[1:])
	case "label":
		cmdTokenLabel(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "inlet-admin token: unknown command %q\n", args[0])
		tokenUsage(os.Stderr)
		os.Exit(2)
	}
}

func cmdTokenLabel(args []string) {
	if len(args) != 2 || len(args[0]) != 12 {
		fmt.Fprintln(os.Stderr, "Usage: inlet-admin token label <12-character-prefix> <label>")
		os.Exit(2)
	}
	var out struct {
		Label string `json:"label"`
	}
	if err := request(http.MethodPost, "/_inlet/admin/tokens/label", map[string]string{
		"prefix": args[0],
		"label":  args[1],
	}, &out); err != nil {
		fail(err)
	}
	if out.Label == "" {
		fmt.Printf("✓ Cleared label for token %s.\n", args[0])
		return
	}
	fmt.Printf("✓ Token %s label set to %q.\n", args[0], out.Label)
}

type adminTokenView struct {
	Prefix       string    `json:"prefix"`
	Source       string    `json:"source"`
	AccountLogin string    `json:"accountLogin"`
	TunnelID     string    `json:"tunnelID"`
	Label        string    `json:"label"`
	CreatedAt    time.Time `json:"createdAt"`
	LastUsedAt   time.Time `json:"lastUsedAt"`
	RevokedAt    time.Time `json:"revokedAt"`
}

func cmdTokenList(args []string) {
	fs := flag.NewFlagSet("token list", flag.ExitOnError)
	limit := fs.Int("limit", 50, "number of tokens per page (1-200)")
	offset := fs.Int("offset", 0, "number of tokens to skip")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *limit < 1 || *limit > 200 || *offset < 0 {
		fmt.Fprintln(os.Stderr, "Usage: inlet-admin token list [--limit 50] [--offset 0]")
		os.Exit(2)
	}
	var out struct {
		Tokens []adminTokenView `json:"tokens"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	path := fmt.Sprintf("/_inlet/admin/tokens?limit=%d&offset=%d", *limit, *offset)
	if err := request(http.MethodGet, path, nil, &out); err != nil {
		fail(err)
	}
	if len(out.Tokens) == 0 {
		if out.Total == 0 {
			fmt.Println("No tokens found.")
		} else {
			fmt.Printf("No tokens at offset %d (total %d).\n", out.Offset, out.Total)
		}
		return
	}
	first := out.Offset + 1
	last := out.Offset + len(out.Tokens)
	fmt.Printf("Showing tokens %d-%d of %d.\n", first, last, out.Total)
	fmt.Printf("%-12s %-8s %-12s %-16s %-18s %-18s %-18s %s\n", "PREFIX", "STATE", "SOURCE", "ACCOUNT", "CREATED", "LAST USED", "TUNNEL ID", "LABEL")
	for _, tok := range out.Tokens {
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
		fmt.Printf("%-12s %-8s %-12s %-16s %-18s %-18s %-18s %s\n", tok.Prefix, state, tok.Source, tok.AccountLogin, created, last, tok.TunnelID, tok.Label)
	}
}

func cmdTokenCreate(args []string) {
	fs := flag.NewFlagSet("token create", flag.ExitOnError)
	label := fs.String("label", "", "token label")
	admin := fs.Bool("admin", false, "create a token with remote admin privileges")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "inlet-admin token create: unexpected argument")
		os.Exit(2)
	}
	var out struct {
		Token    string `json:"token"`
		Prefix   string `json:"prefix"`
		TunnelID string `json:"tunnelID"`
		Source   string `json:"source"`
	}
	if err := request(http.MethodPost, "/_inlet/admin/tokens", map[string]any{
		"label": *label,
		"admin": *admin,
	}, &out); err != nil {
		fail(err)
	}
	if out.Source == "admin" {
		fmt.Printf("✓ Admin token created. Save it now; it will not be shown again.\ntoken: %s\nprefix: %s\n", out.Token, out.Prefix)
		return
	}
	fmt.Printf("✓ Token created. Save it now; it will not be shown again.\ntoken: %s\nprefix: %s\ntunnel ID: %s\n", out.Token, out.Prefix, out.TunnelID)
}

func cmdTokenRevoke(args []string) {
	if len(args) == 0 || len(args) > 100 {
		fmt.Fprintln(os.Stderr, "Usage: inlet-admin token revoke <prefix> [<prefix> ...] (up to 100)")
		os.Exit(2)
	}
	seen := make(map[string]bool, len(args))
	prefixes := make([]string, 0, len(args))
	for _, prefix := range args {
		if !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	var out struct {
		Revoked map[string]int64 `json:"revoked"`
	}
	if err := request(http.MethodPost, "/_inlet/admin/tokens/revoke", map[string][]string{"prefixes": prefixes}, &out); err != nil {
		fail(err)
	}
	var count int64
	missing := make([]string, 0)
	for _, prefix := range prefixes {
		n := out.Revoked[prefix]
		count += n
		if n == 0 {
			missing = append(missing, prefix)
		}
	}
	if count == 0 {
		fmt.Println("No matching active tokens found.")
		return
	}
	word := "tokens"
	if count == 1 {
		word = "token"
	}
	fmt.Printf("✓ Revoked %d %s.\n", count, word)
	if len(missing) > 0 {
		fmt.Printf("No active token found for: %s\n", strings.Join(missing, ", "))
	}
}

func request(method, path string, payload, result any) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if c.Token == "" {
		return fmt.Errorf("no admin token configured; run 'inlet-admin auth <admin-token>'")
	}
	endpoint, err := adminEndpoint(c.Server, path)
	if err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	if req.URL.Scheme == "http" && !isLoopback(req.URL.Hostname()) {
		return fmt.Errorf("refusing to send an admin token over plain HTTP; use HTTPS")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("admin request failed: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &failure)
		if failure.Error != "" {
			return fmt.Errorf("admin request: %s: %s", resp.Status, failure.Error)
		}
		return fmt.Errorf("admin request: %s", resp.Status)
	}
	if result != nil {
		if err := json.Unmarshal(b, result); err != nil {
			return fmt.Errorf("decode admin response: %w", err)
		}
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func adminEndpoint(server, path string) (string, error) {
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
		return "", fmt.Errorf("unsupported server scheme %q", u.Scheme)
	}
	route, query, _ := strings.Cut(path, "?")
	u.Path = route
	u.RawQuery = query
	u.Fragment = ""
	return u.String(), nil
}

func normalizeServer(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", raw)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("server URL must use ws, wss, http or https")
	}
	return u.String(), nil
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean")
	}
}

func configUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  inlet-admin config show
  inlet-admin config set-server <url>
  inlet-admin config set open-signup <true|false>
`)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `inlet-admin - manage an inlet server remotely

Usage:
  inlet-admin auth [--server <url>] <admin-token>
  inlet-admin invite create [--ttl 24h] [--label NAME]
  inlet-admin token list [--limit 50] [--offset 0]
  inlet-admin token create [--label NAME] [--admin]
  inlet-admin token label <12-character-prefix> <label>
  inlet-admin token revoke <prefix> [<prefix> ...]
  inlet-admin config show
  inlet-admin config set-server <url>
  inlet-admin config set open-signup <true|false>
  inlet-admin version

The admin token is stored separately from the regular client token.
Create the first admin token once with 'inletd token create --label admin'.
`)
}

func tokenUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  inlet-admin token list [--limit 50] [--offset 0]
  inlet-admin token create [--label NAME] [--admin]
  inlet-admin token label <12-character-prefix> <label>
  inlet-admin token revoke <prefix> [<prefix> ...]

Tokens are shown by prefix only. Raw tokens are printed once when created.
Use --admin only for a credential that should manage the server.
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "inlet-admin:", err)
	os.Exit(1)
}
