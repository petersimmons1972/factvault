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
}
