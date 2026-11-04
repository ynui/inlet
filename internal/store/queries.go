package store

// All SQL queries centralized in one place
const (
	QCreateTables = `
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
`

	QCreateIndexTokensAccount = `CREATE INDEX IF NOT EXISTS tokens_account ON tokens(account_id)`

	QGetSetting    = `SELECT value FROM settings WHERE key = ?`
	QSetSetting    = `INSERT INTO settings (key, value) VALUES (?, ?)` // will be rewritten by dialect for upsert
	QUpsertSetting = ``                                                // dialect provides

	QFindAccountByProvider       = `SELECT id FROM accounts WHERE provider = ? AND provider_subject = ?`
	QInsertAccount               = `INSERT INTO accounts (id, provider, provider_subject, login, created_at) VALUES (?, ?, ?, ?, ?)`
	QUpdateAccountLogin          = `UPDATE accounts SET login = ? WHERE id = ?`
	QCountActiveTokensForAccount = `SELECT COUNT(*) FROM tokens WHERE account_id = ? AND revoked_at = 0`

	QInsertToken = `INSERT INTO tokens
(id, token_hash, token_prefix, label, tunnel_id, source, signup_ip, account_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	QLatestActiveTokenForAccount = `
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id
WHERE t.account_id = ? AND t.revoked_at = 0
ORDER BY t.created_at DESC, t.id DESC LIMIT 1`
	QReissueToken = `UPDATE tokens SET token_hash = ?, token_prefix = ?, label = ? WHERE id = ?`

	QFindTokenHashExists   = `SELECT 1 FROM tokens WHERE token_hash = ?`
	QUpdateAdminTokenLabel = `UPDATE tokens SET label = ? WHERE token_hash = ? AND source = 'admin' AND label = 'environment'`

	QValidateToken = `
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id WHERE t.token_hash = ?`

	QTouchToken     = `UPDATE tokens SET last_used_at = ? WHERE id = ?`
	QRotateTunnel   = `UPDATE tokens SET tunnel_id = ? WHERE id = ?`
	QRevokeByPrefix = `UPDATE tokens SET revoked_at = ? WHERE substr(token_prefix, 1, length(?)) = ? AND revoked_at = 0`
	QListTokens     = `
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id ORDER BY t.created_at DESC`
	QCountTokens    = `SELECT COUNT(*) FROM tokens`
	QListTokensPage = `
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id
ORDER BY t.created_at DESC, t.id DESC LIMIT ? OFFSET ?`
	QListTokensForClient = `
SELECT t.id, t.token_prefix, t.label, t.tunnel_id, t.source, t.signup_ip,
       t.account_id, COALESCE(a.login, ''), t.created_at, t.last_used_at, t.revoked_at
FROM tokens t LEFT JOIN accounts a ON a.id = t.account_id
WHERE (t.account_id = ? AND ? != '')
   OR (? = '' AND t.account_id = '' AND t.source = 'open' AND t.signup_ip = ?)
   OR t.id = ?
ORDER BY t.created_at DESC`
	QRevokeForClient = `UPDATE tokens SET revoked_at = ?
WHERE ((account_id = ? AND ? != '')
    OR (? = '' AND account_id = '' AND source = 'open' AND signup_ip = ?)
    OR id = ?)
  AND token_prefix = ? AND revoked_at = 0`
	QUpdateLabelClient = `UPDATE tokens SET label = ?
WHERE ((account_id = ? AND ? != '')
    OR (? = '' AND account_id = '' AND source = 'open' AND signup_ip = ?)
    OR id = ?) AND token_prefix = ?`
	QUpdateLabel = `UPDATE tokens SET label = ? WHERE token_prefix = ?`
	QActiveForIP = `SELECT COUNT(*) FROM tokens
WHERE signup_ip = ? AND source = 'open' AND revoked_at = 0`
	QGetAllTokensForCleanup = `SELECT id, tunnel_id, source, created_at, last_used_at, revoked_at FROM tokens`
	QDeleteTokenByID        = `DELETE FROM tokens WHERE id = ?`

	QCreateInvite = `INSERT INTO invites (id, code_hash, label, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)`
	QFindInvite = `SELECT id, label FROM invites
WHERE code_hash = ? AND used_at = 0 AND expires_at > ?`
	QMarkInviteUsed = `UPDATE invites SET used_at = ?, used_token_id = ? WHERE id = ?`
	QDeleteInvites  = `DELETE FROM invites WHERE expires_at <= ? OR used_at > 0`
)
