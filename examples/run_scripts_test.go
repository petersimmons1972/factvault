package examples_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	databaseURL = "FACTVAULT_DATABASE_URL"
	tenantID    = "FACTVAULT_DEV_TENANT_ID"
)

func TestRunScriptsResolveRepoRoot(t *testing.T) {
	repoRoot, scripts := exampleScripts(t)
	stubDir := writeFactvaultStub(t)

	for _, script := range scripts {
		exampleDir := filepath.Dir(script)
		exampleName := filepath.Base(exampleDir)
		t.Run(exampleName, func(t *testing.T) {
			cmd := exec.Command("./run.sh")
			cmd.Dir = exampleDir
			cmd.Env = commandEnv(
				"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				databaseURL+"=postgres://stub/unused",
				tenantID+"=00000000-0000-0000-0000-000000000000",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run.sh failed: %v\n%s", err, output)
			}

			want := strings.Join([]string{
				"STUB_INVOKED",
				"example",
				"load",
				exampleName,
				"--root",
				filepath.Join(repoRoot, "examples"),
			}, "\n") + "\n"
			if string(output) != want {
				t.Fatalf("factvault arguments:\n%s\nwant:\n%s", output, want)
			}
		})
	}
}

func TestRunScriptsRequireEnvironment(t *testing.T) {
	_, scripts := exampleScripts(t)
	stubDir := writeFactvaultStub(t)
	tests := []struct {
		name       string
		missingEnv string
		setEnv     string
		emptyEnv   string
		wantError  string
	}{
		{
			name:       "missing database URL",
			missingEnv: databaseURL,
			setEnv:     tenantID + "=00000000-0000-0000-0000-000000000000",
			wantError:  databaseURL + " is required",
		},
		{
			name:       "missing tenant ID",
			missingEnv: tenantID,
			setEnv:     databaseURL + "=postgres://stub/unused",
			wantError:  tenantID + " is required",
		},
		{
			name:       "empty database URL",
			missingEnv: databaseURL,
			setEnv:     tenantID + "=00000000-0000-0000-0000-000000000000",
			emptyEnv:   databaseURL + "=",
			wantError:  databaseURL + " is required",
		},
		{
			name:       "empty tenant ID",
			missingEnv: tenantID,
			setEnv:     databaseURL + "=postgres://stub/unused",
			emptyEnv:   tenantID + "=",
			wantError:  tenantID + " is required",
		},
	}

	for _, script := range scripts {
		exampleDir := filepath.Dir(script)
		exampleName := filepath.Base(exampleDir)
		for _, tt := range tests {
			t.Run(exampleName+"/"+tt.name, func(t *testing.T) {
				cmd := exec.Command("./run.sh")
				cmd.Dir = exampleDir
				overrides := []string{
					"PATH=" + stubDir + string(os.PathListSeparator) + os.Getenv("PATH"),
					tt.setEnv,
				}
				if tt.emptyEnv != "" {
					overrides = append(overrides, tt.emptyEnv)
				}
				cmd.Env = commandEnv(overrides...)
				output, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatalf("run.sh succeeded without %s; output: %s", tt.missingEnv, output)
				}
				if !strings.Contains(string(output), tt.wantError) {
					t.Fatalf("run.sh output %q does not contain %q", output, tt.wantError)
				}
				if strings.Contains(string(output), "STUB_INVOKED") {
					t.Fatal("factvault stub was invoked")
				}
			})
		}
	}
}

func TestExampleReadmesUseCanonicalLocalDatabaseURL(t *testing.T) {
	_, scripts := exampleScripts(t)
	const canonical = "postgres://app_user:dev_only_local_password@localhost:5432/factvault?sslmode=disable" //nolint:gosec // documented local-only development fixture, not a credential
	for _, script := range scripts {
		readme := filepath.Join(filepath.Dir(script), "README.md")
		data, err := os.ReadFile(readme) //nolint:gosec // path comes from the repository-controlled examples/*/run.sh glob
		if err != nil {
			t.Fatalf("read %s: %v", readme, err)
		}
		if !strings.Contains(string(data), canonical) {
			t.Errorf("%s does not use canonical local database URL", readme)
		}
	}
}

func exampleScripts(t *testing.T) (string, []string) {
	t.Helper()

	examplesDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve examples directory: %v", err)
	}
	scripts, err := filepath.Glob(filepath.Join(examplesDir, "*", "run.sh"))
	if err != nil {
		t.Fatalf("find example scripts: %v", err)
	}
	if len(scripts) != 4 {
		t.Fatalf("found %d example scripts, want 4", len(scripts))
	}
	return filepath.Dir(examplesDir), scripts
}

func writeFactvaultStub(t *testing.T) string {
	t.Helper()

	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "factvault")
	stub := "#!/usr/bin/env sh\nprintf 'STUB_INVOKED\\n'\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(stubPath, []byte(stub), 0o600); err != nil {
		t.Fatalf("write factvault stub: %v", err)
	}
	if err := os.Chmod(stubPath, 0o700); err != nil { //nolint:gosec // reason: test-only command stub must be executable
		t.Fatalf("make factvault stub executable: %v", err)
	}
	return stubDir
}

func commandEnv(overrides ...string) []string {
	keys := make([]string, 0, len(overrides)+2)
	for _, override := range overrides {
		key, _, _ := strings.Cut(override, "=")
		keys = append(keys, key)
	}
	keys = append(keys, databaseURL, tenantID)

	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return slices.Contains(keys, key)
	})
	return append(env, overrides...)
}
