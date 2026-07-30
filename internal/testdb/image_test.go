// Package testdb contains integration tests for container image security requirements.
package testdb

import "testing"

func TestPostgresImageDefaultsToPinnedPgvectorImage(t *testing.T) {
	t.Setenv("FACTVAULT_TEST_POSTGRES_IMAGE", "")

	repository, tag := postgresImage()

	if repository != "pgvector/pgvector" {
		t.Fatalf("repository = %q, want pgvector/pgvector", repository)
	}
	if tag != "pg16" {
		t.Fatalf("tag = %q, want pg16 (pinned for reproducible version-sensitive tests)", tag)
	}
}

func TestPostgresImageAllowsOverride(t *testing.T) {
	t.Setenv("FACTVAULT_TEST_POSTGRES_IMAGE", "registry.example/factvault-postgres:ci")

	repository, tag := postgresImage()

	if repository != "registry.example/factvault-postgres" {
		t.Fatalf("repository = %q, want registry.example/factvault-postgres", repository)
	}
	if tag != "ci" {
		t.Fatalf("tag = %q, want ci", tag)
	}
}
