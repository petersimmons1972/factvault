package docs_test

import (
	"os"
	"strings"
	"testing"
)

func readDoc(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test reads fixed repository paths only
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireContains(t *testing.T, path, want string) {
	t.Helper()
	if !strings.Contains(readDoc(t, path), want) {
		t.Errorf("%s must contain %q", path, want)
	}
}

func requireNotContains(t *testing.T, path, forbidden string) {
	t.Helper()
	if strings.Contains(readDoc(t, path), forbidden) {
		t.Errorf("%s must not contain stale text %q", path, forbidden)
	}
}

func TestOOBEReviewCorrectionsRemainDocumented(t *testing.T) {
	t.Run("first boot writes key files", func(t *testing.T) {
		requireContains(t, "operator-guide.md", "Generate JWT keys and initialize the deployment with `./bin/factvault init --skip-migrate`")
		requireNotContains(t, "operator-guide.md", "Generate JWT keys with `./bin/factvault auth keys`")
	})

	t.Run("pipeline count and confidence implementation", func(t *testing.T) {
		requireContains(t, "../README.md", "## The Seven-Stage Pipeline")
		requireContains(t, "../README.md", "`internal/assembler/confidence.go`")
		requireNotContains(t, "../README.md", "factvault/assembler/confidence.py")
	})

	t.Run("two DSNs are distinct", func(t *testing.T) {
		for _, path := range []string{"../README.md", "getting-started.md"} {
			requireContains(t, path, "FACTVAULT_DATABASE_URL")
			requireContains(t, path, "FACTVAULT_MIGRATE_DATABASE_URL")
		}
	})

	t.Run("CLI environment fallbacks match code", func(t *testing.T) {
		requireContains(t, "reference/cli.md", "| `--addr` | `:8080` | `FACTVAULT_API_ADDR` |")
		requireContains(t, "guides/frontier-models.md", "`FACTVAULT_LLM_BASE_URL`, `FACTVAULT_LLM_URL`")
		requireContains(t, "guides/frontier-models.md", "FACTVAULT_LLM_API_KEY_FILE")
		requireNotContains(t, "guides/frontier-models.md", "--llm-api-key")
		requireNotContains(t, "reference/cli.md", "--llm-api-key")
		requireContains(t, "reference/cli.md", "`FACTVAULT_MIGRATE_DATABASE_URL`, then `FACTVAULT_DATABASE_URL`")
		requireContains(t, "reference/cli.md", "`--skip-migrate`")
	})

	t.Run("embedding input format", func(t *testing.T) {
		requireContains(t, "guides/embedding-population.md", "`title` and the first 2048 bytes of `raw_text`, separated by a newline")
	})

	t.Run("new operator worker guidance", func(t *testing.T) {
		requireContains(t, "getting-started.md", "The initial example dossier is expected to contain no facts")
		requireContains(t, "getting-started.md", "needs a YAML `tenant` to enter the current scheduler")
		requireContains(t, "getting-started.md", "re-export `FACTVAULT_DATABASE_URL`")
		requireContains(t, "getting-started.md", "re-export `FACTVAULT_DEV_TENANT_ID`")
		requireContains(t, "getting-started.md", "--searxng-url \"$FACTVAULT_SEARXNG_URL\"")
	})

	t.Run("external services and current defaults", func(t *testing.T) {
		requireContains(t, "guides/active-acquisition.md", "FACTVAULT_SEARXNG_URL")
		requireContains(t, "reference/cli.md", "`--searxng-url`")
		requireContains(t, "guides/embedding-population.md", "http://localhost:8081")
		requireNotContains(t, "guides/embedding-population.md", "defaults to `http://localhost:8080`")
		requireContains(t, "reference/cli.md", "`FACTVAULT_MCP_AUTH_TOKEN_FILE`")
		requireContains(t, "reference/cli.md", "assembled server-side; client-supplied bundle files and stdin input are not accepted")
		requireNotContains(t, "reference/cli.md", "`--input` | stdin")
	})

	t.Run("historical issue references are linked and accurate", func(t *testing.T) {
		for _, path := range []string{
			"getting-started.md",
			"operator-guide.md",
			"reference/cli.md",
			"guides/rss-ingestion.md",
		} {
			requireContains(t, path, "https://github.com/petersimmons1972/factvault/issues/94")
			requireNotContains(t, path, "TODO #94")
		}
	})

	t.Run("acquisition guardrail explains failure mode", func(t *testing.T) {
		requireContains(t, "guides/active-acquisition.md", "If acquisition could write to the truth layer")
	})
}
