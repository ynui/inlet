package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type Dialect interface {
	Name() string
	DriverName() string
	Placeholder(n int) string
	RewriteQuery(query string) string
	UpsertSettings() string
	EnsurePragmas(db *sql.DB) error
	SetConnDefaults(db *sql.DB)
	HasColumn(db *sql.DB, table, column string) (bool, error)
	AddColumnIfMissing(db *sql.DB, table, columnDef string) error
}

func detectDialect(dsn string) (Dialect, string, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return newSQLiteDialect(), "data/inlet.db", nil
	}

	u, err := url.Parse(dsn)
	if err == nil && u.Scheme != "" {
		switch strings.ToLower(u.Scheme) {
		case "sqlite", "sqlite3", "file":
			// For file scheme, reconstruct path
			if u.Scheme == "file" {
				path := u.Path
				if path == "" && u.Opaque != "" {
					path = u.Opaque
				}
				if path == "" {
					path = "inlet.db"
				}
				// Handle file:./path or file:/absolute/path
				if strings.HasPrefix(path, "//") {
					path = path[1:]
				}
				return newSQLiteDialect(), path, nil
			}
			// sqlite:///path or sqlite:///:memory:
			path := u.Path
			if path == "" && u.Opaque != "" {
				path = u.Opaque
			}
			if path == "" || path == "/" {
				// check for :memory:
				if u.Host == ":memory:" || strings.Contains(dsn, ":memory:") {
					return newSQLiteDialect(), ":memory:", nil
				}
				path = "inlet.db"
			}
			// Remove leading slash for relative paths in some cases, but keep absolute
			if strings.HasPrefix(path, "/") && len(path) > 1 {
				// Could be absolute path - keep as-is (SQLite accepts absolute paths)
			}
			return newSQLiteDialect(), path, nil
		case "postgres", "postgresql":
			return newPostgresDialect(), dsn, nil
		case "mysql":
			return newMySQLDialect(), dsn, nil
		default:
			return nil, "", fmt.Errorf("unsupported database scheme: %s", u.Scheme)
		}
	}

	// No scheme - treat as SQLite file path (backward compatible)
	if dsn == ":memory:" {
		return newSQLiteDialect(), dsn, nil
	}
	// Looks like a path
	return newSQLiteDialect(), dsn, nil
}

type sqliteDialect struct{}

func newSQLiteDialect() Dialect { return &sqliteDialect{} }

func (d *sqliteDialect) Name() string       { return "sqlite" }
func (d *sqliteDialect) DriverName() string { return "sqlite" }
func (d *sqliteDialect) Placeholder(n int) string {
	return "?"
}
func (d *sqliteDialect) RewriteQuery(query string) string {
	return query // SQLite uses ?
}
func (d *sqliteDialect) UpsertSettings() string {
	return `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`
}
func (d *sqliteDialect) EnsurePragmas(db *sql.DB) error {
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return err
		}
	}
	return nil
}
func (d *sqliteDialect) SetConnDefaults(db *sql.DB) {
	db.SetMaxOpenConns(1)
}
func (d *sqliteDialect) HasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
func (d *sqliteDialect) AddColumnIfMissing(db *sql.DB, table, columnDef string) error {
	// columnDef should be like "account_id TEXT NOT NULL DEFAULT ''"
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, columnDef))
	return err
}

type postgresDialect struct{}

func newPostgresDialect() Dialect { return &postgresDialect{} }

func (d *postgresDialect) Name() string       { return "postgres" }
func (d *postgresDialect) DriverName() string { return "pgx" }
func (d *postgresDialect) Placeholder(n int) string {
	return fmt.Sprintf("$%d", n)
}
func (d *postgresDialect) RewriteQuery(query string) string {
	// Replace ? with $n
	var sb strings.Builder
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			sb.WriteString(fmt.Sprintf("$%d", n))
			n++
			continue
		}
		sb.WriteByte(query[i])
	}
	return sb.String()
}
func (d *postgresDialect) UpsertSettings() string {
	return `INSERT INTO settings (key, value) VALUES ($1, $2)
ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value`
}
func (d *postgresDialect) EnsurePragmas(db *sql.DB) error {
	return nil
}
func (d *postgresDialect) SetConnDefaults(db *sql.DB) {
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
}
func (d *postgresDialect) HasColumn(db *sql.DB, table, column string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`, table, column).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
func (d *postgresDialect) AddColumnIfMissing(db *sql.DB, table, columnDef string) error {
	// Try to add column if not exists (Postgres 9.6+)
	// columnDef is like "account_id TEXT NOT NULL DEFAULT ''"
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s", table, columnDef))
	return err
}

type mysqlDialect struct{}

func newMySQLDialect() Dialect { return &mysqlDialect{} }

func (d *mysqlDialect) Name() string       { return "mysql" }
func (d *mysqlDialect) DriverName() string { return "mysql" }
func (d *mysqlDialect) Placeholder(n int) string {
	return "?"
}
func (d *mysqlDialect) RewriteQuery(query string) string {
	return query // MySQL uses ?
}
func (d *mysqlDialect) UpsertSettings() string {
	return `INSERT INTO settings (key, value) VALUES (?, ?)
ON DUPLICATE KEY UPDATE value = VALUES(value)`
}
func (d *mysqlDialect) EnsurePragmas(db *sql.DB) error {
	// Set session vars if needed
	if _, err := db.Exec("SET time_zone = '+00:00'"); err != nil {
		// ignore
	}
	return nil
}
func (d *mysqlDialect) SetConnDefaults(db *sql.DB) {
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
}
func (d *mysqlDialect) HasColumn(db *sql.DB, table, column string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`, table, column).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
func (d *mysqlDialect) AddColumnIfMissing(db *sql.DB, table, columnDef string) error {
	// MySQL doesn't support ADD COLUMN IF NOT EXISTS in older versions easily; try and ignore duplicate column error
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, columnDef))
	if err != nil {
		// Check if it's duplicate column error
		var mysqlErr interface{ Number() uint16 }
		if errors.As(err, &mysqlErr) && mysqlErr.Number() == 1060 { // ER_DUP_FIELDNAME
			return nil
		}
		return err
	}
	return nil
}
