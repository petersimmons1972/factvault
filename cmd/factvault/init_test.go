package main

import (
	"bytes"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
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

// TestInitCmd_KeygenOutputMessages verifies that the init command prints the
// "Generating" header only when keys are actually created, and prints a
// "skipping keygen" message (without "Generating") when keys already exist.
//
// Because the full init command requires a live DB, we use a deliberately
// invalid DSN so the command fails at the doctor step — but keygen output is
// emitted before that, so we can still assert on it.
func TestInitCmd_KeygenOutputMessages(t *testing.T) {
	t.Run("first_run_prints_generating", func(t *testing.T) {
		dir := t.TempDir()
		cmd := newInitCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		// Invalid DSN — command will fail at doctor, but keygen runs first.
		_ = cmd.Flags().Set("dsn", "postgres://invalid:5432/nodb")
		_ = cmd.Flags().Set("key-dir", dir)
		_ = cmd.Flags().Set("skip-example", "true")
		// RunE will error; we only care about what was printed before the error.
		_ = cmd.Execute()
		out := buf.String()
		if !strings.Contains(out, "Generating JWT keys") {
			t.Errorf("first run: expected output to contain 'Generating JWT keys', got:\n%s", out)
		}
	})

	t.Run("second_run_skips_keygen_without_generating_header", func(t *testing.T) {
		dir := t.TempDir()
		// Pre-populate key files so initKeys is a no-op.
		cmd1 := newInitCmd()
		cmd1.SetOut(&bytes.Buffer{})
		_ = cmd1.Flags().Set("dsn", "postgres://invalid:5432/nodb")
		_ = cmd1.Flags().Set("key-dir", dir)
		_ = cmd1.Flags().Set("skip-example", "true")
		_ = cmd1.Execute()
		// Keys should now exist; confirm before second run.
		privPath := filepath.Join(dir, "private.pem")
		pubPath := filepath.Join(dir, "public.pem")
		if !fileNonEmpty(privPath) || !fileNonEmpty(pubPath) {
			t.Fatal("keys not created on first run; cannot test skip path")
		}

		// Second run — should NOT print "Generating", SHOULD print skip message.
		cmd2 := newInitCmd()
		var buf bytes.Buffer
		cmd2.SetOut(&buf)
		_ = cmd2.Flags().Set("dsn", "postgres://invalid:5432/nodb")
		_ = cmd2.Flags().Set("key-dir", dir)
		_ = cmd2.Flags().Set("skip-example", "true")
		_ = cmd2.Execute()
		out := buf.String()
		if strings.Contains(out, "Generating JWT keys") {
			t.Errorf("second run: output must NOT contain 'Generating JWT keys' on skip path, got:\n%s", out)
		}
		if !strings.Contains(out, "skipping keygen") {
			t.Errorf("second run: expected output to contain 'skipping keygen', got:\n%s", out)
		}
	})
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
