package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/ynui/inlet/internal/labels"
	"github.com/ynui/inlet/internal/store"
)

type UserTokens struct {
	store *store.Store
}

func NewUserTokens(s *store.Store) *UserTokens { return &UserTokens{store: s} }

func (u *UserTokens) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !secureTokenRequest(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token management requires HTTPS (loopback is allowed)"})
		return
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "client token required"})
		return
	}
	tok, err := u.store.Validate(parts[1])
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked token"})
		return
	}
	if err := u.store.Touch(tok.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	switch {
	case r.URL.Path == "/_inlet/tokens" && r.Method == http.MethodGet:
		u.list(w, tok.SignupIP, tok.ID)
	case r.URL.Path == "/_inlet/tokens/revoke" && r.Method == http.MethodPost:
		u.revoke(w, r, tok.SignupIP, tok.ID)
	case r.URL.Path == "/_inlet/tokens/label" && r.Method == http.MethodPost:
		u.updateLabel(w, r, tok.SignupIP, tok.ID)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (u *UserTokens) updateLabel(w http.ResponseWriter, r *http.Request, ip, tokenID string) {
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
	n, err := u.store.UpdateClientLabel(req.Prefix, ip, tokenID, label)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no matching token found"})
		return
	}
	log.Printf("client action=update_open_token_label prefix=%s", req.Prefix)
	writeJSON(w, http.StatusOK, map[string]string{"prefix": req.Prefix, "label": label})
}

func (u *UserTokens) list(w http.ResponseWriter, ip, tokenID string) {
	tokens, err := u.store.ListForClient(ip, tokenID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(tokens))
	for _, tok := range tokens {
		out = append(out, map[string]any{
			"prefix":     tok.Prefix,
			"label":      tok.Label,
			"tunnelID":   tok.TunnelID,
			"createdAt":  tok.CreatedAt,
			"lastUsedAt": tok.LastUsedAt,
			"revokedAt":  tok.RevokedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (u *UserTokens) revoke(w http.ResponseWriter, r *http.Request, ip, tokenID string) {
	var req struct {
		Prefix string `json:"prefix"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(req.Prefix) != 12 || strings.Trim(req.Prefix, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prefix must be the 12-character token prefix shown by token list"})
		return
	}
	n, err := u.store.RevokeForClient(req.Prefix, ip, tokenID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no matching token found"})
		return
	}
	log.Printf("client action=revoke_open_token prefix=%s", req.Prefix)
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}
