package main

import (
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// TestInitKeys_WritesKeyFiles verifies that initKeys writes non-empty private
// and public PEM files to the key directory.
func TestInitKeys_WritesKeyFiles(t *testing.T) {
	dir := t.TempDir()
	if err := initKeys(dir); err != nil {
		t.Fatalf("initKeys: %v", err)
	}
	privPath := filepath.Join(dir, "private.pem")
	pubPath := filepath.Join(dir, "public.pem")
	if !fileNonEmpty(privPath) {
		t.Fatalf("private.pem is empty or missing")
	}
	if !fileNonEmpty(pubPath) {
		t.Fatalf("public.pem is empty or missing")
	}
	// Validate PEM blocks are decodable.
	privData, _ := os.ReadFile(privPath)
	block, _ := pem.Decode(privData)
	if block == nil {
		t.Fatal("private.pem: invalid PEM")
	}
	pubData, _ := os.ReadFile(pubPath)
	block, _ = pem.Decode(pubData)
	if block == nil {
		t.Fatal("public.pem: invalid PEM")
	}
}

// TestInitKeys_IsIdempotent verifies that calling initKeys twice does not
// overwrite existing non-empty key files.
func TestInitKeys_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := initKeys(dir); err != nil {
		t.Fatalf("first initKeys: %v", err)
	}
	privPath := filepath.Join(dir, "private.pem")
	first, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatalf("read private.pem: %v", err)
	}
	// Second call must be a no-op.
	if err := initKeys(dir); err != nil {
		t.Fatalf("second initKeys: %v", err)
	}
	second, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatalf("read private.pem after second call: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("initKeys is not idempotent: private.pem changed on second call")
	}
}

// TestFileNonEmpty verifies the fileNonEmpty helper.
func TestFileNonEmpty(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "absent.pem")
	if fileNonEmpty(absent) {
		t.Fatal("expected false for absent file")
	}
	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if fileNonEmpty(empty) {
		t.Fatal("expected false for empty file")
	}
	nonEmpty := filepath.Join(dir, "nonempty.pem")
	if err := os.WriteFile(nonEmpty, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileNonEmpty(nonEmpty) {
		t.Fatal("expected true for non-empty file")
	}
}
