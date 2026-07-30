package testdb

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDockerRunArgsNamesContainerAndVolume(t *testing.T) {
	got := dockerRunArgs("factvault-testdb-123", "factvault-testdb-123", "pgvector/pgvector:pg16")
	wantParts := [][]string{
		{"--rm"},
		{"--name", "factvault-testdb-123"},
		{"--mount", "type=volume,source=factvault-testdb-123,target=/var/lib/postgresql/data"},
	}
	for _, part := range wantParts {
		if !containsSequence(got, part) {
			t.Fatalf("dockerRunArgs() = %q, missing sequence %q", got, part)
		}
	}
}

func TestDockerCleanupCommandsRemoveContainerVolumesAndNamedVolume(t *testing.T) {
	got := dockerCleanupCommands("factvault-testdb-123", "factvault-testdb-123")
	want := [][]string{
		{"rm", "-f", "-v", "factvault-testdb-123"},
		{"volume", "rm", "-f", "factvault-testdb-123"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dockerCleanupCommands() = %q, want %q", got, want)
	}
}

func TestFindTestDBLeaksUsesFakeLister(t *testing.T) {
	lister := fakeResourceLister{
		containers: []string{"factvault-testdb-1", "unrelated"},
		volumes:    []string{"factvault-testdb-2", "other"},
	}

	got, err := findTestDBLeaks(lister)
	if err != nil {
		t.Fatalf("findTestDBLeaks: %v", err)
	}
	want := []string{"container factvault-testdb-1", "volume factvault-testdb-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findTestDBLeaks() = %q, want %q", got, want)
	}
}

// TestNoTestDBLeaks is run as a separate post-suite CI command. Keeping the
// gate separate avoids mistaking containers from concurrently running package
// tests for leaks.
func TestNoTestDBLeaks(t *testing.T) {
	if os.Getenv("TESTDB_LEAK_GATE") != "1" {
		t.Skip("set TESTDB_LEAK_GATE=1 after the suite to run the Docker leak gate")
	}
	leaks, err := findTestDBLeaks(dockerResourceLister{})
	if err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	if len(leaks) != 0 {
		t.Fatalf("testdb resources survived the suite (SIGKILL can leave residue): %s", strings.Join(leaks, ", "))
	}
}

type fakeResourceLister struct {
	containers []string
	volumes    []string
}

func (f fakeResourceLister) listContainers() ([]string, error) { return f.containers, nil }
func (f fakeResourceLister) listVolumes() ([]string, error)    { return f.volumes, nil }

func containsSequence(values, sequence []string) bool {
	for i := 0; i+len(sequence) <= len(values); i++ {
		if reflect.DeepEqual(values[i:i+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
