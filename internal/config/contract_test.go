// Package config_test contains mechanical contract tests that enforce the
// C1/C5/C10 invariants from docs/conventions.md without requiring a database
// or network. These tests are part of the standard `go test ./...` suite.
package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/petersimmons1972/factvault/internal/config"
)

// moduleRoot walks up the directory tree from the current file until it finds
// a go.mod, which marks the module root. Using runtime.Caller makes this
// robust against `go test` being invoked from any working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root (no go.mod found walking up from contract_test.go)")
		}
		dir = parent
	}
}

// goFiles returns all .go files under root, skipping vendor and .git directories.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking module root: %v", err)
	}
	return files
}

// readFile reads a file and returns its content as a string.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test helper reads paths from goFiles()
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// Test1_RegistryEnvVarsAreReadByCode asserts that every registry entry's EnvVar
// appears as a string in at least one .go file under the module root. Known
// violations (vars not yet wired into Go code) are allowlisted with a TODO.
//
// Enforces C1 and C5: every documented env var must be read by the binary.
func Test1_RegistryEnvVarsAreReadByCode(t *testing.T) {
	root := moduleRoot(t)
	files := goFiles(t, root)

	// knownUnwired maps env var names to the reason wiring is deferred.
	// Do NOT add new entries without a GitHub Issue tracking the wiring work.
	// All previously deferred vars are now wired (C1/C5 conformance pass).
	knownUnwired := map[string]string{}

	contents := make(map[string]string, len(files))
	for _, f := range files {
		contents[f] = readFile(t, f)
	}

	violations := 0
	for _, entry := range config.Registry {
		envVar := entry.EnvVar
		if reason, allowed := knownUnwired[envVar]; allowed {
			t.Logf("KNOWN VIOLATION: %s -- TODO wiring phase (%s)", envVar, reason)
			violations++
			continue
		}

		found := false
		for _, content := range contents {
			if strings.Contains(content, envVar) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("registry var %q is not referenced in any .go file under %s -- add to knownUnwired or wire it up", envVar, root)
		}
	}

	if violations > 0 {
		t.Logf("SUMMARY: %d known wiring-phase violation(s) -- tracked TODOs, not test failures", violations)
	}
}

// Test2_NoUndocumentedFACTVAULTVarsInGoSource asserts that every
// FACTVAULT_[A-Z_]+ string found in any .go file is either in the registry or allowlisted.
//
// Enforces C5: no silent env vars that operators can set but that have no effect.
func Test2_NoUndocumentedFACTVAULTVarsInGoSource(t *testing.T) {
	root := moduleRoot(t)
	files := goFiles(t, root)

	// Build a set of all documented names: primary EnvVar and deprecated Alias.
	documented := make(map[string]bool)
	for _, entry := range config.Registry {
		documented[entry.EnvVar] = true
		if entry.Alias != "" {
			documented[entry.Alias] = true
		}
	}

	knownUndocumented := map[string]string{ //nolint:gosec // G101: keys are env var names, not credential values
		"FACTVAULT_TEST_POSTGRES_IMAGE":          "test helper in internal/testdb; not a production config",
		"FACTVAULT_POSTGRES_IMAGE":               "compose contract test inspecting compose YAML; not read by binary",
		"FACTVAULT_TENANT_ID":                    "k8s cronjob contract test inspecting manifest; not a production config",
		"FACTVAULT_WORKER_FAILURE_RETRY_SECONDS": "compose contract test inspecting compose YAML; not read by binary",
		"FACTVAULT_LLM_API_KEY_FILE":             "C9 auto-generated _FILE companion of FACTVAULT_LLM_API_KEY (Secret: true); resolved by config.ResolveSecret, not a separate registry entry",
		"FACTVAULT_MCP_AUTH_TOKEN_FILE":          "C9 auto-generated _FILE companion of FACTVAULT_MCP_AUTH_TOKEN (Secret: true); resolved by config.ResolveSecret, not a separate registry entry",
	}

	// Require the match to end with an uppercase letter, not a trailing underscore.
	// Real env var names by convention never end with "_", so this prevents the
	// regex from matching partial substrings within Go identifiers or comments.
	pattern := regexp.MustCompile(`FACTVAULT_[A-Z][A-Z_]*[A-Z]`)

	found := make(map[string]bool)
	for _, f := range files {
		content := readFile(t, f)
		for _, match := range pattern.FindAllString(content, -1) {
			found[match] = true
		}
	}

	for varName := range found {
		if documented[varName] {
			continue
		}
		if reason, allowed := knownUndocumented[varName]; allowed {
			t.Logf("ALLOWLISTED undocumented var: %s -- %s", varName, reason)
			continue
		}
		t.Errorf("undocumented FACTVAULT_* var %q found in Go source but not in registry -- add to registry or allowlist with justification", varName)
	}
}

// Test3_DeprecatedAliasResolves asserts that ResolveStringWithAlias returns
// isAlias=true when the alias env var is set and the primary is not.
func Test3_DeprecatedAliasResolves(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("llm-base-url", "", "LLM base URL")
	flag := fs.Lookup("llm-base-url")

	// Use CFGTEST_ prefix (not FACTVAULT_) so Test2 does not flag these as undocumented vars.
	const primaryEnv = "CFGTEST_PRIMARY_5a3f"
	const aliasEnv = "CFGTEST_ALIAS_5a3f"
	const wantVal = "http://alias-host:1234"

	t.Setenv(aliasEnv, wantVal)

	val, isAlias, err := config.ResolveStringWithAlias(flag, primaryEnv, aliasEnv, "default", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != wantVal {
		t.Errorf("got value %q, want %q", val, wantVal)
	}
	if !isAlias {
		t.Errorf("isAlias=false, want true when only alias env var is set")
	}
}

// Test4_ResolverPrecedence is a table-driven test for the flag > env > default
// precedence contract defined in C1.
func Test4_ResolverPrecedence(t *testing.T) {
	// CFGTEST_ prefix avoids Test2 flagging this as an undocumented FACTVAULT_ var.
	const envKey = "CFGTEST_RESOLVER_PREC_b9c1"

	makeFlag := func(t *testing.T, value string, changed bool) *pflag.Flag {
		t.Helper()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("myvar", "", "")
		if changed {
			if err := fs.Set("myvar", value); err != nil {
				t.Fatalf("fs.Set: %v", err)
			}
		}
		return fs.Lookup("myvar")
	}

	tests := []struct {
		name        string
		flagVal     string
		flagChanged bool
		envVal      string
		envSet      bool
		defaultVal  string
		required    bool
		wantVal     string
		wantErr     bool
	}{
		{
			name:        "flag.Changed wins over env and default",
			flagVal:     "from-flag",
			flagChanged: true,
			envVal:      "from-env",
			envSet:      true,
			defaultVal:  "from-default",
			wantVal:     "from-flag",
		},
		{
			name:        "env wins over default when flag not changed",
			flagChanged: false,
			envVal:      "from-env",
			envSet:      true,
			defaultVal:  "from-default",
			wantVal:     "from-env",
		},
		{
			name:        "default used when flag not changed and env not set",
			flagChanged: false,
			envSet:      false,
			defaultVal:  "from-default",
			wantVal:     "from-default",
		},
		{
			name:        "empty-string env is used not default",
			flagChanged: false,
			envVal:      "",
			envSet:      true,
			defaultVal:  "from-default",
			wantVal:     "",
		},
		{
			name:        "required=true with no flag or env returns error",
			flagChanged: false,
			envSet:      false,
			required:    true,
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flag := makeFlag(t, tc.flagVal, tc.flagChanged)

			if tc.envSet {
				t.Setenv(envKey, tc.envVal)
			} else {
				os.Unsetenv(envKey) //nolint:errcheck
			}

			got, err := config.ResolveString(flag, envKey, tc.defaultVal, tc.required)

			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (val=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantVal {
				t.Errorf("got %q, want %q", got, tc.wantVal)
			}
		})
	}
}

// Test5_RegistryNoDuplicateEnvVars asserts that no two registry entries share
// the same EnvVar. Duplicate rows indicate a naming collision.
func Test5_RegistryNoDuplicateEnvVars(t *testing.T) {
	seen := make(map[string]int)
	for i, entry := range config.Registry {
		if prev, dup := seen[entry.EnvVar]; dup {
			t.Errorf("duplicate EnvVar %q at registry index %d (first seen at index %d)", entry.EnvVar, i, prev)
		}
		seen[entry.EnvVar] = i
	}
}
