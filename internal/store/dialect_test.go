package store

import "testing"

func TestDetectDialectDSNs(t *testing.T) {
	d, resolved, err := detectDialect("mysql://inlet:inlet@tcp(localhost:3306)/inlet")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name() != "mysql" || resolved != "inlet:inlet@tcp(localhost:3306)/inlet" {
		t.Fatalf("mysql: %s %q", d.Name(), resolved)
	}
	d, resolved, err = detectDialect("postgres://inlet:inlet@localhost:5432/inlet?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name() != "postgres" || resolved != "postgres://inlet:inlet@localhost:5432/inlet?sslmode=disable" {
		t.Fatalf("postgres: %s %q", d.Name(), resolved)
	}
	if _, _, err := detectDialect("bogus://unparseable%%"); err == nil {
		t.Fatal("unparseable URI-style DSN should fail, not silently become a sqlite path")
	}
	if d, _, err := detectDialect("data/inlet.db"); err != nil || d.Name() != "sqlite" {
		t.Fatalf("plain path: %v", err)
	}
}
