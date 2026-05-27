package store

import "fmt"

// Backend selects the configured persistence backend.
type Backend string

const (
	// BackendPostgres selects the pgx-backed Postgres store.
	BackendPostgres Backend = "postgres"
	// BackendSQLite selects the local SQLite store.
	BackendSQLite Backend = "sqlite"
)

// ParseBackend validates the store.backend config knob.
func ParseBackend(value string) (Backend, error) {
	switch Backend(value) {
	case "", BackendPostgres:
		return BackendPostgres, nil
	case BackendSQLite:
		if !sqliteAvailable() {
			return "", fmt.Errorf("unsupported store backend %q: rebuild with -tags sqlite and CGO enabled", value)
		}
		return BackendSQLite, nil
	default:
		return "", fmt.Errorf("unsupported store backend %q: expected postgres or sqlite", value)
	}
}
