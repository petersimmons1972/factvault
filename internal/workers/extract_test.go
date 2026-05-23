package workers

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestVerifyExcerptOffset_MatchesExactSubstring(t *testing.T) {
	rawText := "The quick brown fox jumps over the lazy dog"
	excerpt := "quick brown fox"

	// "The " = 4 chars, so "quick" starts at index 4
	// "quick brown fox" = 15 chars, ends at 19
	verified := VerifyExcerptOffset(rawText, excerpt, 4, 19)
	if !verified {
		t.Errorf("expected offset match for exact substring")
	}
}

func TestVerifyExcerptOffset_RejectsWrongExcerpt(t *testing.T) {
	rawText := "The quick brown fox jumps over the lazy dog"
	wrongExcerpt := "slow red fox"

	// Correct offsets for "quick brown fox"
	verified := VerifyExcerptOffset(rawText, wrongExcerpt, 4, 19)
	if verified {
		t.Errorf("expected rejection for wrong excerpt with correct offsets")
	}
}

func TestVerifyExcerptOffset_RejectsWrongOffsets(t *testing.T) {
	rawText := "The quick brown fox jumps over the lazy dog"
	excerpt := "quick brown fox"

	// Wrong offsets (off by one)
	verified := VerifyExcerptOffset(rawText, excerpt, 5, 20)
	if verified {
		t.Errorf("expected rejection for wrong offsets")
	}
}

func TestVerifyExcerptOffset_HandlesUTF8(t *testing.T) {
	// Text with multi-byte UTF-8 characters
	rawText := "Café is a word" // é is 2 bytes in UTF-8
	excerpt := "a word"

	// "Caf" = 3 runes (even though é is 2 bytes, it's still 1 rune)
	// "é " = runes 3-4, "is " = runes 4-7, "a word" = runes 8-14
	// So "a word" starts at rune 8, ends at rune 14
	verified := VerifyExcerptOffset(rawText, excerpt, 8, 14)
	if !verified {
		t.Errorf("expected UTF-8 handling to work correctly")
	}
}

func TestVerifyExcerptOffset_HandlesStartOfString(t *testing.T) {
	rawText := "Hello world"
	excerpt := "Hello"

	verified := VerifyExcerptOffset(rawText, excerpt, 0, 5)
	if !verified {
		t.Errorf("expected match at start of string")
	}
}

func TestVerifyExcerptOffset_HandlesEndOfString(t *testing.T) {
	rawText := "Hello world"
	excerpt := "world"

	verified := VerifyExcerptOffset(rawText, excerpt, 6, 11)
	if !verified {
		t.Errorf("expected match at end of string")
	}
}

func TestVerifyExcerptOffset_RejectsNegativeOffsets(t *testing.T) {
	rawText := "Hello world"

	verified := VerifyExcerptOffset(rawText, "Hello", -1, 5)
	if verified {
		t.Errorf("expected rejection for negative start offset")
	}

	verified = VerifyExcerptOffset(rawText, "Hello", 0, -1)
	if verified {
		t.Errorf("expected rejection for negative end offset")
	}
}

func TestVerifyExcerptOffset_RejectsInvertedOffsets(t *testing.T) {
	rawText := "Hello world"

	verified := VerifyExcerptOffset(rawText, "", 5, 5)
	if verified {
		t.Errorf("expected rejection for equal offsets")
	}

	verified = VerifyExcerptOffset(rawText, "", 10, 5)
	if verified {
		t.Errorf("expected rejection for inverted offsets")
	}
}

func TestVerifyExcerptOffset_RejectsOutOfBoundsOffsets(t *testing.T) {
	rawText := "Hello world"

	// Offsets beyond string length
	verified := VerifyExcerptOffset(rawText, "", 0, 100)
	if verified {
		t.Errorf("expected rejection for out-of-bounds end offset")
	}

	verified = VerifyExcerptOffset(rawText, "", 100, 200)
	if verified {
		t.Errorf("expected rejection for completely out-of-bounds offsets")
	}
}

func TestVerifyExcerptOffset_ExactMatchOnly(t *testing.T) {
	rawText := "Hello world"

	// Exact match
	verified := VerifyExcerptOffset(rawText, "Hello", 0, 5)
	if !verified {
		t.Errorf("expected exact match to succeed")
	}

	// Case mismatch (should fail - no case folding)
	verified = VerifyExcerptOffset(rawText, "hello", 0, 5)
	if verified {
		t.Errorf("expected case-sensitive comparison to reject 'hello' vs 'Hello'")
	}

	// Trimmed version (should fail - no trimming)
	verified = VerifyExcerptOffset(rawText, "Hello ", 0, 6)
	if !verified {
		t.Errorf("expected exact match including space")
	}
}

func TestVerifyExcerptOffset_EmptyExcerpt(t *testing.T) {
	rawText := "Hello world"

	// Empty excerpt with equal offsets
	verified := VerifyExcerptOffset(rawText, "", 5, 5)
	if verified {
		t.Errorf("expected rejection for zero-length range")
	}
}

func TestVerifyExcerptOffset_LLMHallucination(t *testing.T) {
	// Simulates the core anti-hallucination scenario:
	// LLM fabricates an offset range that doesn't actually contain the proposed excerpt
	rawText := "Apple Inc. was founded in 1976. Steve Jobs was CEO."

	// Proposed excerpt: "Microsoft" (doesn't appear in text)
	// Proposed offsets: (0, 9) - which actually contains "Apple Inc."
	verified := VerifyExcerptOffset(rawText, "Microsoft", 0, 9)
	if verified {
		t.Errorf("expected rejection of hallucinated excerpt with wrong offsets")
	}

	// Correct match: "Apple Inc" is at positions 0-9 (9 characters)
	verified = VerifyExcerptOffset(rawText, "Apple Inc", 0, 9)
	if !verified {
		t.Errorf("expected acceptance of correct excerpt")
	}
}

func TestVerifyExcerptOffset_MultiByteCharacters(t *testing.T) {
	// Test with emoji (4 bytes per emoji, but 1 rune each)
	rawText := "Hello 👋 world"

	// "Hello " = 6 runes
	// "👋" = 1 rune
	// " world" = 6 runes
	// So "👋" is at indices 6-7

	verified := VerifyExcerptOffset(rawText, "👋", 6, 7)
	if !verified {
		t.Errorf("expected emoji handling to work correctly")
	}
}

func TestVerifyExcerptOffset_WhitespaceMatters(t *testing.T) {
	rawText := "Hello  world" // Two spaces between words

	// Match with both spaces
	verified := VerifyExcerptOffset(rawText, "Hello  world", 0, 12)
	if !verified {
		t.Errorf("expected exact whitespace match")
	}

	// Try to match with one space (should fail)
	verified = VerifyExcerptOffset(rawText, "Hello world", 0, 12)
	if verified {
		t.Errorf("expected rejection due to whitespace mismatch")
	}
}

func TestFactPipelineExtractOnce_InsertsStatements(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()
	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawText := "Apple Inc. reported $1.2M on 2024-05-23."
	_, err := pool.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, title)
		VALUES ($1, $2, 'https://example.com/source', 'hash', $3, 'archived', 'Example source')
	`, sourceID, tenantID, rawText)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if err := (&FactPipeline{DB: pool}).ExtractOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}
	var statements int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM statements WHERE tenant_id=$1`, tenantID).Scan(&statements); err != nil {
		t.Fatalf("count statements: %v", err)
	}
	if statements == 0 {
		t.Fatal("expected extracted statements")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM sources WHERE id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("source status: %v", err)
	}
	if status != "extracted" {
		t.Fatalf("status=%q want extracted", status)
	}
}
