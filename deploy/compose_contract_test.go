package deploy_test

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/petersimmons1972/factvault/internal/doctor"
)

func TestDockerComposeTier1Contract(t *testing.T) {
	data, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	compose := string(data)

	for _, service := range []string{
		"postgres",
		"embedder",
		"ollama",
		"factvault-migrate",
		"factvault-api",
		"factvault-workers",
		"factvault-mcp",
	} {
		if !strings.Contains(compose, "\n  "+service+":") {
			t.Fatalf("docker-compose.yml missing service %q", service)
		}
	}
	if strings.Contains(compose, "env_file:") {
		t.Fatal("Tier 1 compose must not require a local .env file")
	}
	for _, expected := range []string{
		"${FACTVAULT_POSTGRES_IMAGE:-ankane/pgvector:latest}",
		"FACTVAULT_DEV_TENANT_ID:-11111111-1111-1111-1111-111111111111",
		"condition: service_completed_successfully",
		"\"factvault\", \"migrate\"",
		"factvault doctor",
		"/var/lib/factvault/auth",
		"status_file=/tmp/factvault-workers.status",
		"grep -q '^ok ' /tmp/factvault-workers.status",
		"FACTVAULT_WORKER_FAILURE_RETRY_SECONDS:-30",
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("docker-compose.yml missing %q", expected)
		}
	}
	for _, unexpected := range []string{
		"/tmp/factvault-workers.ready",
		"test -f /tmp/factvault-workers.ready",
	} {
		if strings.Contains(compose, unexpected) {
			t.Fatalf("docker-compose.yml contains deprecated false-green health sentinel %q", unexpected)
		}
	}
	if strings.Contains(compose, "pgvector/pgvector") {
		t.Fatal("Tier 1 compose must use an existing latest-tag pgvector image")
	}
}

func TestDockerComposeEmbedderPortMatchesDoctorDefault(t *testing.T) {
	data, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}

	matches := regexp.MustCompile(`FACTVAULT_EMBEDDER_PORT:-([0-9]+)}:8080`).FindStringSubmatch(string(data))
	if len(matches) != 2 {
		t.Fatal("docker-compose.yml missing FACTVAULT_EMBEDDER_PORT host mapping")
	}

	embedderURL, err := url.Parse(doctor.DefaultEmbedderURL)
	if err != nil {
		t.Fatalf("parse doctor embedder default %q: %v", doctor.DefaultEmbedderURL, err)
	}

	if got := embedderURL.Port(); got != matches[1] {
		t.Fatalf("doctor default port = %q, want compose host port %q", got, matches[1])
	}
}
