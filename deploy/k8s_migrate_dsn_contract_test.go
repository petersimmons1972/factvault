package deploy_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// containersBlockRe matches the runtime "containers:" key at any
// indentation level, on its own line — the different manifest kinds
// (Deployment, Job, CronJob) nest their PodSpec at different depths.
var containersBlockRe = regexp.MustCompile(`(?m)^[ \t]*containers:[ \t]*$`)

// indexOfContainersBlock returns the byte offset of the runtime
// "containers:" key within manifest, or -1 if not found.
func indexOfContainersBlock(manifest string) int {
	loc := containersBlockRe.FindStringIndex(manifest)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// migrateInitContainerManifests are the manifests that embed a
// factvault-migrate initContainer ahead of their main workload container.
var migrateInitContainerManifests = []string{
	"k8s/api-deployment.yaml",
	"k8s/rss-worker-cronjob.yaml",
	"k8s/archive-worker-cronjob.yaml",
	"k8s/extract-worker-cronjob.yaml",
	"k8s/corroborate-worker-cronjob.yaml",
	"k8s/verify-worker-cronjob.yaml",
	"k8s/dossier-worker-cronjob.yaml",
}

// TestK8sMigrateStepsReferenceMigrateDSNSecret asserts that every place
// migrations run (the initContainers embedded in api-deployment.yaml and
// each worker CronJob, plus the standalone migrate-job.yaml Job) pulls in
// the privileged factvault-migrate-credentials Secret, since migrations run
// CREATE EXTENSION / DDL the runtime app_user DSN cannot perform (#272).
func TestK8sMigrateStepsReferenceMigrateDSNSecret(t *testing.T) {
	migrateSecretName := "name: factvault-migrate-credentials"
	appSecretName := "name: factvault-db-credentials"

	for _, path := range append([]string{"k8s/migrate-job.yaml"}, migrateInitContainerManifests...) {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is static test fixture names
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		manifest := string(data)
		if !strings.Contains(manifest, migrateSecretName) {
			t.Errorf("%s is missing the migration DSN secretRef", path)
		}
		if !strings.Contains(manifest, appSecretName) {
			t.Errorf("%s is missing the app-user DSN secretRef", path)
		}
	}
}

// TestK8sRuntimeContainersDoNotReferenceMigrateDSNSecret asserts that the
// runtime containers (the API server and each worker) only receive the
// app-user DSN, never the privileged migration DSN (#272 acceptance
// criteria: "Runtime pods receive only app-user DSN").
//
// It parses out just the containers: block of each manifest (as opposed to
// initContainers:) so the assertion targets the right container.
func TestK8sRuntimeContainersDoNotReferenceMigrateDSNSecret(t *testing.T) {
	migrateSecretName := "factvault-migrate-credentials"
	appSecretName := "factvault-db-credentials"

	for _, path := range migrateInitContainerManifests {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is static test fixture names
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		manifest := string(data)

		idx := indexOfContainersBlock(manifest)
		if idx == -1 {
			t.Fatalf("%s: could not locate the runtime containers: block", path)
		}
		runtimeSection := manifest[idx:]

		if strings.Contains(runtimeSection, migrateSecretName) {
			t.Errorf("%s: runtime containers: block references the migration DSN secret; "+
				"only the migrate initContainer/Job should", path)
		}
		if !strings.Contains(runtimeSection, appSecretName) {
			t.Errorf("%s: runtime containers: block is missing the app-user DSN secretRef", path)
		}
	}
}

// TestK8sMigrateDSNSecretExampleDocumented asserts the operator example
// Secret documents both the runtime and migration DSNs so operators know to
// provision both Secrets before deploying (#272).
func TestK8sMigrateDSNSecretExampleDocumented(t *testing.T) {
	data, err := os.ReadFile("k8s/examples/secret.example.yaml")
	if err != nil {
		t.Fatalf("read secret example: %v", err)
	}
	example := string(data)

	for _, required := range []string{
		"name: factvault-db-credentials",
		"name: factvault-migrate-credentials",
		"FACTVAULT_DATABASE_URL",
		"FACTVAULT_MIGRATE_DATABASE_URL",
	} {
		if !strings.Contains(example, required) {
			t.Errorf("secret example is missing %q", required)
		}
	}
}

// TestOperatorGuideDocumentsMigrateDSN asserts the operator guide tells
// operators how to provision the migration DSN Secret (#272).
func TestOperatorGuideDocumentsMigrateDSN(t *testing.T) {
	data, err := os.ReadFile("../docs/operator-guide.md")
	if err != nil {
		t.Fatalf("read operator guide: %v", err)
	}
	guide := string(data)

	for _, required := range []string{
		"factvault-migrate-credentials",
		"FACTVAULT_MIGRATE_DATABASE_URL",
	} {
		if !strings.Contains(guide, required) {
			t.Errorf("operator guide is missing %q", required)
		}
	}
}
