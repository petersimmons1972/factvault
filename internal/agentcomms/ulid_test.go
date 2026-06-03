package agentcomms

import (
	"testing"
	"time"
)

func TestNewULIDFormat(t *testing.T) {
	id, err := NewULID(time.Now())
	if err != nil {
		t.Fatalf("NewULID: %v", err)
	}
	if len(id) != 26 {
		t.Fatalf("ULID length: got %d, want 26 (%q)", len(id), id)
	}
	if !ulidPattern.MatchString(id) {
		t.Fatalf("ULID %q does not match pattern", id)
	}
}

func TestNewULIDUniqueness(t *testing.T) {
	seen := make(map[string]struct{})
	now := time.Now()
	for i := range 1000 {
		id, err := NewULID(now)
		if err != nil {
			t.Fatalf("NewULID: %v", err)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate ULID at iteration %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewULIDMonotonicByTime(t *testing.T) {
	// Two ULIDs 100ms apart should sort in time order at the prefix.
	a, err := NewULID(time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewULID(time.Unix(2000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if a[:10] >= b[:10] {
		t.Fatalf("ULID time prefix not monotonic: %s vs %s", a, b)
	}
}
