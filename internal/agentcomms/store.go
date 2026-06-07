package agentcomms

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxQueueDepth is the §20.2 hard cap on undrained messages per inbox.
const MaxQueueDepth = 1024

// SoftQueueDepth is the §20.2 soft cap above which heartbeats should signal
// backpressure (§20.7 queue_depth field).
const SoftQueueDepth = 256

// ErrQueueFull is returned by Send when the recipient inbox is at MaxQueueDepth.
var ErrQueueFull = errors.New("recipient queue at hard cap (1024); refusing send")

// Store is the filesystem-backed message bus rooted at a `.agent-comms`
// directory. All operations are safe for concurrent callers within a single
// process; cross-process atomicity is provided by `.tmp + rename` (§20.1)
// and by readers ignoring `.tmp` files.
type Store struct {
	Root string // path to the .agent-comms directory
}

// NewStore returns a Store rooted at root. The directory and standard
// subdirectories are created if missing.
func NewStore(root string) (*Store, error) {
	s := &Store{Root: root}
	for _, sub := range []string{
		"inbox/claude", "inbox/codex", "processed", "dead-letter", "audit",
	} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o750); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}
	return s, nil
}

func (s *Store) inboxDir(a Agent) string {
	return filepath.Join(s.Root, "inbox", string(a))
}

// filename returns the canonical `<ISO-8601-Z>-<ULID>.json` filename.
func filename(ts time.Time, id string) string {
	return fmt.Sprintf("%s-%s.json", ts.UTC().Format("2006-01-02T15:04:05Z"), id)
}

// Send writes msg to the recipient inbox atomically (§20.1).
// It enforces the hard cap (§20.2) and returns ErrQueueFull if exceeded.
func (s *Store) Send(msg *Message) error {
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	dir := s.inboxDir(msg.To)
	depth, err := s.queueDepth(msg.To)
	if err != nil {
		return err
	}
	if depth >= MaxQueueDepth {
		return ErrQueueFull
	}
	ts, err := time.Parse(time.RFC3339, msg.TS)
	if err != nil {
		ts = time.Time{}
	}
	final := filepath.Join(dir, filename(ts, msg.ID))
	return atomicWriteJSON(final, msg)
}

// atomicWriteJSON marshals v as indented JSON to a `.tmp` sibling, fsyncs,
// then renames to final. Readers MUST ignore `*.tmp` (§20.1).
func atomicWriteJSON(final string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := filepath.Clean(final + ".tmp")
	// tmp is derived from the final destination in this package; directory traversal is not user-driven.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: path constructed from internal message filename.
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		closeErr := f.Close()
		removeErr := os.Remove(tmp)
		return errors.Join(fmt.Errorf("write: %w", err), closeErr, removeErr)
	}
	if err := f.Sync(); err != nil {
		closeErr := f.Close()
		removeErr := os.Remove(tmp)
		return errors.Join(fmt.Errorf("fsync: %w", err), closeErr, removeErr)
	}
	if err := f.Close(); err != nil {
		removeErr := os.Remove(tmp)
		return errors.Join(fmt.Errorf("close: %w", err), removeErr)
	}
	if err := os.Rename(tmp, final); err != nil {
		removeErr := os.Remove(tmp)
		return errors.Join(fmt.Errorf("rename: %w", err), removeErr)
	}
	return nil
}

// queueDepth counts `.json` files in the recipient inbox (ignoring `.tmp`).
func (s *Store) queueDepth(a Agent) (int, error) {
	entries, err := os.ReadDir(s.inboxDir(a))
	if err != nil {
		return 0, fmt.Errorf("readdir inbox/%s: %w", a, err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".tmp") {
			count++
		}
	}
	return count, nil
}

// QueueDepth is the public form of queueDepth (used by `health` and heartbeat
// backpressure signaling).
func (s *Store) QueueDepth(a Agent) (int, error) { return s.queueDepth(a) }

// ReadResult is the parsed inbox content from Read.
type ReadResult struct {
	Messages   []*Message
	DeadLetter int // number of messages routed to dead-letter during this read
	SkippedTmp int // number of `.tmp` files ignored
}

// ReadFilter narrows the messages returned by Read.
type ReadFilter struct {
	Inbox  Agent
	Kind   Kind  // empty = no filter
	From   Agent // empty = no filter
	Unread bool  // future hook (cursor) — current impl is stateless
}

// Read returns messages from the inbox, drained by priority lane (§20.3) then
// timestamp. Malformed files are routed to dead-letter/ (§20.5) and a `block`
// audit row is appended.
func (s *Store) Read(filter ReadFilter) (*ReadResult, error) {
	if _, ok := validAgents[filter.Inbox]; !ok {
		return nil, fmt.Errorf("inbox %q: not a valid agent", filter.Inbox)
	}
	dir := s.inboxDir(filter.Inbox)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("readdir: %w", err)
	}
	out := &ReadResult{}
	var priority, normal []*Message
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			out.SkippedTmp++
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		p := filepath.Join(dir, filepath.Base(name))
		data, rerr := os.ReadFile(filepath.Clean(p)) //nolint:gosec // G304: directory and filename are from trusted inbox entry list.
		if rerr != nil {
			// Race with concurrent archive — skip silently.
			if errors.Is(rerr, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", name, rerr)
		}
		msg, perr := ParseMessage(data)
		if perr != nil {
			if derr := s.routeDeadLetter(p, data, perr); derr != nil {
				return nil, derr
			}
			out.DeadLetter++
			continue
		}
		if filter.Kind != "" && msg.Kind != filter.Kind {
			continue
		}
		if filter.From != "" && msg.From != filter.From {
			continue
		}
		if msg.IsPriority() {
			priority = append(priority, msg)
		} else {
			normal = append(normal, msg)
		}
	}
	sortByTS(priority)
	sortByTS(normal)
	out.Messages = append(priority, normal...)
	return out, nil
}

func sortByTS(ms []*Message) {
	sort.SliceStable(ms, func(i, j int) bool {
		return ms[i].TS < ms[j].TS // RFC3339 sorts lexicographically when zulu
	})
}

// routeDeadLetter moves the offending file under dead-letter/ alongside a
// `.error.json` sidecar describing the parse failure, and appends a `block`
// row to the audit log (§20.5).
func (s *Store) routeDeadLetter(origPath string, data []byte, parseErr error) error {
	base := filepath.Base(origPath)
	dl := filepath.Clean(filepath.Join(s.Root, "dead-letter", base))
	if err := os.WriteFile(dl, data, 0o600); err != nil { //nolint:gosec // G703: dead-letter path is rooted in s.Root and basename-sanitized.
		return fmt.Errorf("dead-letter write: %w", err)
	}
	if err := os.Remove(origPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dead-letter remove orig: %w", err)
	}
	sidecar := dl + ".error.json"
	errObj := map[string]any{
		"file":     base,
		"error":    parseErr.Error(),
		"detected": time.Now().UTC().Format(time.RFC3339),
	}
	if err := atomicWriteJSON(sidecar, errObj); err != nil {
		return fmt.Errorf("dead-letter sidecar: %w", err)
	}
	// Append a structured audit row recording the dead-letter event. The
	// audit row mirrors a `block` message body so callers can tail it.
	return s.appendAudit(map[string]any{
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"event":  "dead_letter",
		"kind":   "block",
		"file":   base,
		"reason": parseErr.Error(),
	})
}

// appendAudit appends a JSON-Lines row to audit/events.jsonl.
func (s *Store) appendAudit(row map[string]any) error {
	p := filepath.Join(s.Root, "audit", "events.jsonl")
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("audit marshal: %w", err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: path rooted under configured store root.
	if err != nil {
		return fmt.Errorf("audit open: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		closeErr := f.Close()
		return errors.Join(fmt.Errorf("audit write: %w", err), closeErr)
	}
	return f.Close()
}

// Archive moves a message file to processed/ with an optional reason logged
// to the audit trail.
func (s *Store) Archive(msgID, reason string) error {
	for _, a := range []Agent{AgentClaude, AgentCodex} {
		dir := s.inboxDir(a)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("readdir inbox/%s: %w", a, err)
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasSuffix(name, ".tmp") || !strings.HasSuffix(name, ".json") {
				continue
			}
			fileMsgID, ok := messageIDFromFilename(name)
			if !ok || fileMsgID != msgID {
				continue
			}
			src := filepath.Join(dir, name)
			dst := filepath.Join(s.Root, "processed", name)
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("archive rename: %w", err)
			}
			return s.appendAudit(map[string]any{
				"ts":     time.Now().UTC().Format(time.RFC3339),
				"event":  "archive",
				"msg_id": msgID,
				"reason": reason,
				"from":   string(a),
			})
		}
	}
	return fmt.Errorf("archive: message %s not found in any inbox", msgID)
}

func messageIDFromFilename(name string) (string, bool) {
	if !strings.HasSuffix(name, ".json") {
		return "", false
	}
	base := strings.TrimSuffix(name, ".json")
	i := strings.LastIndex(base, "-")
	if i <= 0 || i >= len(base)-1 {
		return "", false
	}
	return base[i+1:], true
}
