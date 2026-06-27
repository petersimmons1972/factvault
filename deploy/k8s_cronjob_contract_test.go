package deploy_test

import (
	"os"
	"strings"
	"testing"
)

func TestK8sWorkerCronJobsIncludeTenantArg(t *testing.T) {
	files := []string{
		"k8s/collect-worker-cronjob.yaml",
		"k8s/archive-worker-cronjob.yaml",
		"k8s/extract-worker-cronjob.yaml",
		"k8s/corroborate-worker-cronjob.yaml",
		"k8s/verify-worker-cronjob.yaml",
		"k8s/dossier-worker-cronjob.yaml",
	}

	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is static test fixture names
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		manifest := string(data)
		if !strings.Contains(manifest, "\"--tenant\"") {
			t.Fatalf("%s is missing --tenant worker argument", path)
		}
		if !strings.Contains(manifest, "\"$(FACTVAULT_TENANT_ID)\"") {
			t.Fatalf("%s is missing FACTVAULT_TENANT_ID tenant value", path)
		}
	}
}

func TestK8sWorkerCronJobsIncludeMigrationInitContainer(t *testing.T) {
	files := []string{
		"k8s/collect-worker-cronjob.yaml",
		"k8s/archive-worker-cronjob.yaml",
		"k8s/extract-worker-cronjob.yaml",
		"k8s/corroborate-worker-cronjob.yaml",
		"k8s/verify-worker-cronjob.yaml",
		"k8s/dossier-worker-cronjob.yaml",
	}

	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is static test fixture names
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		manifest := string(data)
		if !strings.Contains(manifest, "initContainers:") {
			t.Fatalf("%s is missing initContainers", path)
		}
		if !strings.Contains(manifest, "name: factvault-migrate") {
			t.Fatalf("%s is missing factvault-migrate init container", path)
		}
		if !strings.Contains(manifest, "args: [\"factvault\", \"migrate\"]") {
			t.Fatalf("%s is missing migrate init args", path)
		}
		if !strings.Contains(manifest, "name: factvault-config") {
			t.Fatalf("%s is missing factvault-config env source", path)
		}
		// Credentials come from the Secret, not the ConfigMap.
		if !strings.Contains(manifest, "name: factvault-db-credentials") {
			t.Fatalf("%s is missing factvault-db-credentials secret ref", path)
		}
	}
}

func TestK8sConfigMapDefinesTenantID(t *testing.T) {
	data, err := os.ReadFile("k8s/configmap.yaml")
	if err != nil {
		t.Fatalf("read configmap.yaml: %v", err)
	}
	if !strings.Contains(string(data), "FACTVAULT_TENANT_ID:") {
		t.Fatal("configmap.yaml must define FACTVAULT_TENANT_ID for worker CronJobs")
	}
}
