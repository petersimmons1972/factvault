package deploy_test

import (
	"os"
	"strings"
	"testing"
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
		"FACTVAULT_DEV_TENANT_ID:-11111111-1111-1111-1111-111111111111",
		"condition: service_completed_successfully",
		"\"factvault\", \"migrate\"",
		"factvault doctor",
		"/var/lib/factvault/auth",
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("docker-compose.yml missing %q", expected)
		}
	}
}
