//go:build sqlite && cgo

package store

import "testing"

func TestParseBackendAcceptsSQLiteWhenTagged(t *testing.T) {
	t.Parallel()
	got, err := ParseBackend("sqlite")
	if err != nil {
		t.Fatalf("ParseBackend: %v", err)
	}
	if got != BackendSQLite {
		t.Fatalf("got %q, want %q", got, BackendSQLite)
	}
}
