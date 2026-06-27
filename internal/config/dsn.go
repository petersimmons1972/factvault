package config

import (
	"fmt"
	"net/url"
)

// ValidateDSNNoPassword rejects a DSN whose URL userinfo carries an inline
// password.  A password in a URL-form DSN (postgres://user:secret@host/db)
// is exposed to every process on the machine via /proc/<pid>/cmdline and
// shows up in shell history when supplied as a CLI flag.
//
// Operators should supply credentials via FACTVAULT_DATABASE_URL (env),
// a ~/.pgpass file, or the PGPASSWORD env var instead.
//
// Non-URL DSNs (e.g. "host=localhost user=foo dbname=bar") cannot carry an
// inline password in the URL sense, so they are always accepted.  Malformed
// URLs are also accepted here — the DB driver will surface parse errors at
// connection time.
func ValidateDSNNoPassword(dsn string) error {
	if dsn == "" {
		return nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		// Let the DB driver surface parse errors; don't gate on them here.
		return nil
	}
	if u.User != nil {
		if p, hasPass := u.User.Password(); hasPass && p != "" {
			return fmt.Errorf("--dsn must not contain a password in the URL " +
				"(use FACTVAULT_DATABASE_URL env, ~/.pgpass, or PGPASSWORD instead)")
		}
	}
	return nil
}
