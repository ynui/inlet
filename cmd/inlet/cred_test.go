package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var inletBin string

func TestMain(m *testing.M) {
	d, err := os.MkdirTemp("", "inlet-bin-*")
	if err != nil {
		panic(err)
	}
	inletBin = filepath.Join(d, "inlet")
	cmd := exec.Command("go", "build", "-o", inletBin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(d)
		panic(string(out) + "\n" + err.Error())
	}
	code := m.Run()
	os.RemoveAll(d)
	os.Exit(code)
}

func runInlet(t *testing.T, home string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(inletBin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run inlet %v: %v", args, err)
		}
	}
	return string(out), code
}

func writeConfig(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "inlet")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAdminDoesNotReadClientConfig(t *testing.T) {
	home := t.TempDir()
	// Only a client config exists; admin.json is absent.
	writeConfig(t, home, "config.json", `{"server":"ws://example.com","token":"client-secret"}`)

	out, _ := runInlet(t, home, "admin", "config", "show")
	if !strings.Contains(out, "no admin token configured") {
		t.Fatalf("expected admin to ignore config.json and report missing admin token; got:\n%s", out)
	}
	if strings.Contains(out, "client-secret") {
		t.Fatalf("admin leaked client token from config.json:\n%s", out)
	}
}

func TestClientDoesNotReadAdminConfig(t *testing.T) {
	home := t.TempDir()
	// Only an admin config exists; config.json is absent.
	writeConfig(t, home, "admin.json", `{"server":"ws://example.com","token":"admin-secret"}`)

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"token", "list"}, "no token configured"},
		{[]string{"http", "3000"}, "no token configured"},
		{[]string{"config", "show"}, "not configured"},
	}
	for _, c := range cases {
		out, _ := runInlet(t, home, c.args...)
		if !strings.Contains(out, c.want) {
			t.Errorf("client command %v should ignore admin.json and report missing client token; got:\n%s", c.args, out)
		}
		if strings.Contains(out, "admin-secret") {
			t.Errorf("client command %v leaked admin token from admin.json:\n%s", c.args, out)
		}
	}
}
