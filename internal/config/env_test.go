package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	body := "# comment\n\nINLET_TOKENS=a,b\nexport INLET_BIND=:9000\nQUOTED=\"x y\"\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("INLET_BIND", ":1111") // real env wins
	os.Unsetenv("INLET_TOKENS")
	os.Unsetenv("QUOTED")
	t.Cleanup(func() {
		os.Unsetenv("INLET_TOKENS")
		os.Unsetenv("QUOTED")
	})

	if err := LoadEnvFile(p); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("INLET_TOKENS"); got != "a,b" {
		t.Fatalf("INLET_TOKENS = %q", got)
	}
	if got := os.Getenv("INLET_BIND"); got != ":1111" {
		t.Fatalf("INLET_BIND overridden: %q", got)
	}
	if got := os.Getenv("QUOTED"); got != "x y" {
		t.Fatalf("QUOTED = %q", got)
	}

	if err := LoadEnvFile(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}
