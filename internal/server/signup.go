package server

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ynui/inlet/internal/store"
)

type Signup struct {
	store    *store.Store
	hub      *Hub
	open     bool
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
	maxIP    int
}

func NewSignup(s *store.Store, h *Hub, open bool) *Signup {
	return &Signup{
		store:    s,
		hub:      h,
		open:     open,
		attempts: map[string][]time.Time{},
		limit:    5,
		window:   time.Hour,
		maxIP:    3,
	}
}

func (s *Signup) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]bool{"open": s.open})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ip := clientIP(r)
	if !s.allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many signups"})
		return
	}

	var req struct {
		Invite string `json:"invite"`
		Label  string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var (
		raw string
		tok *store.Token
		err error
	)
	if req.Invite != "" {
		raw, tok, err = s.store.RedeemInvite(req.Invite, req.Label, ip)
		if errors.Is(err, store.ErrInvalidCode) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or expired invite"})
			return
		}
	} else if !s.open {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invite required"})
		return
	} else {
		active, aerr := s.store.ActiveForIP(ip)
		if aerr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if active >= s.maxIP {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many active tokens"})
			return
		}
		raw, tok, err = s.store.Issue(req.Label, "open", ip)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("signup source=%s ip=%s label=%q", tok.Source, ip, tok.Label)
	writeJSON(w, http.StatusOK, map[string]string{
		"token":     raw,
		"id":        tok.TunnelID,
		"publicUrl": s.hub.PublicURL(tok.TunnelID, r),
	})
}

func (s *Signup) allow(ip string) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.attempts) > 10000 {
		s.attempts = map[string][]time.Time{}
	}
	cutoff := now.Add(-s.window)
	hits := s.attempts[ip]
	kept := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= s.limit {
		s.attempts[ip] = kept
		return false
	}
	s.attempts[ip] = append(kept, now)
	return true
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
