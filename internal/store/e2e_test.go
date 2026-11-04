package store

import (
	"errors"
	"os"
	"testing"
	"time"
)

func cleanDB(t *testing.T, s *Store) {
	t.Helper()
	// Best effort cleanup
	s.db.Exec(s.dialect.RewriteQuery("DELETE FROM invites"))
	s.db.Exec(s.dialect.RewriteQuery("DELETE FROM tokens"))
	s.db.Exec(s.dialect.RewriteQuery("DELETE FROM accounts"))
	s.db.Exec(s.dialect.RewriteQuery("DELETE FROM settings"))
}

func openFromDSN(t *testing.T, dsn string) *Store {
	t.Helper()
	if dsn == "" {
		t.Skip("no dsn provided")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() {
		cleanDB(t, s)
		s.Close()
	})
	return s
}

func TestE2E_Basic_AllBackends(t *testing.T) {
	backends := []struct {
		name string
		dsn  string
	}{
		{"sqlite", os.Getenv("INLET_TEST_DSN_SQLITE")},
		{"postgres", os.Getenv("INLET_TEST_DSN_POSTGRES")},
		{"mysql", os.Getenv("INLET_TEST_DSN_MYSQL")},
	}
	for _, b := range backends {
		if b.dsn == "" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			s := openFromDSN(t, b.dsn)
			// Issue, validate, rotate, revoke
			raw, tok, err := s.Issue("laptop", "admin", "")
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Validate(raw)
			if err != nil || got.ID != tok.ID {
				t.Fatalf("validate: %v", err)
			}
			if _, err := s.Validate("inlet_wrong"); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("wrong token error = %v", err)
			}
			rotated, err := s.RotateTunnel(tok.ID)
			if err != nil || rotated == tok.TunnelID {
				t.Fatalf("rotate: %q %v", rotated, err)
			}
			if n, err := s.Revoke(tok.Prefix); err != nil || n != 1 {
				t.Fatalf("revoke: %d %v", n, err)
			}
			if _, err := s.Validate(raw); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("revoked token error = %v", err)
			}
		})
	}
}

func TestE2E_Invites_AllBackends(t *testing.T) {
	backends := []struct {
		name string
		dsn  string
	}{
		{"sqlite", os.Getenv("INLET_TEST_DSN_SQLITE")},
		{"postgres", os.Getenv("INLET_TEST_DSN_POSTGRES")},
		{"mysql", os.Getenv("INLET_TEST_DSN_MYSQL")},
	}
	for _, b := range backends {
		if b.dsn == "" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			s := openFromDSN(t, b.dsn)
			code, err := s.CreateInvite("friend", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			raw, tok, err := s.RedeemInvite(code, "", "1.2.3.4")
			if err != nil {
				t.Fatal(err)
			}
			if tok.Label != "friend" || tok.SignupIP != "1.2.3.4" {
				t.Fatalf("token = %+v", tok)
			}
			if _, err := s.Validate(raw); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.RedeemInvite(code, "", "1.2.3.4"); !errors.Is(err, ErrInvalidCode) {
				t.Fatalf("second redeem error = %v", err)
			}
		})
	}
}

func TestE2E_Settings_AllBackends(t *testing.T) {
	backends := []struct {
		name string
		dsn  string
	}{
		{"sqlite", os.Getenv("INLET_TEST_DSN_SQLITE")},
		{"postgres", os.Getenv("INLET_TEST_DSN_POSTGRES")},
		{"mysql", os.Getenv("INLET_TEST_DSN_MYSQL")},
	}
	for _, b := range backends {
		if b.dsn == "" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			s := openFromDSN(t, b.dsn)
			if _, ok, err := s.GetSetting("missing"); err != nil || ok {
				t.Fatalf("missing = %v %v", ok, err)
			}
			if err := s.SetSetting("open_signup", "true"); err != nil {
				t.Fatal(err)
			}
			v, ok, err := s.GetSetting("open_signup")
			if err != nil || !ok || v != "true" {
				t.Fatalf("get = %q %v %v", v, ok, err)
			}
			if err := s.SetSetting("open_signup", "false"); err != nil {
				t.Fatal(err)
			}
			v, ok, err = s.GetSetting("open_signup")
			if err != nil || !ok || v != "false" {
				t.Fatalf("get2 = %q %v %v", v, ok, err)
			}
		})
	}
}

func TestE2E_ListAndClientOps_AllBackends(t *testing.T) {
	backends := []struct {
		name string
		dsn  string
	}{
		{"sqlite", os.Getenv("INLET_TEST_DSN_SQLITE")},
		{"postgres", os.Getenv("INLET_TEST_DSN_POSTGRES")},
		{"mysql", os.Getenv("INLET_TEST_DSN_MYSQL")},
	}
	for _, b := range backends {
		if b.dsn == "" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			s := openFromDSN(t, b.dsn)
			raw1, tok1, err := s.Issue("a", "open", "1.1.1.1")
			if err != nil {
				t.Fatal(err)
			}
			raw2, tok2, err := s.Issue("b", "open", "1.1.1.1")
			if err != nil {
				t.Fatal(err)
			}
			_ = raw1
			_ = raw2
			list, err := s.ListForClient("", "1.1.1.1", tok1.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) < 2 {
				t.Fatalf("expected >=2, got %d", len(list))
			}
			if n, err := s.RevokeForClient(tok2.Prefix, "", "1.1.1.1", tok1.ID); err != nil || n != 1 {
				t.Fatalf("revoke client: %d %v", n, err)
			}
			if n, err := s.UpdateClientLabel(tok1.Prefix, "", "1.1.1.1", tok1.ID, "renamed"); err != nil || n != 1 {
				t.Fatalf("update label: %d %v", n, err)
			}
		})
	}
}

func TestE2E_Cleanup_AllBackends(t *testing.T) {
	backends := []struct {
		name string
		dsn  string
	}{
		{"sqlite", os.Getenv("INLET_TEST_DSN_SQLITE")},
		{"postgres", os.Getenv("INLET_TEST_DSN_POSTGRES")},
		{"mysql", os.Getenv("INLET_TEST_DSN_MYSQL")},
	}
	for _, b := range backends {
		if b.dsn == "" {
			continue
		}
		t.Run(b.name, func(t *testing.T) {
			s := openFromDSN(t, b.dsn)
			raw, tok, err := s.Issue("old", "admin", "")
			if err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-40 * 24 * time.Hour).Unix()
			if _, err := s.db.Exec(s.dialect.RewriteQuery("UPDATE tokens SET created_at = ?, last_used_at = ? WHERE id = ?"), old, old, tok.ID); err != nil {
				t.Fatal(err)
			}
			if n, err := s.Cleanup(time.Now(), 30*24*time.Hour, 7*24*time.Hour, 7*24*time.Hour, nil); err != nil || n < 1 {
				t.Fatalf("cleanup: %d %v", n, err)
			}
			if _, err := s.Validate(raw); !errors.Is(err, ErrInvalidToken) {
				t.Fatal("stale token still valid")
			}
		})
	}
}
