package testdb

import "testing"

func TestPostgresImageDefaultsToLatestPgvectorImage(t *testing.T) {
	t.Setenv("FACTVAULT_TEST_POSTGRES_IMAGE", "")

	repository, tag := postgresImage()

	if repository != "ankane/pgvector" {
		t.Fatalf("repository = %q, want ankane/pgvector", repository)
	}
	if tag != "latest" {
		t.Fatalf("tag = %q, want latest", tag)
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
