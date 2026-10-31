package main

import (
	"strings"
	"testing"
)

func TestHelpIsDetailed(t *testing.T) {
	var b strings.Builder
	serverUsage(&b)
	for _, want := range []string{"token create", "token list", "token revoke", "invite create", "-bind", "-control", "INLET_OPEN_SIGNUP", "INLET_DB"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("server help missing %q:\n%s", want, b.String())
		}
	}
	for name, text := range map[string]string{"token": tokenUsage, "invite": inviteUsage} {
		if !strings.Contains(text, "Usage:") || !strings.Contains(text, "inletd "+name) {
			t.Fatalf("%s help is not detailed:\n%s", name, text)
		}
	}
	for _, want := range []string{"create", "list", "revoke"} {
		if !strings.Contains(tokenUsage, want) {
			t.Fatalf("token help missing %q", want)
		}
	}
	for _, want := range []string{"--ttl", "--label"} {
		if !strings.Contains(inviteUsage, want) {
			t.Fatalf("invite help missing %q", want)
		}
	}
}
