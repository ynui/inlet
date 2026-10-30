package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ynui/inlet/internal/client"
)

func TestParseHTTPArgs(t *testing.T) {
	bottom, inspect, isNew := parseHTTPArgs([]string{"3000", "--new-url"})
	if bottom != 3000 || !isNew || inspect != 0 {
		t.Fatalf("flags after port: got port=%d inspect=%d new=%v", bottom, inspect, isNew)
	}
	bottom, inspect, isNew = parseHTTPArgs([]string{"--new-url", "--inspect", "4041", "3000"})
	if bottom != 3000 || !isNew || inspect != 4041 {
		t.Fatalf("flags before port: got port=%d inspect=%d new=%v", bottom, inspect, isNew)
	}
	bottom, inspect, isNew = parseHTTPArgs([]string{"3000"})
	if bottom != 3000 || isNew || inspect != 0 {
		t.Fatalf("no flags: got port=%d inspect=%d new=%v", bottom, inspect, isNew)
	}
}

func TestRequestView(t *testing.T) {
	evs := []client.Event{{Path: "/newest", Status: 200}, {Path: "/older", Status: 404}, {Path: "/oldest", Status: 500}}

	out := requestView(evs, 11, 22, 33, 44, 110, false)
	if got := strings.Count(out, "\n"); got != paneLines {
		t.Fatalf("pane lines = %d, want %d", got, paneLines)
	}
	if !strings.Contains(out, "\033[7;1H") {
		t.Fatal("missing cursor-up for fixed pane")
	}
	if !strings.Contains(out, "2xx 11") || !strings.Contains(out, "3xx 22") ||
		!strings.Contains(out, "4xx 33") || !strings.Contains(out, "5xx 44") {
		t.Fatalf("stats missing: %q", out)
	}
	// events are newest-first: last pane row must be the newest one
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[2], "/newest") || !strings.Contains(lines[4], "/oldest") {
		t.Fatalf("wrong order: %q", lines[:5])
	}

	// fewer events than rows: pane stays full
	if got := strings.Count(requestView(nil, 0, 0, 0, 0, 0, true), "\n"); got != paneLines {
		t.Fatalf("empty pane lines = %d, want %d", got, paneLines)
	}
	// old requests fall off the pane
	many := make([]client.Event, 50)
	if got := strings.Count(requestView(many, 0, 0, 0, 0, 0, true), "\n"); got != paneLines {
		t.Fatalf("full pane lines = %d, want %d", got, paneLines)
	}
	// long path is truncated to the column width
	if !strings.Contains(eventLine(client.Event{Path: strings.Repeat("x", 100)}), "xxx...") {
		t.Fatal("path not truncated")
	}
	if humanSize(1536) != "1.5KB" || humanSize(999) != "999B" || humanSize(2<<20) != "2.0MB" {
		t.Fatalf("humanSize wrong: %s %s %s", humanSize(1536), humanSize(999), humanSize(2<<20))
	}
}

func TestPrintHeaderRedrawsInPlace(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printHeader("(connecting)", 3000, 4040, 0)
	printHeader("http://localhost:8080/abc", 3000, 4040, 0)
	w.Close()
	os.Stdout = old

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	// Newlines would make the second header stack below the first one.
	if strings.Contains(string(out), "\n") {
		t.Fatalf("header advances the cursor: %q", out)
	}
	for _, want := range []string{"\033[4;1H", "(connecting)", "http://localhost:8080/abc", "\033[7;1H"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestHelpIsDetailed(t *testing.T) {
	var b strings.Builder
	usage(&b)
	for _, want := range []string{"signup", "auth", "http", "version", "help", "config.json"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("main help missing %q:\n%s", want, b.String())
		}
	}
	for name, text := range map[string]string{"http": httpUsage, "signup": signupUsage, "auth": authUsage} {
		if !strings.Contains(text, "Usage:") || !strings.Contains(text, "inlet "+name) {
			t.Fatalf("%s help is not detailed:\n%s", name, text)
		}
	}
	for _, want := range []string{"<port>", "--new-url", "--inspect"} {
		if !strings.Contains(httpUsage, want) {
			t.Fatalf("http help missing %q", want)
		}
	}
	for _, want := range []string{"--server", "--invite", "--label"} {
		if !strings.Contains(signupUsage, want) {
			t.Fatalf("signup help missing %q", want)
		}
	}
}
