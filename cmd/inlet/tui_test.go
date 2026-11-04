package main

import (
	"bufio"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReadInput(t *testing.T) {
	var keys string
	readInput(bufio.NewReader(strings.NewReader("cxz")), func(b byte) { keys += string(b) })
	if keys != "cxz" {
		t.Fatalf("keys = %q", keys)
	}
}

func headerOutput(t *testing.T, notice string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printConnectionHeader("https://x/abc", 3000, 4040, time.Second, "online", "", notice)
	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestHeaderShowsNotice(t *testing.T) {
	out := headerOutput(t, "✓ url copied")
	if !strings.Contains(out, "\033[2;33H\033[90m✓ url copied\033[0m") {
		t.Fatalf("notice missing: %q", out)
	}
}

func TestHeaderIdleHasNoSlot(t *testing.T) {
	if out := headerOutput(t, ""); strings.Contains(out, "\033[2;33H") {
		t.Fatalf("idle title line has extra text: %q", out)
	}
}
