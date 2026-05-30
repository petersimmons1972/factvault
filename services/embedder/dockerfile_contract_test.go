package embedder

import (
	"os"
	"strings"
	"testing"
)

func TestDockerfileDevPackageCleanupDoesNotSwallowApkDelFailures(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	dockerfile := string(data)
	if strings.Contains(dockerfile, "apk del python-3.12-dev py3.12-pip || true") {
		t.Fatal("Dockerfile cleanup must not use 'apk del ... || true', which hides apk del failures")
	}

	wantInfo := "apk info python-3.12-dev >/dev/null 2>&1"
	wantDel := "apk del python-3.12-dev py3.12-pip"
	if !strings.Contains(dockerfile, wantInfo) || !strings.Contains(dockerfile, wantDel) {
		t.Fatalf("Dockerfile cleanup must guard %q before running %q", wantInfo, wantDel)
	}

	if strings.Contains(dockerfile, wantInfo+" && "+wantDel) {
		t.Fatal("Dockerfile cleanup must not let a missing package make the RUN instruction fail")
	}
}
