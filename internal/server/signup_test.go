package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ynui/inlet/internal/store"
)

func signupFixture(t *testing.T, open bool) (*Signup, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "inlet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub(st)
	hub.SetPublicBase("https://tunnel.test")
	return NewSignup(st, hub, open), st
}

func postSignup(t *testing.T, s *Signup, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/_inlet/signup", bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.9:1234"
	w := httptest.NewRecorder()
	s.Handle(w, req)
	return w
}

func TestSignupInviteOnly(t *testing.T) {
	s, st := signupFixture(t, false)
	if w := postSignup(t, s, map[string]string{}); w.Code != http.StatusForbidden {
		t.Fatalf("open signup disabled: %d", w.Code)
	}
	code, err := st.CreateInvite("friend", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w := postSignup(t, s, map[string]string{"invite": code})
	if w.Code != http.StatusOK {
		t.Fatalf("invite signup: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Token     string `json:"token"`
		PublicURL string `json:"publicUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" || out.PublicURL == "" {
		t.Fatalf("response = %+v", out)
	}
	if _, err := st.Validate(out.Token); err != nil {
		t.Fatal(err)
	}
	if w := postSignup(t, s, map[string]string{"invite": code}); w.Code != http.StatusForbidden {
		t.Fatalf("reused invite: %d", w.Code)
	}
}

func TestSignupOpen(t *testing.T) {
	s, st := signupFixture(t, true)
	w := postSignup(t, s, map[string]string{"label": "guest"})
	if w.Code != http.StatusOK {
		t.Fatalf("open signup: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	tok, err := st.Validate(out.Token)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Source != "open" || tok.SignupIP != "203.0.113.9" {
		t.Fatalf("token = %+v", tok)
	}
}
