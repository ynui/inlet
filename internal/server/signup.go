package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ynui/inlet/internal/labels"
	"github.com/ynui/inlet/internal/store"
)

type Signup struct {
	store    *store.Store
	hub      *Hub
	open     atomic.Bool
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
	maxIP    int
}

func NewSignup(s *store.Store, h *Hub, open bool) *Signup {
	signup := &Signup{
		store:    s,
		hub:      h,
		attempts: map[string][]time.Time{},
		limit:    5,
		window:   time.Hour,
		maxIP:    3,
	}
	signup.open.Store(open)
	return signup
}

func (s *Signup) Open() bool { return s.open.Load() }

func (s *Signup) SetOpen(open bool) { s.open.Store(open) }

func (s *Signup) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]bool{"open": s.Open()})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ip := clientIP(r)
	if !s.allow(ip) {
		log.Printf("signup rejected reason=attempt_rate_limit ip=%q", ip)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": fmt.Sprintf("signup attempt limit reached: maximum %d attempts per IP every %s; wait or ask the server administrator for an invite", s.limit, s.window)})
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
	label, labelErr := labels.Normalize(req.Label)
	if labelErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": labelErr.Error()})
		return
	}

	var (
		raw string
		tok *store.Token
		err error
	)
	if req.Invite != "" {
		raw, tok, err = s.store.RedeemInvite(req.Invite, label, ip)
		if errors.Is(err, store.ErrInvalidCode) {
			log.Printf("signup rejected source=invite reason=invalid_or_expired ip=%q", ip)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or expired invite"})
			return
		}
	} else if !s.Open() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invite required; ask the server administrator for a one-time code"})
		return
	} else {
		active, aerr := s.store.ActiveForIP(ip)
		if aerr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if active >= s.maxIP {
			log.Printf("signup rejected source=open reason=ip_token_limit ip=%q", ip)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": fmt.Sprintf("open signup limit reached: maximum %d active tokens per signup IP; use 'inlet token list' and 'inlet token revoke <prefix>' or claim an invite", s.maxIP)})
			return
		}
		raw, tok, err = s.store.Issue(label, "open", ip)
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
