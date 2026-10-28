package client

import (
	"strings"
	"testing"
)

func TestBodyPreview(t *testing.T) {
	if got := bodyPreview([]byte("hello")); got != "hello" {
		t.Fatalf("short body = %q", got)
	}
	big := strings.Repeat("x", 5000)
	got := bodyPreview([]byte(big))
	if len(got) != 4096+len("\n... (truncated)") {
		t.Fatalf("long body length = %d", len(got))
	}
	if !strings.HasSuffix(got, "\n... (truncated)") {
		t.Fatal("missing truncation marker")
	}
}
