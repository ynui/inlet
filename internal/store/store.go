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
	"strconv"
	"time"

	"github.com/ynui/inlet/internal/labels"

	_ "modernc.org/sqlite"
)

var (
	ErrInvalidToken      = errors.New("invalid token")
	ErrInvalidCode       = errors.New("invalid or expired invite")
	ErrSignupClosed      = errors.New("GitHub signup is disabled for new accounts")
	ErrAccountTokenLimit = errors.New("account token limit reached")
)

type Token struct {
	ID           string
	Prefix       string
	Label        string
	TunnelID     string
	Source       string
	SignupIP     string
	AccountID    string
	AccountLogin string
	CreatedAt    time.Time
	LastUsedAt   time.Time
	RevokedAt    time.Time
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
  account_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  revoked_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tokens_last_used ON tokens(last_used_at);
CREATE INDEX IF NOT EXISTS tokens_ip ON tokens(signup_ip);

CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  provider_subject TEXT NOT NULL,
  login TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(provider, provider_subject)
);

CREATE TABLE IF NOT EXISTS invites (
  id TEXT PRIMARY KEY,
  code_hash BLOB NOT NULL UNIQUE,
  label TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at INTEGER NOT NULL DEFAULT 0,
  used_token_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`)
	if err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info(tokens)`)
	if err != nil {
		return err
	}
	hasAccountID := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "account_id" {
			hasAccountID = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !hasAccountID {
		if _, err := db.Exec(`ALTER TABLE tokens ADD COLUMN account_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS tokens_account ON tokens(account_id)`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) GetSetting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) Issue(label, source, signupIP string) (string, *Token, error) {
	var err error
	label, err = labels.Normalize(label)
	if err != nil {
		return "", nil, err
	}
	raw, hash := newSecret()
	tok, err := s.insertToken(s.db, raw, hash, label, source, signupIP, "", "")
	if err != nil {
		return "", nil, err
	}
	return raw, tok, nil
}

// IssueGitHub creates an inlet token for a GitHub user. Existing accounts may
// add devices while open signup is disabled; new accounts require open signup.
func (s *Store) IssueGitHub(label string, githubID int64, login, signupIP string, open bool) (string, *Token, error) {
	label, err := labels.Normalize(label)
	if err != nil {
		return "", nil, err
	}
	if githubID <= 0 || login == "" {
		return "", nil, fmt.Errorf("invalid GitHub identity")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()

	subject := strconv.FormatInt(githubID, 10)
	var accountID string
	err = tx.QueryRow(`SELECT id FROM accounts WHERE provider = 'github' AND provider_subject = ?`, subject).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		if !open {
			return "", nil, ErrSignupClosed
		}
		accountID = randID(16)
		if _, err := tx.Exec(`INSERT INTO accounts (id, provider, provider_subject, login, created_at)
VALUES (?, 'github', ?, ?, ?)`, accountID, subject, login, time.Now().Unix()); err != nil {
			return "", nil, err
		}
	} else if err != nil {
		return "", nil, err
	} else if _, err := tx.Exec(`UPDATE accounts SET login = ? WHERE id = ?`, login, accountID); err != nil {
		return "", nil, err
	}

	var active int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tokens WHERE account_id = ? AND revoked_at = 0`, accountID).Scan(&active); err != nil {
		return "", nil, err
	}
	if active >= 10 {
		return "", nil, fmt.Errorf("%w (10 active devices); revoke an unused token first", ErrAccountTokenLimit)
	}
	raw, hash := newSecret()
	tok, err := s.insertToken(tx, raw, hash, label, "github", signupIP, "", accountID)
	if err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	tok.AccountLogin = login
	return raw, tok, nil
}

// SeedAdmin provisions a remote administrator token from a secret environment variable.
func (s *Store) SeedAdmin(raw, label string) error {
	if len(raw) < 32 {
		return fmt.Errorf("admin token must be at least 32 characters; use a randomly generated secret")
	}
	return s.seed(raw, label, "admin", "")
}

func (s *Store) seed(raw, label, source, tunnelID string) error {
	hash := sha256.Sum256([]byte(raw))
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM tokens WHERE token_hash = ?`, hash[:]).Scan(&one)
	if err == nil {
		if source == "admin" && label == "admin-token" {
			_, err = s.db.Exec(`UPDATE tokens SET label = ? WHERE token_hash = ? AND source = 'admin' AND label = 'environment'`, label, hash[:])
			return err
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.insertToken(s.db, raw, hash[:], label, source, "", tunnelID, "")
	return err
}

func (s *Store) Validate(raw string) (*Token, error) {
	hash := sha256.Sum256([]byte(raw))
	row := s.db.QueryRow(`
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id WHERE t.token_hash = ?`, hash[:])
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
	counts, err := s.RevokeMany([]string{prefix})
	if err != nil {
		return 0, err
	}
	return counts[0], nil
}

func (s *Store) RevokeMany(prefixes []string) ([]int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	counts := make([]int64, len(prefixes))
	for i, prefix := range prefixes {
		res, err := tx.Exec(`UPDATE tokens SET revoked_at = ?
WHERE substr(token_prefix, 1, length(?)) = ? AND revoked_at = 0`,
			time.Now().Unix(), prefix, prefix)
		if err != nil {
			return nil, err
		}
		counts[i], err = res.RowsAffected()
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return counts, nil
}

func (s *Store) List() ([]Token, error) {
	rows, err := s.db.Query(`
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id ORDER BY t.created_at DESC`)
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

func (s *Store) ListPage(limit, offset int) ([]Token, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id
ORDER BY t.created_at DESC, t.id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Token, 0, limit)
	for rows.Next() {
		tok, err := scanToken(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *tok)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (s *Store) ListForClient(accountID, ip, currentTokenID string) ([]Token, error) {
	rows, err := s.db.Query(`
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id
WHERE (t.account_id = ? AND ? != '')
   OR (? = '' AND t.account_id = '' AND t.source = 'open' AND t.signup_ip = ?)
   OR t.id = ?
ORDER BY t.created_at DESC`, accountID, accountID, accountID, ip, currentTokenID)
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

func (s *Store) RevokeForClient(prefix, accountID, ip, currentTokenID string) (int64, error) {
	res, err := s.db.Exec(`UPDATE tokens SET revoked_at = ?
WHERE ((account_id = ? AND ? != '')
    OR (? = '' AND account_id = '' AND source = 'open' AND signup_ip = ?)
    OR id = ?)
  AND token_prefix = ? AND revoked_at = 0`,
		time.Now().Unix(), accountID, accountID, accountID, ip, currentTokenID, prefix)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) UpdateClientLabel(prefix, accountID, ip, currentTokenID, label string) (int64, error) {
	var err error
	label, err = labels.Normalize(label)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`UPDATE tokens SET label = ?
WHERE ((account_id = ? AND ? != '')
    OR (? = '' AND account_id = '' AND source = 'open' AND signup_ip = ?)
    OR id = ?) AND token_prefix = ?`,
		label, accountID, accountID, accountID, ip, currentTokenID, prefix)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) UpdateLabel(prefix, label string) (int64, error) {
	var err error
	label, err = labels.Normalize(label)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`UPDATE tokens SET label = ? WHERE token_prefix = ?`, label, prefix)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) ActiveForIP(ip string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens
WHERE signup_ip = ? AND source = 'open' AND revoked_at = 0`, ip).Scan(&n)
	return n, err
}

func (s *Store) CreateInvite(label string, ttl time.Duration) (string, error) {
	var err error
	label, err = labels.Normalize(label)
	if err != nil {
		return "", err
	}
	raw, hash := newSecret()
	id := randID(8)
	now := time.Now()
	_, err = s.db.Exec(`INSERT INTO invites (id, code_hash, label, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)`, id, hash, label, now.Unix(), now.Add(ttl).Unix())
	return raw, err
}

func (s *Store) RedeemInvite(code, label, signupIP string) (string, *Token, error) {
	var err error
	label, err = labels.Normalize(label)
	if err != nil {
		return "", nil, err
	}
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
	label, err = labels.Normalize(label)
	if err != nil {
		return "", nil, err
	}
	raw, tokenHash := newSecret()
	tok, err := s.insertToken(tx, raw, tokenHash, label, "invite", signupIP, "", "")
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

func (s *Store) insertToken(db execer, raw string, hash []byte, label, source, signupIP, tunnelID, accountID string) (*Token, error) {
	if len(raw) < 12 {
		return nil, fmt.Errorf("token must be at least 12 characters")
	}
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
		AccountID: accountID,
		CreatedAt: now,
	}
	_, err := db.Exec(`INSERT INTO tokens
(id, token_hash, token_prefix, label, tunnel_id, source, signup_ip, account_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tok.ID, hash, tok.Prefix, tok.Label, tok.TunnelID, tok.Source, tok.SignupIP, tok.AccountID, now.Unix())
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
		&tok.AccountID, &tok.AccountLogin, &createdAt, &lastUsed, &revokedAt)
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
