package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "inlet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestIssueValidateRotateRevoke(t *testing.T) {
	s := openTemp(t)
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
	if len(tok.Prefix) == 0 {
		t.Fatal("empty prefix")
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
}

func TestInviteRedeemsOnce(t *testing.T) {
	s := openTemp(t)
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
}

func TestCleanupSkipsActiveAndRemovesStale(t *testing.T) {
	s := openTemp(t)
	raw, tok, err := s.Issue("old", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour).Unix()
	if _, err := s.db.Exec(`UPDATE tokens SET created_at = ?, last_used_at = ? WHERE id = ?`, old, old, tok.ID); err != nil {
		t.Fatal(err)
	}

	active := func(id string) bool { return id == tok.TunnelID }
	if n, err := s.Cleanup(time.Now(), 30*24*time.Hour, 7*24*time.Hour, 7*24*time.Hour, active); err != nil || n != 0 {
		t.Fatalf("active cleanup = %d %v", n, err)
	}
	if _, err := s.Validate(raw); err != nil {
		t.Fatal("active token was removed")
	}

	if n, err := s.Cleanup(time.Now(), 30*24*time.Hour, 7*24*time.Hour, 7*24*time.Hour, nil); err != nil || n != 1 {
		t.Fatalf("stale cleanup = %d %v", n, err)
	}
	if _, err := s.Validate(raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("stale token still valid")
	}
}

func TestCleanupRemovesRevokedAfterRetention(t *testing.T) {
	s := openTemp(t)
	raw, tok, err := s.Issue("revoked", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	// revoked 1 day ago: within 7d retention -> kept
	revokedAt := time.Now().Add(-1 * 24 * time.Hour).Unix()
	if _, err := s.db.Exec(`UPDATE tokens SET revoked_at = ? WHERE id = ?`, revokedAt, tok.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Cleanup(time.Now(), 30*24*time.Hour, 7*24*time.Hour, 7*24*time.Hour, nil); err != nil || n != 0 {
		t.Fatalf("within retention cleanup = %d %v", n, err)
	}
	if _, err := s.Validate(raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("revoked token should be invalid")
	}
	// revoked 8 days ago: past 7d retention -> removed
	revokedAt = time.Now().Add(-8 * 24 * time.Hour).Unix()
	if _, err := s.db.Exec(`UPDATE tokens SET revoked_at = ? WHERE id = ?`, revokedAt, tok.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Cleanup(time.Now(), 30*24*time.Hour, 7*24*time.Hour, 7*24*time.Hour, nil); err != nil || n != 1 {
		t.Fatalf("past retention cleanup = %d %v", n, err)
	}
}

func TestActiveForIPIgnoresRevoked(t *testing.T) {
	s := openTemp(t)
	if _, _, err := s.Issue("open", "open", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	raw2, tok2, err := s.Issue("open2", "open", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(tok2.Prefix); err != nil {
		t.Fatal(err)
	}
	_ = raw2
	n, err := s.ActiveForIP("1.2.3.4")
	if err != nil || n != 1 {
		t.Fatalf("active for ip = %d %v, want 1", n, err)
	}
}
func TestCleanupUsesShorterWindowForOpenTokens(t *testing.T) {
	s := openTemp(t)
	raw, tok, err := s.Issue("open", "open", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour).Unix()
	if _, err := s.db.Exec(`UPDATE tokens SET last_used_at = ? WHERE id = ?`, tenDaysAgo, tok.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Cleanup(time.Now(), 30*24*time.Hour, 7*24*time.Hour, 7*24*time.Hour, nil); err != nil || n != 1 {
		t.Fatalf("open cleanup = %d %v", n, err)
	}
	if _, err := s.Validate(raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("stale open token still valid")
	}
}
