package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/labels"
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
	db      *sql.DB
	dialect Dialect
	dsn     string
}

func Open(dsn string) (*Store, error) {
	db, d, resolved, err := openDB(dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(db, d); err != nil {
		db.Close()
		return nil, err
	}
	if d.Name() == "sqlite" && resolved != ":memory:" {
		if err := os.Chmod(resolved, 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, dialect: d, dsn: resolved}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) GetSetting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(s.dialect.RewriteQuery(QGetSetting), key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SetSetting(key, value string) error {
	q := s.dialect.UpsertSettings()
	if q == "" {
		q = s.dialect.RewriteQuery(QSetSetting + ` ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
		// fallback - but dialect should provide
		if s.dialect.Name() == "postgres" {
			q = `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value`
		} else if s.dialect.Name() == "mysql" {
			q = `INSERT INTO settings (` + "`key`" + `, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)`
		} else {
			q = s.dialect.RewriteQuery(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
		}
	}
	// For MySQL we need to handle backticks in our dialect
	if s.dialect.Name() == "mysql" && q == s.dialect.UpsertSettings() {
		// use our dialect's version
		q = `INSERT INTO settings (` + "`key`" + `, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)`
	}
	_, err := s.db.Exec(q, key, value)
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

	subject := fmt.Sprintf("%d", githubID)
	var accountID string
	err = tx.QueryRow(s.dialect.RewriteQuery(QFindAccountByProvider), "github", subject).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		if !open {
			return "", nil, ErrSignupClosed
		}
		accountID = randID(16)
		if _, err := tx.Exec(s.dialect.RewriteQuery(QInsertAccount), accountID, "github", subject, login, time.Now().Unix()); err != nil {
			return "", nil, err
		}
	} else if err != nil {
		return "", nil, err
	} else if _, err := tx.Exec(s.dialect.RewriteQuery(QUpdateAccountLogin), login, accountID); err != nil {
		return "", nil, err
	}

	// Re-issue the most recent active token instead of creating a new one, so
	// repeated logins don't pile up tokens or rotate the tunnel ID.
	row := tx.QueryRow(s.dialect.RewriteQuery(QLatestActiveTokenForAccount), accountID)
	if tok, err := scanToken(row); err == nil {
		raw, hash := newSecret()
		if _, err := tx.Exec(s.dialect.RewriteQuery(QReissueToken), hash, raw[:12], label, tok.ID); err != nil {
			return "", nil, err
		}
		if err := tx.Commit(); err != nil {
			return "", nil, err
		}
		tok.Prefix = raw[:12]
		tok.Label = label
		tok.AccountLogin = login
		return raw, tok, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}

	var active int
	if err := tx.QueryRow(s.dialect.RewriteQuery(QCountActiveTokensForAccount), accountID).Scan(&active); err != nil {
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

func (s *Store) SeedAdmin(raw, label string) error {
	if len(raw) < 32 {
		return fmt.Errorf("admin token must be at least 32 characters; use a randomly generated secret")
	}
	return s.seed(raw, label, "admin", "")
}

func (s *Store) seed(raw, label, source, tunnelID string) error {
	hash := sha256.Sum256([]byte(raw))
	var one int
	err := s.db.QueryRow(s.dialect.RewriteQuery(QFindTokenHashExists), hash[:]).Scan(&one)
	if err == nil {
		if source == "admin" && label == "admin-token" {
			_, err = s.db.Exec(s.dialect.RewriteQuery(QUpdateAdminTokenLabel), label, hash[:])
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
	row := s.db.QueryRow(s.dialect.RewriteQuery(QValidateToken), hash[:])
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
	_, err := s.db.Exec(s.dialect.RewriteQuery(QTouchToken), time.Now().Unix(), id)
	return err
}

func (s *Store) RotateTunnel(id string) (string, error) {
	tid := randID(6)
	_, err := s.db.Exec(s.dialect.RewriteQuery(QRotateTunnel), tid, id)
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
	q := s.dialect.RewriteQuery(QRevokeByPrefix)
	for i, prefix := range prefixes {
		res, err := tx.Exec(q, time.Now().Unix(), prefix, prefix)
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
	rows, err := s.db.Query(s.dialect.RewriteQuery(QListTokens))
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
	if err := s.db.QueryRow(s.dialect.RewriteQuery(QCountTokens)).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(s.dialect.RewriteQuery(QListTokensPage), limit, offset)
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
	rows, err := s.db.Query(s.dialect.RewriteQuery(QListTokensForClient), accountID, accountID, accountID, ip, currentTokenID)
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
	res, err := s.db.Exec(s.dialect.RewriteQuery(QRevokeForClient),
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
	res, err := s.db.Exec(s.dialect.RewriteQuery(QUpdateLabelClient),
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
	res, err := s.db.Exec(s.dialect.RewriteQuery(QUpdateLabel), label, prefix)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) ActiveForIP(ip string) (int, error) {
	var n int
	err := s.db.QueryRow(s.dialect.RewriteQuery(QActiveForIP), ip).Scan(&n)
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
	_, err = s.db.Exec(s.dialect.RewriteQuery(QCreateInvite), id, hash, label, now.Unix(), now.Add(ttl).Unix())
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
	err = tx.QueryRow(s.dialect.RewriteQuery(QFindInvite), hash[:], time.Now().Unix()).
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
	if _, err := tx.Exec(s.dialect.RewriteQuery(QMarkInviteUsed), time.Now().Unix(), tok.ID, inviteID); err != nil {
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
	_, err := db.Exec(s.dialect.RewriteQuery(QInsertToken),
		tok.ID, hash, tok.Prefix, tok.Label, tok.TunnelID, tok.Source, tok.SignupIP, tok.AccountID, now.Unix())
	if err != nil {
		return nil, err
	}
	return tok, nil
}

func (s *Store) Cleanup(now time.Time, staleAfter, openStaleAfter, revokedRetention time.Duration, active func(string) bool) (int64, error) {
	rows, err := s.db.Query(s.dialect.RewriteQuery(QGetAllTokensForCleanup))
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
		if _, err := s.db.Exec(s.dialect.RewriteQuery(QDeleteTokenByID), id); err != nil {
			return 0, err
		}
	}
	res, err := s.db.Exec(s.dialect.RewriteQuery(QDeleteInvites), now.Unix())
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

func maskDSN(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if u.User != nil {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			return u.String()
		}
		return dsn
	}
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		before := dsn[:at]
		if colon := strings.LastIndex(before, ":"); colon >= 0 {
			return before[:colon] + ":xxxxx@" + dsn[at+1:]
		}
	}
	return dsn
}

func (s *Store) DSN() string { return s.dsn }

func (s *Store) MaskedDSN() string { return maskDSN(s.dsn) }
