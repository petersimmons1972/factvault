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
		data, err := os.ReadFile(path)
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
