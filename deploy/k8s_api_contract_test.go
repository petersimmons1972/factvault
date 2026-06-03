package deploy_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestK8sAPIIncludesMigrationInitContainer(t *testing.T) {
	data, err := os.ReadFile("k8s/api-deployment.yaml")
	if err != nil {
		t.Fatalf("read api deployment: %v", err)
	}
	manifest := string(data)
	if !strings.Contains(manifest, "initContainers:") {
		t.Fatalf("api deployment is missing initContainers")
	}
	if !strings.Contains(manifest, "name: factvault-migrate") {
		t.Fatalf("api deployment is missing factvault-migrate init container")
	}
	if !strings.Contains(manifest, "args: [\"factvault\", \"migrate\"]") {
		t.Fatalf("api deployment is missing migrate init args")
	}
	if !strings.Contains(manifest, "name: factvault-config") {
		t.Fatalf("api deployment is missing factvault-config env source")
	}
	// Credentials must come from the Secret, not the ConfigMap.
	if !strings.Contains(manifest, "name: factvault-db-credentials") {
		t.Fatalf("api deployment is missing factvault-db-credentials secret ref")
	}
}

// credentialValueRe matches values that look like actual secrets:
//   - base64-encoded blobs >= 20 chars (e.g. bcrypt hashes, API tokens, JWT secrets)
//   - lowercase hex strings >= 32 chars (e.g. MD5/SHA digests used as passwords)
//
// A key *name* that contains "password" (e.g. POSTGRES_PASSWORD_FILE) is fine.
// A literal credential value is not.
var credentialValueRe = regexp.MustCompile(`(?m):\s+([A-Za-z0-9+/]{20,}=*|[a-f0-9]{32,})\s*$`)

func TestK8sConfigMapContainsNoCredentials(t *testing.T) {
	data, err := os.ReadFile("k8s/configmap.yaml")
	if err != nil {
		t.Fatalf("read configmap: %v", err)
	}
	cm := string(data)

	// Explicit forbidden patterns that are always wrong in a ConfigMap.
	for _, forbidden := range []string{
		"DATABASE_URL: postgres://",
		"changeme",
	} {
		if strings.Contains(strings.ToLower(cm), strings.ToLower(forbidden)) {
			t.Fatalf("configmap.yaml contains forbidden credential pattern %q", forbidden)
		}
	}

	// Reject any value that looks like a raw secret (base64 blob or hex digest).
	// Key names referencing secrets (e.g. POSTGRES_PASSWORD_FILE) are allowed.
	if m := credentialValueRe.FindString(cm); m != "" {
		t.Fatalf("configmap.yaml contains a value that looks like a raw credential: %q", strings.TrimSpace(m))
	}
}
