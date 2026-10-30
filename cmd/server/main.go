package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/config"
	"github.com/ynui/inlet/internal/server"
	"github.com/ynui/inlet/internal/store"
)

type SConfig struct {
	Bind        string   `json:"bind"`
	ControlAddr string   `json:"controlAddr"`
	Tokens      []string `json:"tokens"`
	PublicBase  string   `json:"publicBase"`
	OpenSignup  bool     `json:"openSignup"`
}

func main() {
	_ = config.LoadEnvFile(".env")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			serverUsage(os.Stdout)
			return
		case "invite", "token":
			// admin commands, dispatched after the DB is open
		default:
			if !strings.HasPrefix(os.Args[1], "-") {
				fmt.Fprintf(os.Stderr, "inletd: unknown command %q\n\n", os.Args[1])
				serverUsage(os.Stderr)
				os.Exit(1)
			}
		}
	}

	dbPath, err := databasePath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open db:", err)
		os.Exit(1)
	}
	defer st.Close()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "invite":
			cmdInvite(st, os.Args[2:])
			return
		case "token":
			cmdToken(st, os.Args[2:])
			return
		}
	}

	serve(st, dbPath)
}

func serve(st *store.Store, dbPath string) {
	var (
		bind    = flag.String("bind", ":8080", "public http bind")
		control = flag.String("control", ":8081", "control ws bind")
	)
	flag.Usage = func() { serverUsage(os.Stderr) }
	flag.Parse()

	sc := SConfig{}
	_ = config.Load("server.json", &sc)
	if v := os.Getenv("INLET_BIND"); v != "" {
		sc.Bind = v
	}
	if v := os.Getenv("INLET_CONTROL_ADDR"); v != "" {
		sc.ControlAddr = v
	}
	if v := os.Getenv("INLET_TOKENS"); v != "" {
		tokens := []string{}
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tokens = append(tokens, t)
			}
		}
		if len(tokens) > 0 {
			sc.Tokens = tokens
		}
	}
	if v := os.Getenv("INLET_PUBLIC_BASE"); v != "" {
		sc.PublicBase = v
	}
	if v := os.Getenv("INLET_OPEN_SIGNUP"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			sc.OpenSignup = b
		}
	}
	if sc.Bind != "" {
		*bind = sc.Bind
	}
	if sc.ControlAddr != "" {
		*control = sc.ControlAddr
	}

	migrateTokens(st, sc.Tokens)

	staleAfter := envDuration("INLET_STALE_AFTER", 30*24*time.Hour)
	openStaleAfter := envDuration("INLET_OPEN_STALE_AFTER", 7*24*time.Hour)
	revokedRetention := envDuration("INLET_REVOKED_RETENTION", 7*24*time.Hour)

	hub := server.NewHub(st)
	hub.SetPublicBase(sc.PublicBase)
	hub.SetPublicAddr(*bind)
	proxy := server.NewProxy(hub)
	signup := server.NewSignup(st, hub, sc.OpenSignup)

	go janitor(st, hub, staleAfter, openStaleAfter, revokedRetention)

	controlMux := http.NewServeMux()
	controlMux.HandleFunc("/_inlet/control", hub.HandleControl)
	controlMux.HandleFunc("/_inlet/signup", signup.Handle)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/", proxy.HandlePublic)

	go func() {
		fmt.Printf("control on %s\n", *control)
		if err := http.ListenAndServe(*control, controlMux); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	fmt.Printf("public on %s\n", *bind)
	fmt.Printf("database %s\n", dbPath)
	if err := http.ListenAndServe(*bind, publicMux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func janitor(st *store.Store, hub *server.Hub, stale, openStale, revoked time.Duration) {
	run := func() {
		n, err := st.Cleanup(time.Now(), stale, openStale, revoked, hub.Online)
		if err != nil {
			log.Printf("cleanup: %v", err)
		} else if n > 0 {
			log.Printf("cleanup: removed %d stale tokens/invites", n)
		}
	}
	run()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}

func migrateTokens(st *store.Store, tokens []string) {
	if len(tokens) == 0 {
		return
	}
	n, err := st.Count()
	if err != nil || n > 0 {
		return
	}
	var assignments map[string]struct {
		ID string `json:"id"`
	}
	_ = config.Load("assignments.json", &assignments)
	for _, raw := range tokens {
		tid := ""
		if a, ok := assignments[raw]; ok {
			tid = a.ID
		}
		if err := st.Seed(raw, "bootstrap", tid); err != nil {
			log.Printf("migrate token: %v", err)
		}
	}
	log.Printf("migrated %d bootstrap tokens into the database", len(tokens))
}

func databasePath() (string, error) {
	if v := os.Getenv("INLET_DB"); v != "" {
		return v, nil
	}
	return "data/inlet.db", nil
}

func envDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("invalid %s: %v", name, err)
		return def
	}
	return d
}

func cmdInvite(st *store.Store, args []string) {
	if len(args) > 0 && args[0] == "create" {
		args = args[1:]
	}
	if wantsHelp(args) {
		fmt.Print(inviteUsage)
		return
	}
	fs := flag.NewFlagSet("invite", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, inviteUsage) }
	ttl := fs.Duration("ttl", envDuration("INLET_INVITE_TTL", 24*time.Hour), "invite lifetime")
	label := fs.String("label", "", "invite label")
	fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "inletd invite: unexpected argument (try 'inletd invite --help')")
		os.Exit(1)
	}
	code, err := st.CreateInvite(*label, *ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(code)
}

func cmdToken(st *store.Store, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, tokenUsage)
		os.Exit(1)
	}
	if args[0] == "help" || wantsHelp(args) {
		fmt.Print(tokenUsage)
		return
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("token create", flag.ExitOnError)
		fs.Usage = func() { fmt.Fprint(os.Stderr, tokenUsage) }
		label := fs.String("label", "", "token label")
		fs.Parse(args[1:])
		raw, tok, err := st.Issue(*label, "admin", "")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("token: %s\nid: %s\n", raw, tok.TunnelID)
	case "list":
		tokens, err := st.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, t := range tokens {
			last := "never"
			if !t.LastUsedAt.IsZero() {
				last = t.LastUsedAt.Format("2006-01-02 15:04")
			}
			state := "active"
			if !t.RevokedAt.IsZero() {
				state = "revoked"
			}
			fmt.Printf("%s  %-7s  %-10s  %s  last=%s  %s\n",
				t.Prefix, t.Source, t.TunnelID, t.Label, last, state)
		}
	case "revoke":
		if len(args) < 2 {
			fmt.Fprint(os.Stderr, tokenUsage)
			os.Exit(1)
		}
		n, err := st.Revoke(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("revoked %d token(s)\n", n)
	default:
		fmt.Fprintf(os.Stderr, "inletd token: unknown subcommand %q\n\n", args[0])
		fmt.Fprint(os.Stderr, tokenUsage)
		os.Exit(1)
	}
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func serverUsage(w io.Writer) {
	fmt.Fprint(w, `inletd - inlet tunnel server

Usage:
  inletd [flags]                 start the server
  inletd <command> [options]     admin commands (local DB, server may be running)

Server flags:
  -bind <addr>      public HTTP listener (default ":8080")
  -control <addr>   control WebSocket listener (default ":8081")

Commands:
  token create [--label NAME]    issue a token; printed once
  token list                     list tokens and their state
  token revoke <prefix>          revoke active tokens by prefix
  invite create [--ttl 24h] [--label NAME]
                                 create a single-use invite code
  help                           show this help

Configuration (highest wins): INLET_* env > ~/.config/inlet/server.json > flags.
  ./.env is loaded at startup without overwriting exported variables.
  INLET_BIND              public HTTP listener
  INLET_CONTROL_ADDR      control WebSocket listener
  INLET_PUBLIC_BASE       base URL advertised to clients (e.g. https://example.com)
  INLET_OPEN_SIGNUP       allow signup without an invite (default false)
  INLET_DB                SQLite path (default ./data/inlet.db, relative to the
                          working directory; admin commands must run in the
                          same directory)
  INLET_STALE_AFTER       unused admin/invite token TTL (default 720h)
  INLET_OPEN_STALE_AFTER  unused open-signup token TTL (default 168h)
  INLET_REVOKED_RETENTION revoked token retention (default 168h)
  INLET_INVITE_TTL        default invite lifetime (default 24h)
  INLET_TOKENS            bootstrap tokens, imported once when the DB is empty

Run 'inletd token --help' or 'inletd invite --help' for command details.
`)
}

const tokenUsage = `inletd token - manage tunnel tokens

Usage:
  inletd token create [--label NAME]   issue a token; printed once
  inletd token list                    list prefix, source, tunnel id, label,
                                       last use and state
  inletd token revoke <prefix>         revoke active tokens whose prefix matches
  inletd token --help                  show this help

<prefix> is the 12-character prefix shown by 'list'. Tokens are stored hashed;
the raw token is only printed by 'create'. Revocation applies to new
connections; a connected client keeps its session until it reconnects.
`

const inviteUsage = `inletd invite - create single-use invite codes

Usage:
  inletd invite create [--ttl 24h] [--label NAME]
  inletd invite [--ttl 24h] [--label NAME]    ("create" may be omitted)
  inletd invite --help                        show this help

  --ttl <duration>  lifetime, e.g. 1h or 168h (default INLET_INVITE_TTL or 24h)
  --label NAME      label copied to the token created from this invite

Codes are stored hashed, expire after --ttl and are deleted after use. Redeem
with 'inlet signup --invite <code>' on the client.
`
