package deploy_test

import (
	"os"
	"strings"
	"testing"
)

// TestK8sAPIDeploymentMountsWritableAuthVolume asserts the factvault-api
// Deployment's "factvault" container (the only container that runs
// "factvault api", the command deploy/docker/entrypoint.sh bootstraps JWT
// key material for) mounts a writable emptyDir volume at
// /var/lib/factvault/auth despite readOnlyRootFilesystem: true (#271).
func TestK8sAPIDeploymentMountsWritableAuthVolume(t *testing.T) {
	data, err := os.ReadFile("k8s/api-deployment.yaml")
	if err != nil {
		t.Fatalf("read api-deployment.yaml: %v", err)
	}
	manifest := string(data)

	for _, required := range []string{
		"volumes:",
		"name: auth",
		"emptyDir: {}",
		"mountPath: /var/lib/factvault/auth",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("api-deployment.yaml is missing %q", required)
		}
	}

	idx := strings.Index(manifest, "\n      containers:")
	if idx == -1 {
		t.Fatalf("could not locate the containers: block")
	}
	containerSection := manifest[idx:]
	if !strings.Contains(containerSection, "volumeMounts:") {
		t.Fatalf("api-deployment.yaml containers: block is missing volumeMounts:")
	}
}

// TestK8sMigrateAndWorkerStepsHaveNoAuthVolume asserts the migrate
// initContainers/Job and worker containers do NOT need an auth volume:
// deploy/docker/entrypoint.sh only bootstraps JWT material for "factvault
// api", so those pods run cleanly on a read-only root filesystem without any
// writable mount (#271). This guards against accidentally widening the
// volume workaround to containers that do not need it.
func TestK8sMigrateAndWorkerStepsHaveNoAuthVolume(t *testing.T) {
	files := []string{
		"k8s/migrate-job.yaml",
		"k8s/rss-worker-cronjob.yaml",
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
		if strings.Contains(manifest, "mountPath: /var/lib/factvault/auth") {
			t.Errorf("%s mounts an auth volume it should not need — only the "+
				"factvault-api Deployment's api container bootstraps auth material", path)
		}
	}
}
