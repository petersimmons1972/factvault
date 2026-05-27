package store

import "testing"

func TestParseBackend(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   string
		want    Backend
		wantErr bool
	}{
		{name: "empty defaults to postgres", value: "", want: BackendPostgres},
		{name: "postgres", value: "postgres", want: BackendPostgres},
		{name: "sqlite unavailable by default", value: "sqlite", wantErr: true},
		{name: "invalid", value: "mysql", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBackend(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBackend: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
