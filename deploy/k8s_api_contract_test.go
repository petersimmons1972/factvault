package deploy_test

import (
	"os"
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

func TestK8sConfigMapContainsNoCredentials(t *testing.T) {
	data, err := os.ReadFile("k8s/configmap.yaml")
	if err != nil {
		t.Fatalf("read configmap: %v", err)
	}
	cm := string(data)
	for _, forbidden := range []string{
		"DATABASE_URL: postgres://",
		"changeme",
		"password",
	} {
		if strings.Contains(strings.ToLower(cm), strings.ToLower(forbidden)) {
			t.Fatalf("configmap.yaml contains forbidden credential pattern %q", forbidden)
		}
	}
}
