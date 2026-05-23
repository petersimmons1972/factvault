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
		newHeartbeatCmd(), newClaimCmd(), newQuestionCmd(), newBlockCmd(), newHandoffCmd(),
		newCapabilityCmd(), newLessonsCmd(), newArchiveCmd(), newHealthCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	// Capture stdout (cobra writes the actual command output to os.Stdout in
	// our commands; reroute it).
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := root.Execute()
	_ = w.Close()
	os.Stdout = origStdout
	data, _ := readAll(r)
	return data, err
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
	processed, _ := os.ReadDir(filepath.Join(dir, "processed"))
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

func TestCLIReadUnreadAdvancesCursor(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := runCLI(t, "--root", dir, "send",
			"--from", "claude", "--to", "codex",
			"--kind", "nudge", "--body", "hello"); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	out, err := runCLI(t, "--root", dir, "read", "--inbox", "codex", "--unread")
	if err != nil {
		t.Fatalf("read unread: %v", err)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(out), &msgs); err != nil {
		t.Fatalf("decode unread: %v\n%s", err, out)
	}
	if len(msgs) != 2 {
		t.Fatalf("len=%d want 2", len(msgs))
	}
	cursorPath := filepath.Join(dir, "cursors", "codex.json")
	if _, err := os.Stat(cursorPath); err != nil {
		t.Fatalf("cursor missing: %v", err)
	}
	out, err = runCLI(t, "--root", dir, "read", "--inbox", "codex", "--unread")
	if err != nil {
		t.Fatalf("second unread read: %v", err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("expected no unread messages, got %s", out)
	}
}

func TestCLIQuestionBlockHandoff(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, "--root", dir, "question", "--from", "codex", "--body", "clarify scope"); err != nil {
		t.Fatalf("question: %v", err)
	}
	if _, err := runCLI(t, "--root", dir, "block", "--from", "codex", "--code", "env", "--severity", "error", "--body", "tool missing"); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := runCLI(t, "--root", dir, "handoff", "--from", "codex", "--issue", "85", "--commit", "abc123", "--summary", "done"); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	out, err := runCLI(t, "--root", dir, "read", "--inbox", "claude")
	if err != nil {
		t.Fatalf("read claude: %v", err)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(out), &msgs); err != nil {
		t.Fatalf("decode claude inbox: %v\n%s", err, out)
	}
	if len(msgs) != 3 {
		t.Fatalf("len=%d want 3", len(msgs))
	}
	if msgs[0]["kind"] != "block" {
		t.Fatalf("first kind=%v want block", msgs[0]["kind"])
	}
	if msgs[2]["kind"] != "handoff" {
		t.Fatalf("third kind=%v want handoff", msgs[2]["kind"])
	}
}

func TestCLIClaimCapabilityAndLessons(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, "--root", dir, "claim", "85"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := runCLI(t, "--root", dir, "capability", "publish", "--profile-hash", "abc"); err != nil {
		t.Fatalf("capability publish: %v", err)
	}
	if _, err := runCLI(t, "--root", dir, "lessons", "propose", "protocol", "--body", "keep fields stable"); err != nil {
		t.Fatalf("lessons propose: %v", err)
	}
	out, err := runCLI(t, "--root", dir, "lessons", "get", "protocol")
	if err != nil {
		t.Fatalf("lessons get: %v", err)
	}
	var lessons []map[string]any
	if err := json.Unmarshal([]byte(out), &lessons); err != nil {
		t.Fatalf("decode lessons: %v\n%s", err, out)
	}
	if len(lessons) != 1 || lessons[0]["body"] != "keep fields stable" {
		t.Fatalf("unexpected lessons: %+v", lessons)
	}
	if _, err := os.Stat(filepath.Join(dir, "registry", "codex.json")); err != nil {
		t.Fatalf("registry missing: %v", err)
	}
	out, err = runCLI(t, "--root", dir, "read", "--inbox", "claude")
	if err != nil {
		t.Fatalf("read claude: %v", err)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(out), &msgs); err != nil {
		t.Fatalf("decode claude inbox: %v\n%s", err, out)
	}
	kinds := map[string]bool{}
	for _, msg := range msgs {
		kinds[msg["kind"].(string)] = true
	}
	for _, kind := range []string{"claim", "capability_publish"} {
		if !kinds[kind] {
			t.Fatalf("missing %s message in %+v", kind, msgs)
		}
	}
}
