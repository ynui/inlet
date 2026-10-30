package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/labels"
	"github.com/ynui/inlet/internal/store"
)

type Admin struct {
	store  *store.Store
	signup *Signup
}

func NewAdmin(s *store.Store, signup *Signup) *Admin {
	return &Admin{store: s, signup: signup}
}

func (a *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !secureTokenRequest(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "admin API requires HTTPS (loopback is allowed)"})
		return
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "admin token required"})
		return
	}
	tok, err := a.store.Validate(parts[1])
	if err != nil || tok.Source != "admin" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin token"})
		return
	}
	if err := a.store.Touch(tok.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	switch {
	case r.URL.Path == "/_inlet/admin/config":
		a.handleConfig(w, r)
	case r.URL.Path == "/_inlet/admin/invites":
		a.handleInvites(w, r)
	case r.URL.Path == "/_inlet/admin/tokens":
		a.handleTokens(w, r)
	case r.URL.Path == "/_inlet/admin/tokens/revoke":
		a.handleTokenRevoke(w, r, tok)
	case r.URL.Path == "/_inlet/admin/tokens/label":
		a.handleTokenLabel(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "admin endpoint not found"})
	}
}

func (a *Admin) handleTokenLabel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Prefix string `json:"prefix"`
		Label  string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(req.Prefix) != 12 || strings.Trim(req.Prefix, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prefix must be the 12-character token prefix shown by token list"})
		return
	}
	label, err := labels.Normalize(req.Label)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	n, err := a.store.UpdateLabel(req.Prefix, label)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no token found with that prefix"})
		return
	}
	log.Printf("admin action=update_token_label prefix=%s", req.Prefix)
	writeJSON(w, http.StatusOK, map[string]string{"prefix": req.Prefix, "label": label})
}

func (a *Admin) handleTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, offset, err := tokenPage(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		tokens, total, err := a.store.ListPage(limit, offset)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(tokens))
		for _, tok := range tokens {
			out = append(out, map[string]any{
				"prefix":     tok.Prefix,
				"source":     tok.Source,
				"tunnelID":   tok.TunnelID,
				"label":      tok.Label,
				"createdAt":  tok.CreatedAt,
				"lastUsedAt": tok.LastUsedAt,
				"revokedAt":  tok.RevokedAt,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tokens": out,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	case http.MethodPost:
		var req struct {
			Label string `json:"label"`
			Admin bool   `json:"admin"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		label, err := labels.Normalize(req.Label)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		source := "managed"
		if req.Admin {
			source = "admin"
		}
		raw, tok, err := a.store.Issue(label, source, "")
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		log.Printf("admin action=create_token source=%s prefix=%s", source, tok.Prefix)
		writeJSON(w, http.StatusCreated, map[string]string{
			"token":    raw,
			"prefix":   tok.Prefix,
			"tunnelID": tok.TunnelID,
			"source":   tok.Source,
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func tokenPage(r *http.Request) (limit, offset int, err error) {
	limit = 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, fmt.Errorf("limit must be between 1 and 200")
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("offset must be zero or greater")
		}
	}
	return limit, offset, nil
}

func (a *Admin) handleTokenRevoke(w http.ResponseWriter, r *http.Request, current *store.Token) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Prefixes []string `json:"prefixes"`
		Prefix   string   `json:"prefix"` // compatibility with earlier inlet-admin clients
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(req.Prefixes) == 0 && req.Prefix != "" {
		req.Prefixes = []string{req.Prefix}
	}
	if len(req.Prefixes) == 0 || len(req.Prefixes) > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provide between 1 and 100 token prefixes"})
		return
	}
	seen := make(map[string]bool, len(req.Prefixes))
	prefixes := make([]string, 0, len(req.Prefixes))
	for _, prefix := range req.Prefixes {
		if len(prefix) != 12 || strings.Trim(prefix, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "each prefix must be the 12-character token prefix shown by token list"})
			return
		}
		if prefix == current.Prefix {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot revoke the admin token used for this request; authenticate with a different admin token first"})
			return
		}
		if !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	counts, err := a.store.RevokeMany(prefixes)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	revoked := make(map[string]int64, len(prefixes))
	var total int64
	for i, prefix := range prefixes {
		revoked[prefix] = counts[i]
		total += counts[i]
	}
	log.Printf("admin action=revoke_tokens count=%d", total)
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}

func secureTokenRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	if strings.EqualFold(forwardedProto, "https") {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *Admin) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]bool{"openSignup": a.signup.Open()})
	case http.MethodPatch:
		var req struct {
			OpenSignup *bool `json:"openSignup"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.OpenSignup == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "openSignup boolean is required"})
			return
		}
		open := *req.OpenSignup
		if err := a.store.SetSetting("open_signup", map[bool]string{true: "true", false: "false"}[open]); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		a.signup.SetOpen(open)
		log.Printf("admin action=set_open_signup enabled=%t", open)
		writeJSON(w, http.StatusOK, map[string]bool{"openSignup": open})
	default:
		w.Header().Set("Allow", "GET, PATCH")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (a *Admin) handleInvites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Label string `json:"label"`
		TTL   string `json:"ttl"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	label, err := labels.Normalize(req.Label)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ttl := 24 * time.Hour
	if req.TTL != "" {
		var err error
		ttl, err = time.ParseDuration(req.TTL)
		if err != nil || ttl <= 0 || ttl > 365*24*time.Hour {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ttl must be between 1ns and 8760h"})
			return
		}
	}
	code, err := a.store.CreateInvite(label, ttl)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("admin action=create_invite ttl=%s", ttl)
	writeJSON(w, http.StatusCreated, map[string]string{"code": code, "ttl": ttl.String()})
}
