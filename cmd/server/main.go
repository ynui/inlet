package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/ynui/inlet/internal/auth"
	"github.com/ynui/inlet/internal/config"
	"github.com/ynui/inlet/internal/server"
)

type SConfig struct {
	Bind        string   `json:"bind"`
	ControlAddr string   `json:"controlAddr"`
	Tokens      []string `json:"tokens"`
	PublicBase  string   `json:"publicBase"`
}

func main() {
	// Optional .env in the working directory; real environment wins.
	_ = config.LoadEnvFile(".env")

	var (
		bind    = flag.String("bind", ":8080", "public http bind")
		control = flag.String("control", ":8081", "control ws bind")
	)
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
	if sc.Bind != "" {
		*bind = sc.Bind
	}
	if sc.ControlAddr != "" {
		*control = sc.ControlAddr
	}
	if len(sc.Tokens) == 0 {
		fmt.Fprintln(os.Stderr, "no tokens - set INLET_TOKENS in .env or ~/.config/inlet/server.json")
		os.Exit(1)
	}

	hub := server.NewHub(auth.New(sc.Tokens), server.NewStore())
	hub.SetPublicBase(sc.PublicBase)
	proxy := server.NewProxy(hub)

	controlMux := http.NewServeMux()
	controlMux.HandleFunc("/_inlet/control", hub.HandleControl)
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
	if err := http.ListenAndServe(*bind, publicMux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
