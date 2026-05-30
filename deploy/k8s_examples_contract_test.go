package deploy_test

import (
	"os"
	"strings"
	"testing"
)

func TestK8sPostgresAppUserInitExampleContract(t *testing.T) {
	const examplePath = "k8s/examples/postgres-app-user-init.example.yaml"

	data, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}
	manifest := string(data)

	for _, required := range []string{
		"kind: Job",
		"postgres:16-alpine",
		"name: POSTGRES_SUPERUSER_DATABASE_URL",
		"name: POSTGRES_APP_USER_PASSWORD",
		"name: factvault-postgres-bootstrap",
		"key: POSTGRES_SUPERUSER_DATABASE_URL",
		"key: POSTGRES_APP_USER_PASSWORD",
		"automountServiceAccountToken: false",
		"allowPrivilegeEscalation: false",
		"drop: [\"ALL\"]",
		":sql_verb ROLE app_user WITH LOGIN PASSWORD :'password';",
		"CREATE ROLE app_user WITH LOGIN PASSWORD :'password'",
		"ALTER ROLE app_user WITH LOGIN PASSWORD :'password'",
		"SELECT 1 FROM pg_roles WHERE rolname = 'app_user'",
		"single-quoted heredoc delimiter",
		"psql's :'password' form applies SQL string quoting",
		"run once per cluster",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("%s is missing required text %q", examplePath, required)
		}
	}

	if strings.Contains(manifest, "<<SQL") {
		t.Fatalf("%s uses an unquoted heredoc delimiter", examplePath)
	}
	if !strings.Contains(manifest, "<<-'SQL'") {
		t.Fatalf("%s is missing quoted heredoc delimiter", examplePath)
	}
}

func TestOperatorGuideLinksPostgresAppUserInitExample(t *testing.T) {
	data, err := os.ReadFile("../docs/operator-guide.md")
	if err != nil {
		t.Fatalf("read operator guide: %v", err)
	}

	link := "deploy/k8s/examples/postgres-app-user-init.example.yaml"
	if !strings.Contains(string(data), link) {
		t.Fatalf("operator guide is missing link to %s", link)
	}

	for _, required := range []string{
		"kubectl apply -f /tmp/factvault-postgres-app-user-init.yaml",
		"kubectl wait --for=condition=complete job/factvault-postgres-app-user-init",
		"kubectl logs job/factvault-postgres-app-user-init",
		"kubectl delete job factvault-postgres-app-user-init",
		"kubectl delete secret factvault-postgres-bootstrap",
		"match your Postgres TLS mode",
	} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("operator guide is missing Kubernetes bootstrap guidance %q", required)
		}
	}

	if strings.Contains(string(data), "deploy/k8s/postgres-app-user-init.yaml") {
		t.Fatalf("operator guide should not copy one-shot bootstrap jobs under deploy/k8s/")
	}
}

func TestK8sSecretExampleReferencesBootstrapJob(t *testing.T) {
	data, err := os.ReadFile("k8s/examples/secret.example.yaml")
	if err != nil {
		t.Fatalf("read secret example: %v", err)
	}

	secretExample := string(data)
	if strings.Contains(secretExample, "K8s init container") {
		t.Fatalf("secret example still references a K8s init container instead of the bootstrap Job")
	}
	if !strings.Contains(secretExample, "postgres-app-user-init.example.yaml") {
		t.Fatalf("secret example does not link to the app_user bootstrap Job example")
	}
}
