package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEntrypointAuthBootstrap exercises deploy/docker/entrypoint.sh directly
// against a stub "factvault" binary, simulating the readOnlyRootFilesystem
// Kubernetes pods run under (#271). The entrypoint must only attempt to
// create/write the JWT keypair under $FACTVAULT_AUTH_DIR when the wrapped
// command is "factvault api" — "factvault migrate" and "factvault worker
// <stage>" must succeed unchanged even when the auth directory is read-only,
// since those commands never need auth material.
func TestEntrypointAuthBootstrap(t *testing.T) {
	entrypoint, err := filepath.Abs("docker/entrypoint.sh")
	if err != nil {
		t.Fatalf("resolve entrypoint path: %v", err)
	}
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("entrypoint.sh not found: %v", err)
	}

	stubDir := t.TempDir()
	installStubFactvault(t, stubDir)

	tests := []struct {
		name           string
		args           []string
		bootstrapEnv   string // "" (unset), "0", or "1"
		readOnlyAuth   bool
		wantErr        bool
		wantAuthWrites bool
	}{
		{
			name:         "migrate command skips bootstrap on read-only auth dir",
			args:         []string{"factvault", "migrate"},
			readOnlyAuth: true,
			wantErr:      false,
		},
		{
			name:         "worker command skips bootstrap on read-only auth dir",
			args:         []string{"factvault", "worker", "extract", "--tenant", "t1"},
			readOnlyAuth: true,
			wantErr:      false,
		},
		{
			name:           "api command bootstraps and requires a writable auth dir",
			args:           []string{"factvault", "api", "--addr", ":8080"},
			readOnlyAuth:   false,
			wantErr:        false,
			wantAuthWrites: true,
		},
		{
			name:         "api command fails fast on read-only auth dir without a mounted volume",
			args:         []string{"factvault", "api", "--addr", ":8080"},
			readOnlyAuth: true,
			wantErr:      true,
		},
		{
			name:         "explicit FACTVAULT_BOOTSTRAP_AUTH=1 overrides command auto-detection",
			args:         []string{"factvault", "migrate"},
			bootstrapEnv: "1",
			readOnlyAuth: true,
			wantErr:      true,
		},
		{
			name:           "explicit FACTVAULT_BOOTSTRAP_AUTH=0 skips bootstrap even for api",
			args:           []string{"factvault", "api", "--addr", ":8080"},
			bootstrapEnv:   "0",
			readOnlyAuth:   false,
			wantErr:        false,
			wantAuthWrites: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			authDir := t.TempDir()
			if tc.readOnlyAuth {
				// Simulate readOnlyRootFilesystem: the parent of authDir
				// cannot be written to, so mkdir -p on a not-yet-existing
				// auth dir fails exactly like it would on a K8s pod without
				// a writable volume mounted at that path.
				roParent := filepath.Join(authDir, "ro-parent")
				if err := os.Mkdir(roParent, 0o555); err != nil {
					t.Fatalf("create read-only parent: %v", err)
				}
				authDir = filepath.Join(roParent, "auth")
			}

			cmd := exec.Command("sh", append([]string{entrypoint}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"PATH="+stubDir+":"+os.Getenv("PATH"),
				"FACTVAULT_AUTH_DIR="+authDir,
			)
			if tc.bootstrapEnv != "" {
				cmd.Env = append(cmd.Env, "FACTVAULT_BOOTSTRAP_AUTH="+tc.bootstrapEnv)
			}

			out, runErr := cmd.CombinedOutput()
			if tc.wantErr && runErr == nil {
				t.Fatalf("expected entrypoint to fail, but it succeeded; output:\n%s", out)
			}
			if !tc.wantErr && runErr != nil {
				t.Fatalf("expected entrypoint to succeed, got error %v; output:\n%s", runErr, out)
			}

			pub := filepath.Join(authDir, "factvault-jwt-public.pem")
			priv := filepath.Join(authDir, "factvault-jwt-private.pem")
			_, pubErr := os.Stat(pub)
			_, privErr := os.Stat(priv)
			wroteAuth := pubErr == nil && privErr == nil

			if tc.wantAuthWrites && !wroteAuth {
				t.Fatalf("expected JWT key files to be written under %s, they were not; output:\n%s", authDir, out)
			}
			if !tc.wantAuthWrites && !tc.wantErr && wroteAuth {
				t.Fatalf("did not expect JWT key files under %s, but they were written; output:\n%s", authDir, out)
			}
		})
	}
}

// installStubFactvault writes a stub "factvault" script into dir that
// stands in for the real binary: "factvault auth keys" prints a fake
// RSA/PEM pair in the format entrypoint.sh's awk parser expects, and every
// other invocation ("migrate", "api ...", "worker ...") just exits 0 so the
// entrypoint's final `exec "$@"` succeeds without needing a real database
// or HTTP listener.
func installStubFactvault(t *testing.T, dir string) {
	t.Helper()
	script := `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "keys" ]; then
	cat <<'PEM'
-----BEGIN RSA PRIVATE KEY-----
ZmFrZS1wcml2YXRlLWtleS1tYXRlcmlhbA==
-----END RSA PRIVATE KEY-----
-----BEGIN PUBLIC KEY-----
ZmFrZS1wdWJsaWMta2V5LW1hdGVyaWFs
-----END PUBLIC KEY-----
PEM
	exit 0
fi
exit 0
`
	path := filepath.Join(dir, "factvault")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // G306: stub binary must be executable
		t.Fatalf("write stub factvault: %v", err)
	}
	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Fatalf("stub script missing shebang")
	}
}
