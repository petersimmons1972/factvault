package agentcomms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func mustID(t *testing.T) string {
	t.Helper()
	id, err := NewULID(time.Now())
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}
	return id
}

func mkMsg(t *testing.T, kind Kind, from, to Agent) *Message {
	t.Helper()
	return &Message{
		ID:   mustID(t),
		From: from,
		To:   to,
		TS:   time.Now().UTC().Format(time.RFC3339),
		Kind: kind,
		Refs: []string{},
		Body: "test",
	}
}

func TestSendRoundTrip(t *testing.T) {
	s := newStore(t)
	m := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	if err := s.Send(m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentCodex})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(res.Messages))
	}
	if res.Messages[0].ID != m.ID {
		t.Fatalf("ID mismatch")
	}
}

func TestReadIgnoresTmpFiles(t *testing.T) {
	// §20.1: readers MUST ignore .tmp files.
	s := newStore(t)
	tmpPath := filepath.Join(s.inboxDir(AgentCodex), "2026-05-23T01:00:00Z-01HXYZABCDEFGHJKMNPQRSTVWX.json.tmp")
	if err := os.WriteFile(tmpPath, []byte("{garbage"), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentCodex})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(res.Messages))
	}
	if res.SkippedTmp != 1 {
		t.Fatalf("expected SkippedTmp=1, got %d", res.SkippedTmp)
	}
}

func TestDeadLetterOnMalformed(t *testing.T) {
	// §20.5: malformed JSON → dead-letter/ with sidecar, audit row appended.
	s := newStore(t)
	bad := filepath.Join(s.inboxDir(AgentCodex), "2026-05-23T01:00:00Z-01HXYZABCDEFGHJKMNPQRSTVWX.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentCodex})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.DeadLetter != 1 {
		t.Fatalf("DeadLetter=%d, want 1", res.DeadLetter)
	}
	dlEntries, err := os.ReadDir(filepath.Join(s.Root, "dead-letter"))
	if err != nil {
		t.Fatalf("ReadDir dead-letter: %v", err)
	}
	if len(dlEntries) != 2 { // file + sidecar
		t.Fatalf("dead-letter entries: %d, want 2", len(dlEntries))
	}
	audit, err := os.ReadFile(filepath.Join(s.Root, "audit", "events.jsonl"))
	if err != nil {
		t.Fatalf("audit read: %v", err)
	}
	if !strings.Contains(string(audit), "dead_letter") {
		t.Fatalf("audit row missing dead_letter event: %s", audit)
	}
	if !strings.Contains(string(audit), `"kind":"block"`) {
		t.Fatalf("audit row missing block kind: %s", audit)
	}
}

func TestPriorityDrainFirst(t *testing.T) {
	// §20.3: block kind drains ahead of nudge regardless of timestamp.
	s := newStore(t)
	older := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	older.TS = "2026-05-23T01:00:00Z"
	if err := s.Send(older); err != nil {
		t.Fatal(err)
	}
	newer := mkMsg(t, KindBlock, AgentClaude, AgentCodex)
	newer.TS = "2026-05-23T02:00:00Z"
	if err := s.Send(newer); err != nil {
		t.Fatal(err)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("got %d messages", len(res.Messages))
	}
	if res.Messages[0].Kind != KindBlock {
		t.Fatalf("priority lane: first kind=%s want block", res.Messages[0].Kind)
	}
}

func TestReadFilterByKindAndFrom(t *testing.T) {
	s := newStore(t)
	a := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	if err := s.Send(a); err != nil {
		t.Fatal(err)
	}
	b := mkMsg(t, KindAck, AgentClaude, AgentCodex)
	if err := s.Send(b); err != nil {
		t.Fatal(err)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentCodex, Kind: KindAck})
	if err != nil {
		t.Fatalf("Read kind filter: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Kind != KindAck {
		t.Fatalf("kind filter failed: %+v", res.Messages)
	}
	res, err = s.Read(ReadFilter{Inbox: AgentCodex, From: AgentClaude})
	if err != nil {
		t.Fatalf("Read from filter: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("from filter: got %d", len(res.Messages))
	}
}

func TestArchive(t *testing.T) {
	s := newStore(t)
	m := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	if err := s.Send(m); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive(m.ID, "handled"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentCodex})
	if err != nil {
		t.Fatalf("Read after archive: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("after archive, inbox has %d", len(res.Messages))
	}
	processed, err := os.ReadDir(filepath.Join(s.Root, "processed"))
	if err != nil {
		t.Fatalf("ReadDir processed: %v", err)
	}
	if len(processed) != 1 {
		t.Fatalf("processed dir count: %d", len(processed))
	}
}

func TestArchiveRejectsPartialMessageID(t *testing.T) {
	s := newStore(t)
	m := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	if err := s.Send(m); err != nil {
		t.Fatal(err)
	}
	partial := m.ID[:8]
	err := s.Archive(partial, "handled")
	if err == nil {
		t.Fatalf("expected archive error for partial message id %q", partial)
	}
	res, rerr := s.Read(ReadFilter{Inbox: AgentCodex})
	if rerr != nil {
		t.Fatalf("Read: %v", rerr)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected inbox to remain unchanged, got %d messages", len(res.Messages))
	}
	processed, err := os.ReadDir(filepath.Join(s.Root, "processed"))
	if err != nil {
		t.Fatalf("ReadDir processed: %v", err)
	}
	if len(processed) != 0 {
		t.Fatalf("processed dir count: %d, want 0", len(processed))
	}
}

func TestArchiveRejectsAmbiguousPrefixAcrossMessages(t *testing.T) {
	s := newStore(t)
	m1 := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	m2 := mkMsg(t, KindAck, AgentClaude, AgentCodex)
	if m1.ID[10] == 'A' {
		m2.ID = m1.ID[:10] + "B" + m1.ID[11:]
	} else {
		m2.ID = m1.ID[:10] + "A" + m1.ID[11:]
	}
	prefixLen := 10
	if err := s.Send(m1); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(m2); err != nil {
		t.Fatal(err)
	}
	partial := m1.ID[:prefixLen]
	if err := s.Archive(partial, "handled"); err == nil {
		t.Fatalf("expected archive error for ambiguous partial id %q", partial)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentCodex})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("expected both messages to remain, got %d", len(res.Messages))
	}
	processed, err := os.ReadDir(filepath.Join(s.Root, "processed"))
	if err != nil {
		t.Fatalf("ReadDir processed: %v", err)
	}
	if len(processed) != 0 {
		t.Fatalf("processed dir count: %d, want 0", len(processed))
	}
}

func TestAtomicWriteNoTmpLeak(t *testing.T) {
	// After Send, no .tmp file should remain.
	s := newStore(t)
	m := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	if err := s.Send(m); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.inboxDir(AgentCodex))
	if err != nil {
		t.Fatalf("ReadDir inbox: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover .tmp file: %s", e.Name())
		}
	}
}

func TestSendValidatesMessage(t *testing.T) {
	s := newStore(t)
	m := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
	m.ID = "not-a-ulid"
	if err := s.Send(m); err == nil {
		t.Fatal("expected validation failure")
	}
}

func TestFromCodexToClaudeRoundTrip(t *testing.T) {
	// Sanity: codex→claude direction lands in inbox/claude.
	s := newStore(t)
	m := mkMsg(t, KindAck, AgentCodex, AgentClaude)
	if err := s.Send(m); err != nil {
		t.Fatal(err)
	}
	res, err := s.Read(ReadFilter{Inbox: AgentClaude})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("got %d, want 1", len(res.Messages))
	}
	if res.Messages[0].From != AgentCodex {
		t.Fatalf("from=%s want codex", res.Messages[0].From)
	}
}

func TestQueueDepth(t *testing.T) {
	s := newStore(t)
	for range 3 {
		m := mkMsg(t, KindNudge, AgentClaude, AgentCodex)
		if err := s.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.QueueDepth(AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	if d != 3 {
		t.Fatalf("QueueDepth=%d, want 3", d)
	}
}

// TestSendWritesValidSchema verifies round-trip JSON shape.
func TestSendWritesValidSchema(t *testing.T) {
	s := newStore(t)
	m := mkMsg(t, KindHandoff, AgentClaude, AgentCodex)
	m.Refs = []string{"#85", "commit:abc1234"}
	if err := s.Send(m); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.inboxDir(AgentCodex))
	if err != nil {
		t.Fatalf("ReadDir inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(s.inboxDir(AgentCodex), entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"id", "from", "to", "ts", "kind", "refs", "body"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing required field %q", k)
		}
	}
}
