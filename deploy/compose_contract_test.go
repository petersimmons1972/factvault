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

func TestDockerComposeRSSFeedsContract(t *testing.T) {
	t.Parallel()

	composeData, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	dockerfileData, err := os.ReadFile("docker/Dockerfile")
	if err != nil {
		t.Fatalf("read deploy/docker/Dockerfile: %v", err)
	}
	workerCmdData, err := os.ReadFile("../cmd/factvault/worker.go")
	if err != nil {
		t.Fatalf("read cmd/factvault/worker.go: %v", err)
	}

	compose := string(composeData)
	dockerfile := string(dockerfileData)
	workerCmd := string(workerCmdData)

	if !strings.Contains(compose, "factvault worker rss --once") {
		t.Fatal("docker-compose.yml no longer starts the worker loop with `factvault worker rss --once`; update this contract test")
	}
	if !strings.Contains(workerCmd, `"config/feeds.yaml"`) {
		t.Fatal("cmd/factvault/worker.go no longer defaults RSS feeds to config/feeds.yaml; update this contract test")
	}

	composeSetsFeedsPath := strings.Contains(compose, "FACTVAULT_FEEDS_PATH")
	dockerfileCopiesConfig := strings.Contains(dockerfile, "COPY --from=builder /app/config /app/config")
	if !composeSetsFeedsPath && !dockerfileCopiesConfig {
		t.Fatal("docker-compose.yml runs `factvault worker rss --once` using the default config/feeds.yaml, so deploy/docker/Dockerfile must copy /app/config into the runtime image or Compose must set FACTVAULT_FEEDS_PATH explicitly")
	}
}

func TestK8sRSSCronJobRunsRSSContract(t *testing.T) {
	t.Parallel()

	cronData, err := os.ReadFile("k8s/rss-worker-cronjob.yaml")
	if err != nil {
		t.Fatalf("read deploy/k8s/rss-worker-cronjob.yaml: %v", err)
	}
	cron := string(cronData)

	if !strings.Contains(cron, "name: factvault-rss") {
		t.Fatal("k8s RSS CronJob must be named factvault-rss so operators do not confuse it with the retired collect stub")
	}
	if strings.Contains(cron, "name: factvault-collect") {
		t.Fatal("k8s RSS CronJob still uses the retired factvault-collect name")
	}
	if !strings.Contains(cron, `"rss", "--once"`) {
		t.Fatal("k8s RSS CronJob must run `worker rss --once` (real feed ingestion), not the static `worker collect` stub — see issue #276")
	}
	if strings.Contains(cron, `"collect",`) {
		t.Fatal("k8s RSS CronJob still references the static `worker collect` stub — see issue #276")
	}
}
