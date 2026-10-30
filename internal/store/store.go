package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrInvalidCode  = errors.New("invalid or expired invite")
)

type Token struct {
	ID         string
	Prefix     string
	Label      string
	TunnelID   string
	Source     string
	SignupIP   string
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS tokens (
  id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  tunnel_id TEXT NOT NULL UNIQUE,
  source TEXT NOT NULL DEFAULT 'admin',
  signup_ip TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  revoked_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tokens_last_used ON tokens(last_used_at);
CREATE INDEX IF NOT EXISTS tokens_ip ON tokens(signup_ip);

CREATE TABLE IF NOT EXISTS invites (
  id TEXT PRIMARY KEY,
  code_hash BLOB NOT NULL UNIQUE,
  label TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at INTEGER NOT NULL DEFAULT 0,
  used_token_id TEXT NOT NULL DEFAULT ''
);
`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&n)
	return n, err
}

func (s *Store) Issue(label, source, signupIP string) (string, *Token, error) {
	raw, hash := newSecret()
	tok, err := s.insertToken(s.db, raw, hash, label, source, signupIP, "")
	if err != nil {
		return "", nil, err
	}
	return raw, tok, nil
}

// Seed imports an existing raw token, preserving its tunnel ID when possible.
func (s *Store) Seed(raw, label, tunnelID string) error {
	hash := sha256.Sum256([]byte(raw))
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM tokens WHERE token_hash = ?`, hash[:]).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.insertToken(s.db, raw, hash[:], label, "bootstrap", "", tunnelID)
	return err
}

func (s *Store) Validate(raw string) (*Token, error) {
	hash := sha256.Sum256([]byte(raw))
	row := s.db.QueryRow(`
SELECT id, token_prefix, label, tunnel_id, source, signup_ip,
       created_at, last_used_at, revoked_at
FROM tokens WHERE token_hash = ?`, hash[:])
	tok, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !tok.RevokedAt.IsZero() {
		return nil, ErrInvalidToken
	}
	return tok, nil
}

func (s *Store) Touch(id string) error {
	_, err := s.db.Exec(`UPDATE tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

func (s *Store) RotateTunnel(id string) (string, error) {
	tid := randID(6)
	_, err := s.db.Exec(`UPDATE tokens SET tunnel_id = ? WHERE id = ?`, tid, id)
	return tid, err
}

func (s *Store) Revoke(prefix string) (int64, error) {
	res, err := s.db.Exec(`UPDATE tokens SET revoked_at = ? WHERE token_prefix LIKE ? AND revoked_at = 0`,
		time.Now().Unix(), prefix+"%")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) List() ([]Token, error) {
	rows, err := s.db.Query(`
SELECT id, token_prefix, label, tunnel_id, source, signup_ip,
       created_at, last_used_at, revoked_at
FROM tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		tok, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *tok)
	}
	return out, rows.Err()
}

func (s *Store) ActiveForIP(ip string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens
WHERE signup_ip = ? AND source = 'open' AND revoked_at = 0`, ip).Scan(&n)
	return n, err
}

func (s *Store) CreateInvite(label string, ttl time.Duration) (string, error) {
	raw, hash := newSecret()
	id := randID(8)
	now := time.Now()
	_, err := s.db.Exec(`INSERT INTO invites (id, code_hash, label, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)`, id, hash, label, now.Unix(), now.Add(ttl).Unix())
	return raw, err
}

func (s *Store) RedeemInvite(code, label, signupIP string) (string, *Token, error) {
	hash := sha256.Sum256([]byte(code))
	tx, err := s.db.Begin()
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()

	var inviteID, inviteLabel string
	err = tx.QueryRow(`SELECT id, label FROM invites
WHERE code_hash = ? AND used_at = 0 AND expires_at > ?`, hash[:], time.Now().Unix()).
		Scan(&inviteID, &inviteLabel)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrInvalidCode
	}
	if err != nil {
		return "", nil, err
	}
	if label == "" {
		label = inviteLabel
	}
	raw, tokenHash := newSecret()
	tok, err := s.insertToken(tx, raw, tokenHash, label, "invite", signupIP, "")
	if err != nil {
		return "", nil, err
	}
	if _, err := tx.Exec(`UPDATE invites SET used_at = ?, used_token_id = ? WHERE id = ?`,
		time.Now().Unix(), tok.ID, inviteID); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	return raw, tok, nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func (s *Store) insertToken(db execer, raw string, hash []byte, label, source, signupIP, tunnelID string) (*Token, error) {
	if tunnelID == "" {
		tunnelID = randID(6)
	}
	now := time.Now()
	tok := &Token{
		ID:        randID(8),
		Prefix:    raw[:12],
		Label:     label,
		TunnelID:  tunnelID,
		Source:    source,
		SignupIP:  signupIP,
		CreatedAt: now,
	}
	_, err := db.Exec(`INSERT INTO tokens
(id, token_hash, token_prefix, label, tunnel_id, source, signup_ip, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tok.ID, hash, tok.Prefix, tok.Label, tok.TunnelID, tok.Source, tok.SignupIP, now.Unix())
	if err != nil {
		return nil, err
	}
	return tok, nil
}

// Cleanup removes expired invites and stale tokens. Tokens with a live
// connection are skipped so a long-running session is never deleted.
func (s *Store) Cleanup(now time.Time, staleAfter, openStaleAfter, revokedRetention time.Duration, active func(string) bool) (int64, error) {
	rows, err := s.db.Query(`SELECT id, tunnel_id, source, created_at, last_used_at, revoked_at FROM tokens`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id, tunnelID, source string
		var createdAt, lastUsed, revokedAt int64
		if err := rows.Scan(&id, &tunnelID, &source, &createdAt, &lastUsed, &revokedAt); err != nil {
			rows.Close()
			return 0, err
		}
		if active != nil && active(tunnelID) {
			continue
		}
		window := staleAfter
		if source == "open" {
			window = openStaleAfter
		}
		stale := time.Unix(lastUsed, 0)
		if lastUsed == 0 {
			stale = time.Unix(createdAt, 0)
		}
		remove := false
		if revokedAt > 0 && revokedAt <= now.Add(-revokedRetention).Unix() {
			remove = true
		} else if stale.Before(now.Add(-window)) {
			remove = true
		}
		if remove {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, id := range ids {
		if _, err := s.db.Exec(`DELETE FROM tokens WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	res, err := s.db.Exec(`DELETE FROM invites WHERE expires_at <= ? OR used_at > 0`, now.Unix())
	if err != nil {
		return int64(len(ids)), err
	}
	invites, _ := res.RowsAffected()
	return int64(len(ids)) + invites, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanToken(row scanner) (*Token, error) {
	var tok Token
	var createdAt, lastUsed, revokedAt int64
	err := row.Scan(&tok.ID, &tok.Prefix, &tok.Label, &tok.TunnelID, &tok.Source, &tok.SignupIP,
		&createdAt, &lastUsed, &revokedAt)
	if err != nil {
		return nil, err
	}
	tok.CreatedAt = unixTime(createdAt)
	tok.LastUsedAt = unixTime(lastUsed)
	tok.RevokedAt = unixTime(revokedAt)
	return &tok, nil
}

func unixTime(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func newSecret() (string, []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	raw := "inlet_" + base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, h[:]
}

func randID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return hex.EncodeToString(b)
}
