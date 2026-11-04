package store

import (
	"database/sql"
)

func migrate(db *sql.DB, d Dialect) error {
	q := QCreateTables
	if d.Name() != "sqlite" {
		// For Postgres/MySQL, need to adapt types slightly for better compatibility
		if d.Name() == "postgres" {
			q = `CREATE TABLE IF NOT EXISTS tokens (
  id TEXT PRIMARY KEY,
  token_hash BYTEA NOT NULL UNIQUE,
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
  code_hash BYTEA NOT NULL UNIQUE,
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
		} else if d.Name() == "mysql" {
			q = `CREATE TABLE IF NOT EXISTS tokens (
  id VARCHAR(191) PRIMARY KEY,
  token_hash VARBINARY(32) NOT NULL,
  token_prefix VARCHAR(191) NOT NULL,
  label VARCHAR(191) NOT NULL DEFAULT '',
  tunnel_id VARCHAR(191) NOT NULL UNIQUE,
  source VARCHAR(191) NOT NULL DEFAULT 'admin',
  signup_ip VARCHAR(191) NOT NULL DEFAULT '',
  account_id VARCHAR(191) NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  last_used_at BIGINT NOT NULL DEFAULT 0,
  revoked_at BIGINT NOT NULL DEFAULT 0,
  UNIQUE KEY token_hash_unique (token_hash)
);
CREATE INDEX IF NOT EXISTS tokens_last_used ON tokens(last_used_at);
CREATE INDEX IF NOT EXISTS tokens_ip ON tokens(signup_ip);

CREATE TABLE IF NOT EXISTS accounts (
  id VARCHAR(191) PRIMARY KEY,
  provider VARCHAR(191) NOT NULL,
  provider_subject VARCHAR(191) NOT NULL,
  login VARCHAR(191) NOT NULL,
  created_at BIGINT NOT NULL,
  UNIQUE KEY provider_subject_unique (provider, provider_subject)
);

CREATE TABLE IF NOT EXISTS invites (
  id VARCHAR(191) PRIMARY KEY,
  code_hash VARBINARY(32) NOT NULL,
  label VARCHAR(191) NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  used_at BIGINT NOT NULL DEFAULT 0,
  used_token_id VARCHAR(191) NOT NULL DEFAULT '',
  UNIQUE KEY code_hash_unique (code_hash)
);
CREATE TABLE IF NOT EXISTS settings (
  ` + "`key`" + ` VARCHAR(191) PRIMARY KEY,
  value TEXT NOT NULL
);
`

		}
	}
	if _, err := db.Exec(q); err != nil {
		return err
	}

	// MySQL uses different column name for settings key? Let us handle
	if d.Name() == "mysql" {
		// Check if old settings table exists with different schema
		// Try to migrate
		var count int
		db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'settings'").Scan(&count)
		// Simple approach - if key_name doesn't exist, try to rename; easier to just recreate logic not critical for now
	}

	if _, err := db.Exec(QCreateIndexTokensAccount); err != nil {
		return err
	}

	// Add account_id column if missing
	hasAccountID, err := d.HasColumn(db, "tokens", "account_id")
	if err != nil {
		return err
	}
	if !hasAccountID {
		if err := d.AddColumnIfMissing(db, "tokens", "account_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}

	return nil
}
