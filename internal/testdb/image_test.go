package testdb

import "testing"

func TestPostgresImageDefaultsToFactVaultChainguardImage(t *testing.T) {
	t.Setenv("FACTVAULT_TEST_POSTGRES_IMAGE", "")

	repository, tag := postgresImage()

	if repository != "factvault-postgres" {
		t.Fatalf("repository = %q, want factvault-postgres", repository)
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
