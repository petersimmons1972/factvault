package deploy_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// TestK8sConfigMapYAMLParsed parses configmap.yaml structurally (not via string search)
// and asserts:
//   - The data section contains every expected configuration key.
//   - No value in data exceeds 200 characters, guarding against accidentally inlined secrets.
func TestK8sConfigMapYAMLParsed(t *testing.T) {
	raw, err := os.ReadFile("k8s/configmap.yaml")
	if err != nil {
		t.Fatalf("read configmap.yaml: %v", err)
	}

	// configMap mirrors the relevant fields of a Kubernetes ConfigMap manifest.
	var configMap struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal(raw, &configMap); err != nil {
		t.Fatalf("parse configmap.yaml as YAML: %v", err)
	}

	if configMap.Kind != "ConfigMap" {
		t.Fatalf("expected kind=ConfigMap, got %q", configMap.Kind)
	}
	if configMap.Metadata.Name != "factvault-config" {
		t.Fatalf("expected metadata.name=factvault-config, got %q", configMap.Metadata.Name)
	}

	// Every key that non-credential workloads depend on must be present.
	requiredKeys := []string{
		"FACTVAULT_EMBEDDER_URL",
		"FACTVAULT_LLM_URL",
		"FACTVAULT_WAYBACK_URL",
	}
	for _, key := range requiredKeys {
		if _, ok := configMap.Data[key]; !ok {
			t.Errorf("configmap.yaml data section is missing required key %q", key)
		}
	}

	// No value may exceed 200 characters — inlined secrets are always long strings.
	const maxValueLen = 200
	for key, value := range configMap.Data {
		if len(value) > maxValueLen {
			t.Errorf("configmap.yaml data[%q] length %d exceeds %d — possible inlined secret",
				key, len(value), maxValueLen)
		}
	}
}
