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

var version = "dev"
var commit = "unknown"

type SConfig struct {
	Bind        string `json:"bind"`
	ControlAddr string `json:"controlAddr"`
	PublicBase  string `json:"publicBase"`
	OpenSignup  bool   `json:"openSignup"`
}

func main() {
	_ = config.LoadEnvFile(".env")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			serverUsage(os.Stdout)
			return
		case "version", "--version", "-v":
			fmt.Printf("inletd %s (%s)\n", strings.TrimPrefix(version, "v"), commit)
			return
		case "invite", "token":
			// admin commands, dispatched after the DB is open
		case "doctor":
			cmdDoctor(os.Args[2:])
			return
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
		bind    = flag.String("bind", "", "public http bind")
		control = flag.String("control", "", "optional separate control bind (default: same as -bind)")
	)
	flag.Usage = func() { serverUsage(os.Stderr) }
	flag.Parse()

	sc := SConfig{}
	_ = config.Load("server.json", &sc)
	bindAddr := sc.Bind
	if bindAddr == "" {
		bindAddr = ":8080"
	}
	controlAddr := sc.ControlAddr
	flagProvided := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { flagProvided[f.Name] = true })
	if flagProvided["bind"] {
		bindAddr = *bind
	}
	if flagProvided["control"] {
		controlAddr = *control
	}
	if v := os.Getenv("INLET_BIND"); v != "" {
		bindAddr = v
	}
	if v, ok := os.LookupEnv("INLET_CONTROL_ADDR"); ok {
		controlAddr = v
	}
	if v := os.Getenv("INLET_PUBLIC_BASE"); v != "" {
		sc.PublicBase = v
	}
	if v := os.Getenv("INLET_OPEN_SIGNUP"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			sc.OpenSignup = b
		}
	}
	if raw, exists, err := st.GetSetting("open_signup"); err != nil {
		log.Fatalf("load runtime settings: %v", err)
	} else if exists {
		open, err := strconv.ParseBool(raw)
		if err != nil {
			log.Fatalf("invalid persisted open_signup setting: %v", err)
		}
		if open != sc.OpenSignup {
			log.Printf("using admin-managed openSignup=%t (overrides startup configuration)", open)
		}
		sc.OpenSignup = open
	}
	accessLog := false
	if v := os.Getenv("INLET_ACCESS_LOG"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			accessLog = b
		} else {
			log.Printf("invalid INLET_ACCESS_LOG %q; access logging remains disabled", v)
		}
	}
	if sc.ControlAddr != "" && !flagProvided["control"] {
		if _, set := os.LookupEnv("INLET_CONTROL_ADDR"); !set {
			log.Printf("server.json sets controlAddr=%q; using a separate control listener (set controlAddr to empty, INLET_CONTROL_ADDR=, or -control= to share the listener)", sc.ControlAddr)
		}
	}

	if raw := os.Getenv("INLET_ADMIN_TOKEN"); raw != "" {
		if err := st.SeedAdmin(raw, "environment"); err != nil {
			log.Fatalf("load INLET_ADMIN_TOKEN: %v", err)
		}
	}

	staleAfter := envDuration("INLET_STALE_AFTER", 30*24*time.Hour)
	openStaleAfter := envDuration("INLET_OPEN_STALE_AFTER", 7*24*time.Hour)
	revokedRetention := envDuration("INLET_REVOKED_RETENTION", 7*24*time.Hour)

	hub := server.NewHub(st)
	hub.SetPublicBase(sc.PublicBase)
	hub.SetPublicAddr(bindAddr)
	proxy := server.NewProxy(hub)
	signup := server.NewSignup(st, hub, sc.OpenSignup)
	admin := server.NewAdmin(st, signup)
	userTokens := server.NewUserTokens(st)

	go janitor(st, hub, staleAfter, openStaleAfter, revokedRetention)

	combinedMux := http.NewServeMux()
	combinedMux.HandleFunc("/_inlet/control", hub.HandleControl)
	combinedMux.HandleFunc("/_inlet/signup", signup.Handle)
	combinedMux.Handle("/_inlet/admin/", admin)
	combinedMux.Handle("/_inlet/tokens", userTokens)
	combinedMux.Handle("/_inlet/tokens/", userTokens)
	combinedMux.HandleFunc("/_inlet/healthz", healthHandler)
	combinedMux.HandleFunc("/", proxy.HandlePublic)

	if controlAddr != "" && controlAddr != bindAddr {
		controlMux := http.NewServeMux()
		controlMux.HandleFunc("/_inlet/control", hub.HandleControl)
		controlMux.HandleFunc("/_inlet/signup", signup.Handle)
		controlMux.Handle("/_inlet/admin/", admin)
		controlMux.Handle("/_inlet/tokens", userTokens)
		controlMux.Handle("/_inlet/tokens/", userTokens)
		publicMux := http.NewServeMux()
		publicMux.HandleFunc("/_inlet/healthz", healthHandler)
		publicMux.HandleFunc("/", proxy.HandlePublic)
		go func() {
			fmt.Printf("control on %s\n", controlAddr)
			if err := http.ListenAndServe(controlAddr, controlMux); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}()
		fmt.Printf("public on %s\n", bindAddr)
		fmt.Printf("database %s\n", dbPath)
		if err := http.ListenAndServe(bindAddr, server.WithAccessLog(publicMux, accessLog)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("http on %s (public, control, signup)\n", bindAddr)
	fmt.Printf("database %s\n", dbPath)
	if err := http.ListenAndServe(bindAddr, server.WithAccessLog(combinedMux, accessLog)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
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
	fmt.Printf("✓ Invite created. One-time code (copy it now):\n%s\n", code)
}

func cmdToken(st *store.Store, args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, tokenUsage)
		os.Exit(1)
	}
	root := flag.NewFlagSet("token", flag.ExitOnError)
	root.Usage = func() { fmt.Fprint(os.Stderr, tokenUsage) }
	if err := root.Parse(args[:1]); err != nil {
		return
	}
	if root.Arg(0) == "help" {
		fmt.Print(tokenUsage)
		return
	}
	switch root.Arg(0) {
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
		fmt.Printf("✓ Token created. Save it now; it will not be shown again.\ntoken: %s\nid: %s\n", raw, tok.TunnelID)
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
		if n == 0 {
			fmt.Println("No matching tokens found.")
			return
		}
		word := "tokens"
		if n == 1 {
			word = "token"
		}
		fmt.Printf("✓ Revoked %d %s.\n", n, word)
	default:
		fmt.Fprintf(os.Stderr, "inletd token: unknown subcommand %q\n\n", root.Arg(0))
		fmt.Fprint(os.Stderr, tokenUsage)
		os.Exit(1)
	}
}

func serverUsage(w io.Writer) {
	fmt.Fprint(w, `inletd - inlet tunnel server

Usage:
  inletd [flags]                 start the server
  inletd <command> [options]     admin commands (local DB, server may be running)

Server flags:
  -bind <addr>      public HTTP listener (default ":8080")
  -control <addr>   optional separate control listener (default: same as -bind)

Commands:
  token create [--label NAME]    issue a token; printed once
  token list                     list tokens and their state
  token revoke <prefix>          revoke active tokens by prefix
  invite create [--ttl 24h] [--label NAME]
                                 create a single-use invite code
  doctor                         check the running server health endpoint
  help                           show this help

Configuration: INLET_* env > flags > server config > defaults.
Persisted admin-managed openSignup overrides its startup setting.
  ./.env is loaded at startup without overwriting exported variables.
  INLET_BIND              public HTTP listener
  INLET_CONTROL_ADDR      optional separate control listener (default: same as INLET_BIND)
  INLET_PUBLIC_BASE       base URL advertised to clients (e.g. https://example.com)
  INLET_OPEN_SIGNUP       allow signup without an invite (default false)
  INLET_ADMIN_TOKEN       bootstrap a remote admin token (stored hashed)
  INLET_ACCESS_LOG        log public request method, tunnel, status and duration (default false)
  INLET_DB                SQLite path (default ./data/inlet.db, relative to the
                          working directory; admin commands must run in the
                          same directory)
  INLET_STALE_AFTER       unused admin/invite token TTL (default 720h)
  INLET_OPEN_STALE_AFTER  unused open-signup token TTL (default 168h)
  INLET_REVOKED_RETENTION revoked token retention (default 168h)
  INLET_INVITE_TTL        default invite lifetime (default 24h)

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
