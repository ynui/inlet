package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func openDB(dsn string) (*sql.DB, Dialect, string, error) {
	d, resolved, err := detectDialect(dsn)
	if err != nil {
		return nil, nil, "", err
	}

	var db *sql.DB
	switch d.Name() {
	case "sqlite":
		// Ensure directory exists if it's a file path
		if resolved != ":memory:" {
			if err := os.MkdirAll(filepath.Dir(resolved), 0700); err != nil {
				return nil, nil, "", err
			}
		}
		db, err = sql.Open("sqlite", resolved)
		if err != nil {
			return nil, nil, "", err
		}
	case "postgres":
		db, err = sql.Open("pgx", resolved)
		if err != nil {
			return nil, nil, "", err
		}
	case "mysql":
		db, err = sql.Open("mysql", resolved)
		if err != nil {
			return nil, nil, "", err
		}
	default:
		return nil, nil, "", fmt.Errorf("unsupported dialect")
	}

	d.SetConnDefaults(db)
	if err := d.EnsurePragmas(db); err != nil {
		db.Close()
		return nil, nil, "", err
	}

	return db, d, resolved, nil
}
