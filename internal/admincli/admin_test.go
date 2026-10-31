package admincli

import "testing"

func TestNormalizeServer(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"https://example.com", "wss://example.com", false},
		{"http://127.0.0.1:8080", "ws://127.0.0.1:8080", false},
		{"wss://example.com", "wss://example.com", false},
		{"ftp://example.com", "", true},
		{"example.com", "", true},
	}
	for _, c := range cases {
		got, err := normalizeServer(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeServer(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("normalizeServer(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestAdminEndpoint(t *testing.T) {
	cases := []struct {
		server, path, want string
		wantErr            bool
	}{
		{"wss://example.com", "/_inlet/admin/tokens?limit=5", "https://example.com/_inlet/admin/tokens?limit=5", false},
		{"ws://127.0.0.1:8080", "/_inlet/admin/config", "http://127.0.0.1:8080/_inlet/admin/config", false},
		{"wss://example.com/base#frag", "/x", "https://example.com/x", false},
		{"ftp://example.com", "/x", "", true},
	}
	for _, c := range cases {
		got, err := adminEndpoint(c.server, c.path)
		if c.wantErr {
			if err == nil {
				t.Errorf("adminEndpoint(%q, %q) = %q, want error", c.server, c.path, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("adminEndpoint(%q, %q) = %q, %v; want %q", c.server, c.path, got, err, c.want)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1":     true,
		"localhost":     true,
		"foo.localhost": true,
		"10.0.0.1":      false,
		"example.com":   false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}
