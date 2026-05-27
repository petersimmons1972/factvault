// Package agentcomms implements the agent message bus protocol v1
// (filesystem-based message queue between Claude and Codex).
//
// See .agent-comms/README.md and .agent-comms/schema.json for the protocol
// contract. This package is the Go v2 of the bus (replacing the Python v1
// reference at .agent-comms/bin/agentcomms).
package agentcomms

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// Agent identifies a participant on the bus.
type Agent string

const (
	AgentClaude Agent = "claude"
	AgentCodex  Agent = "codex"
)

// Kind enumerates the message kinds defined in schema.json.
type Kind string

const (
	KindQuestion Kind = "question"
	KindAnswer   Kind = "answer"
	KindNudge    Kind = "nudge"
	KindBlock    Kind = "block"
	KindHandoff  Kind = "handoff"
	KindAck      Kind = "ack"
)

// validKinds is the set of valid Kind values per schema.json.
var validKinds = map[Kind]struct{}{
	KindQuestion: {}, KindAnswer: {}, KindNudge: {},
	KindBlock: {}, KindHandoff: {}, KindAck: {},
}

// validAgents is the set of valid Agent values per schema.json.
var validAgents = map[Agent]struct{}{
	AgentClaude: {}, AgentCodex: {},
}

// priorityKinds are drained ahead of other kinds (§20.3).
// Schema v1 has no error_fatal / nack / decision kinds yet, so block is
// the sole priority kind for now.
var priorityKinds = map[Kind]struct{}{
	KindBlock: {},
}

// ulidPattern matches Crockford base32 ULIDs (26 chars, 0-9 A-Z minus I,L,O,U).
var ulidPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// IsULID reports whether id is a valid 26-character Crockford ULID.
func IsULID(id string) bool {
	return ulidPattern.MatchString(id)
}

// Message is the on-wire representation of an agent message.
// All fields conform to schema.json; additional fields are rejected on read.
type Message struct {
	ID        string   `json:"id"`
	From      Agent    `json:"from"`
	To        Agent    `json:"to"`
	TS        string   `json:"ts"`
	Kind      Kind     `json:"kind"`
	Refs      []string `json:"refs"`
	InReplyTo *string  `json:"in_reply_to,omitempty"`
	Body      string   `json:"body"`
}

// Validate checks the message against schema.json constraints.
// Returns a descriptive error on failure, nil on success.
func (m *Message) Validate() error {
	if !IsULID(m.ID) {
		return fmt.Errorf("id %q: invalid ULID", m.ID)
	}
	if _, ok := validAgents[m.From]; !ok {
		return fmt.Errorf("from %q: not a valid agent", m.From)
	}
	if _, ok := validAgents[m.To]; !ok {
		return fmt.Errorf("to %q: not a valid agent", m.To)
	}
	if _, err := time.Parse(time.RFC3339, m.TS); err != nil {
		return fmt.Errorf("ts %q: not RFC3339: %w", m.TS, err)
	}
	if _, ok := validKinds[m.Kind]; !ok {
		return fmt.Errorf("kind %q: not a valid kind", m.Kind)
	}
	if m.Refs == nil {
		return fmt.Errorf("refs: must be present (use empty array, not null)")
	}
	if m.InReplyTo != nil && !IsULID(*m.InReplyTo) {
		return fmt.Errorf("in_reply_to %q: invalid ULID", *m.InReplyTo)
	}
	return nil
}

// IsPriority reports whether the message belongs to a priority lane (§20.3).
func (m *Message) IsPriority() bool {
	_, ok := priorityKinds[m.Kind]
	return ok
}

// ParseMessage decodes a JSON document and validates it.
func ParseMessage(data []byte) (*Message, error) {
	var msg Message
	dec := json.NewDecoder(bytesReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&msg); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	return &msg, nil
}
