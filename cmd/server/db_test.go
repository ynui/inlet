package main

import "testing"

func TestDatabasePath(t *testing.T) {
	t.Setenv("INLET_DB", "")
	if p, err := databasePath(); err != nil || p != "data/inlet.db" {
		t.Fatalf("default path = %q, %v; want data/inlet.db", p, err)
	}
	t.Setenv("INLET_DB", "/tmp/custom.db")
	if p, err := databasePath(); err != nil || p != "/tmp/custom.db" {
		t.Fatalf("override = %q, %v; want /tmp/custom.db", p, err)
	}
}
