package agentcomms

import (
	"strings"
	"testing"
	"time"
)

func validMsg(t *testing.T) *Message {
	t.Helper()
	id, err := NewULID(time.Now())
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}
	return &Message{
		ID:   id,
		From: AgentClaude,
		To:   AgentCodex,
		TS:   time.Now().UTC().Format(time.RFC3339),
		Kind: KindNudge,
		Refs: []string{},
		Body: "hello",
	}
}

func TestValidateOK(t *testing.T) {
	m := validMsg(t)
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateBadULID(t *testing.T) {
	m := validMsg(t)
	m.ID = "not-a-ulid"
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for bad ULID")
	}
}

func TestValidateBadAgent(t *testing.T) {
	m := validMsg(t)
	m.From = Agent("martian")
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for bad agent")
	}
}

func TestValidateBadKind(t *testing.T) {
	m := validMsg(t)
	m.Kind = Kind("bogus")
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for bad kind")
	}
}

func TestValidateBadTS(t *testing.T) {
	m := validMsg(t)
	m.TS = "yesterday"
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for bad ts")
	}
}

func TestValidateNilRefs(t *testing.T) {
	m := validMsg(t)
	m.Refs = nil
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for nil refs")
	}
}

func TestParseMessageRejectsUnknownField(t *testing.T) {
	doc := `{"id":"01HXYZABCDEFGHJKMNPQRSTVWX","from":"claude","to":"codex","ts":"2026-05-23T01:14:00Z","kind":"ack","refs":[],"body":"hi","mystery":"yes"}`
	if _, err := ParseMessage([]byte(doc)); err == nil || !strings.Contains(err.Error(), "mystery") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestParseMessageOK(t *testing.T) {
	doc := `{"id":"01HXYZABCDEFGHJKMNPQRSTVWX","from":"claude","to":"codex","ts":"2026-05-23T01:14:00Z","kind":"ack","refs":[],"body":"hi"}`
	m, err := ParseMessage([]byte(doc))
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if m.Kind != KindAck {
		t.Fatalf("kind: %v", m.Kind)
	}
}

func TestIsPriority(t *testing.T) {
	m := validMsg(t)
	m.Kind = KindBlock
	if !m.IsPriority() {
		t.Fatal("block should be priority")
	}
	m.Kind = KindAck
	if m.IsPriority() {
		t.Fatal("ack should not be priority")
	}
}
