package vocabulary_test

import (
	"testing"

	"github.com/petersimmons1972/factvault/internal/vocabulary"
)

func TestResolverKnownProperty(t *testing.T) {
	t.Parallel()

	resolver := vocabulary.NewResolver(vocabulary.ModeStrict)
	result := resolver.Resolve("SEC CIK", "string", "CIK 0000320193")
	if !result.Known {
		t.Fatal("expected known property")
	}
	if result.Property.Slug != "sec_cik" {
		t.Fatalf("Slug = %q, want sec_cik", result.Property.Slug)
	}
	if len(result.QueuedProposals) != 0 {
		t.Fatalf("queued proposals = %d, want 0", len(result.QueuedProposals))
	}
}

func TestResolverStrictQueuesUnknownProperty(t *testing.T) {
	t.Parallel()

	resolver := vocabulary.NewResolver(vocabulary.ModeStrict)
	result := resolver.Resolve("Launch Date", "date", "Launched in 2024")
	if result.Known {
		t.Fatal("expected unknown property in strict mode")
	}
	if result.Property.Slug != "launch_date" {
		t.Fatalf("Slug = %q, want launch_date", result.Property.Slug)
	}
	if len(result.QueuedProposals) != 1 {
		t.Fatalf("queued proposals = %d, want 1", len(result.QueuedProposals))
	}
	if result.QueuedProposals[0].ProposedSlug != "launch_date" {
		t.Fatalf("ProposedSlug = %q, want launch_date", result.QueuedProposals[0].ProposedSlug)
	}
}

func TestResolverPermissiveAcceptsUnknownProperty(t *testing.T) {
	t.Parallel()

	resolver := vocabulary.NewResolver(vocabulary.ModePermissive)
	result := resolver.Resolve("Launch Date", "date", "Launched in 2024")
	if !result.Known {
		t.Fatal("expected permissive mode to accept unknown property")
	}
	if result.Property.Slug != "launch_date" {
		t.Fatalf("Slug = %q, want launch_date", result.Property.Slug)
	}
	if len(result.QueuedProposals) != 0 {
		t.Fatalf("queued proposals = %d, want 0", len(result.QueuedProposals))
	}
}
