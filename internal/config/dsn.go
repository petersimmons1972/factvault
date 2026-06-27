package config

import (
	"fmt"
	"net/url"
)

// ValidateDSNNoPassword returns an error if dsn is a URL-form DSN with a
// non-empty password. Called when --dsn was explicitly set on the command line
// to guard against passwords leaking to /proc/<pid>/cmdline.
// Key=value DSN style (host=... user=... password=...) is not affected.
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
