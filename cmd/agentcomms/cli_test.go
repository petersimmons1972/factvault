package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runCLI builds a fresh root command tree and executes args, returning stdout.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "agentcomms"}
	root.PersistentFlags().String("root", ".agent-comms", "")
	root.AddCommand(newSendCmd(), newReadCmd(), newAckCmd(),
		newHeartbeatCmd(), newArchiveCmd(), newHealthCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	// Capture stdout (cobra writes the actual command output to os.Stdout in
	// our commands; reroute it).
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	execErr := root.Execute()
	if err := w.Close(); err != nil {
		t.Fatalf("w.Close: %v", err)
	}
	os.Stdout = origStdout
	data, readErr := readAll(r)
	if readErr != nil && readErr.Error() != "EOF" {
		t.Fatalf("readAll: %v", readErr)
	}
	return data, execErr
}

func readAll(r *os.File) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String(), nil
}

func TestCLISendReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLI(t, "--root", dir, "send",
		"--from", "claude", "--to", "codex",
		"--kind", "nudge", "--body", "hello")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	id := strings.TrimSpace(out)
	if len(id) != 26 {
		t.Fatalf("send id length=%d: %q", len(id), id)
	}
	out, err = runCLI(t, "--root", dir, "read", "--inbox", "codex")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(out), &msgs); err != nil {
		t.Fatalf("read JSON: %v\n%s", err, out)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0]["id"] != id {
		t.Fatalf("id mismatch: %v vs %s", msgs[0]["id"], id)
	}
}

func TestCLIHealthJSON(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLI(t, "--root", dir, "health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if raw["schema_valid"] != true {
		t.Fatalf("schema_valid=%v want true", raw["schema_valid"])
	}
}

func TestCLIArchive(t *testing.T) {
	dir := t.TempDir()
	idOut, err := runCLI(t, "--root", dir, "send",
		"--from", "claude", "--to", "codex",
		"--kind", "nudge", "--body", "x")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(idOut)
	if _, err := runCLI(t, "--root", dir, "archive", id, "--reason", "test"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	processed, err := os.ReadDir(filepath.Join(dir, "processed"))
	if err != nil {
		t.Fatalf("ReadDir processed: %v", err)
	}
	if len(processed) != 1 {
		t.Fatalf("processed count=%d", len(processed))
	}
}

func TestCLISendRequiresKind(t *testing.T) {
	dir := t.TempDir()
	_, err := runCLI(t, "--root", dir, "send",
		"--from", "claude", "--to", "codex", "--body", "x")
	if err == nil {
		t.Fatal("expected error when --kind missing")
	}
}

func TestCLISendRejectsUnexpectedPositionalArgs(t *testing.T) {
	dir := t.TempDir()
	_, err := runCLI(t, "--root", dir, "send", "extra",
		"--from", "claude", "--to", "codex", "--kind", "nudge", "--body", "hello")
	if err == nil {
		t.Fatal("expected positional arg error")
	}
}

func TestCLIReadUnreadNotImplemented(t *testing.T) {
	dir := t.TempDir()
	_, err := runCLI(t, "--root", dir, "read", "--inbox", "codex", "--unread")
	if err == nil {
		t.Fatal("expected not implemented error")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("unexpected error: %v", err)
	}
}
