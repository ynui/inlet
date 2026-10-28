package main

import (
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

	out := requestView(evs, 11, 22, 33, 44, false)
	if got := strings.Count(out, "\n"); got != paneLines {
		t.Fatalf("pane lines = %d, want %d", got, paneLines)
	}
	if !strings.Contains(out, "\033[8A") {
		t.Fatal("missing cursor-up for fixed pane")
	}
	if !strings.Contains(out, "2xx 11") || !strings.Contains(out, "3xx 22") ||
		!strings.Contains(out, "4xx 33") || !strings.Contains(out, "5xx 44") {
		t.Fatalf("stats missing: %q", out)
	}
	// events are newest-first: last pane row must be the newest one
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[2], "/oldest") || !strings.Contains(lines[4], "/newest") {
		t.Fatalf("wrong order: %q", lines[:5])
	}

	// fewer events than rows: pane stays full
	if got := strings.Count(requestView(nil, 0, 0, 0, 0, true), "\n"); got != paneLines {
		t.Fatalf("empty pane lines = %d, want %d", got, paneLines)
	}
	// old requests fall off the pane
	many := make([]client.Event, 50)
	if got := strings.Count(requestView(many, 0, 0, 0, 0, true), "\n"); got != paneLines {
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
