package config

import (
	"testing"
)

func TestValidateDSNNoPassword(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{
			name:    "empty DSN is ok",
			dsn:     "",
			wantErr: false,
		},
		{
			name:    "URL with no userinfo is ok",
			dsn:     "postgres://host/db",
			wantErr: false,
		},
		{
			name:    "URL with username only is ok",
			dsn:     "postgres://user@host/db",
			wantErr: false,
		},
		{
			name:    "URL with empty password is ok",
			dsn:     "postgres://user:@host/db",
			wantErr: false,
		},
		{
			name:    "URL with password is rejected",
			dsn:     "postgres://user:secret@host/db", //nolint:gosec // G101: intentional test value
			wantErr: true,
		},
		{
			name:    "URL with password and path is rejected",
			dsn:     "postgresql://app:p%40ss@db.prod:5432/factvault?sslmode=require", //nolint:gosec // G101: intentional test value
			wantErr: true,
		},
		{
			name:    "key=value DSN style is ok",
			dsn:     "host=localhost user=foo dbname=bar",
			wantErr: false,
		},
		{
			name:    "malformed URL is ok (DB driver will catch it)",
			dsn:     "not a url ://garbage",
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDSNNoPassword(tc.dsn)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateDSNNoPassword(%q) err=%v, wantErr=%v", tc.dsn, err, tc.wantErr)
			}
		})
	}
}
