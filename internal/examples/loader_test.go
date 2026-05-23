package examples

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExample(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(dir, "fixtures"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "properties.yaml"), "- slug: founded_in\n  label: Founded in\n  value_type: date\n")
	mustWrite(t, filepath.Join(dir, "seeds.yaml"), "- ext_id: acme\n  label: Acme\n  type_uri: https://schema.org/Organization\n")
	mustWrite(t, filepath.Join(dir, "fixtures", "source.html"), "<html>source</html>")
	ex, err := Load(root, "demo")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ex.Properties) != 1 || len(ex.Seeds) != 1 || len(ex.Fixtures) != 1 {
		t.Fatalf("unexpected example: %+v", ex)
	}
	names, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 1 || names[0] != "demo" {
		t.Fatalf("names=%v", names)
	}
}

func mustWrite(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
