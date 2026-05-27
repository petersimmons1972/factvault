package workers

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
	"github.com/petersimmons1972/factvault/internal/testdb"
	"github.com/petersimmons1972/factvault/internal/vocabulary"
)

type stubLLM struct {
	proposals []extractors.StatementProposal
}

func (s stubLLM) Extract(context.Context, *db.Source, string) ([]extractors.StatementProposal, error) {
	return s.proposals, nil
}

type emptyExtractor struct{}

func (emptyExtractor) Extract(context.Context, *db.Source, string) ([]extractors.ExtractedFact, error) {
	return nil, nil
}

type scriptedExtractor struct {
	factsBySource map[string][]extractors.ExtractedFact
}

func (s scriptedExtractor) Extract(_ context.Context, source *db.Source, _ string) ([]extractors.ExtractedFact, error) {
	if source == nil {
		return nil, nil
	}
	return s.factsBySource[source.ID.String()], nil
}

type fixedExtractor struct {
	facts []extractors.ExtractedFact
}

func (s fixedExtractor) Extract(context.Context, *db.Source, string) ([]extractors.ExtractedFact, error) {
	return s.facts, nil
}

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

func TestFactPipelineExtractOnce_InvalidTenantID(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	err := (&FactPipeline{DB: pool}).ExtractOnce(ctx, "not-a-uuid", 10)
	if err == nil {
		t.Fatal("expected invalid tenant id error")
	}
}

func TestFactPipelineExtractOnce_EntityExtIDStrategyAvoidsNullCollision(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawText := "Acme raised funds. Globex raised funds. Acme raised funds again."

	if _, err := pool.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, title)
		VALUES ($1, $2, 'https://example.com/entities', 'hash-entities', $3, 'archived', 'entities')
	`, sourceID, tenantID, rawText); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	p := &FactPipeline{
		DB: pool,
		Deterministic: fixedExtractor{facts: []extractors.ExtractedFact{
			mustFactFromExcerpt(t, rawText, "Acme", "raised funds", "Acme raised funds"),
			mustFactFromExcerpt(t, rawText, "Globex", "raised funds", "Globex raised funds"),
			mustFactFromExcerpt(t, rawText, "Acme", "raised funds again", "Acme raised funds again"),
		}},
	}

	if err := p.ExtractOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}

	var entities int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entities WHERE tenant_id = $1`, tenantID).Scan(&entities); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if entities != 2 {
		t.Fatalf("entities=%d want 2", entities)
	}

	var nullExtID int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entities WHERE tenant_id = $1 AND ext_id IS NULL`, tenantID).Scan(&nullExtID); err != nil {
		t.Fatalf("count null ext_id entities: %v", err)
	}
	if nullExtID != 0 {
		t.Fatalf("null ext_id entities=%d want 0", nullExtID)
	}
}

func mustFactFromExcerpt(t *testing.T, rawText, subject, value, excerpt string) extractors.ExtractedFact {
	t.Helper()
	byteStart := strings.Index(rawText, excerpt)
	if byteStart < 0 {
		t.Fatalf("excerpt %q not found in raw text", excerpt)
	}
	offsetStart := len([]rune(rawText[:byteStart]))
	offsetEnd := offsetStart + len([]rune(excerpt))
	return extractors.ExtractedFact{
		SubjectText:        subject,
		PropertySlug:       "description",
		Value:              value,
		ValueType:          "string",
		Excerpt:            excerpt,
		ExcerptOffsetStart: offsetStart,
		ExcerptOffsetEnd:   offsetEnd,
		ExtractionMethod:   "deterministic:test",
	}
}

func TestFactPipelineExtractOnce_StrictQueuesUnknownProperty(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawText := "Acme Corp launched on 2024-03-01."
	_, err := pool.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, title)
		VALUES ($1, $2, 'https://example.com/strict', 'hash', $3, 'archived', 'Acme Corp')
	`, sourceID, tenantID, rawText)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	p := &FactPipeline{
		DB:            pool,
		Deterministic: emptyExtractor{},
		LLM: stubLLM{proposals: []extractors.StatementProposal{
			{
				SubjectText:        "Acme Corp",
				PropertySlug:       "Launch Date",
				Value:              "2024-03-01",
				ValueType:          "date",
				Excerpt:            "launched on 2024-03-01.",
				ExcerptOffsetStart: 10,
				ExcerptOffsetEnd:   33,
			},
		}},
		VocabularyMode: vocabulary.ModeStrict,
	}
	if err := p.ExtractOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}

	var statements, proposals int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM statements WHERE tenant_id=$1`, tenantID).Scan(&statements); err != nil {
		t.Fatalf("count statements: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM proposed_properties WHERE tenant_id=$1`, tenantID).Scan(&proposals); err != nil {
		t.Fatalf("count proposed properties: %v", err)
	}
	if statements != 0 {
		t.Fatalf("statements=%d want 0", statements)
	}
	if proposals != 1 {
		t.Fatalf("proposals=%d want 1", proposals)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM sources WHERE id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("source status: %v", err)
	}
	if status != "archived" {
		t.Fatalf("status=%q want archived", status)
	}
}

func TestFactPipelineExtractOnce_StrictUnknownAndKnownAdvancesStatusWhenAnyAccepted(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawText := "Acme Corp launched on 2024-03-01."
	rawLen := utf8.RuneCountInString(rawText)
	_, err := pool.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, title)
		VALUES ($1, $2, 'https://example.com/strict-mixed', 'hash', $3, 'archived', 'Acme Corp')
	`, sourceID, tenantID, rawText)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	p := &FactPipeline{
		DB:            pool,
		Deterministic: emptyExtractor{},
		LLM: stubLLM{proposals: []extractors.StatementProposal{
			{
				SubjectText:        "Acme Corp",
				PropertySlug:       "Launch Date",
				Value:              "2024-03-01",
				ValueType:          "date",
				Excerpt:            rawText,
				ExcerptOffsetStart: 0,
				ExcerptOffsetEnd:   rawLen,
			},
			{
				SubjectText:        "Acme Corp",
				PropertySlug:       "Date",
				Value:              "2024-03-01",
				ValueType:          "date",
				Excerpt:            rawText,
				ExcerptOffsetStart: 0,
				ExcerptOffsetEnd:   rawLen,
			},
		}},
		VocabularyMode: vocabulary.ModeStrict,
	}
	if err := p.ExtractOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}

	var statements, proposals int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM statements WHERE tenant_id=$1`, tenantID).Scan(&statements); err != nil {
		t.Fatalf("count statements: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM proposed_properties WHERE tenant_id=$1`, tenantID).Scan(&proposals); err != nil {
		t.Fatalf("count proposed properties: %v", err)
	}
	if statements != 1 {
		t.Fatalf("statements=%d want 1", statements)
	}
	if proposals != 1 {
		t.Fatalf("proposals=%d want 1", proposals)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM sources WHERE id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("source status: %v", err)
	}
	if status != "extracted" {
		t.Fatalf("status=%q want extracted", status)
	}
}

func TestFactPipelineExtractOnce_PermissiveRegistersUnknownProperty(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawText := "Acme Corp launched on 2024-03-01."
	_, err := pool.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, title)
		VALUES ($1, $2, 'https://example.com/permissive', 'hash', $3, 'archived', 'Acme Corp')
	`, sourceID, tenantID, rawText)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}

	p := &FactPipeline{
		DB:            pool,
		Deterministic: emptyExtractor{},
		LLM: stubLLM{proposals: []extractors.StatementProposal{
			{
				SubjectText:        "Acme Corp",
				PropertySlug:       "Launch Date",
				Value:              "2024-03-01",
				ValueType:          "date",
				Excerpt:            "launched on 2024-03-01.",
				ExcerptOffsetStart: 10,
				ExcerptOffsetEnd:   33,
			},
		}},
		VocabularyMode: vocabulary.ModePermissive,
	}
	if err := p.ExtractOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}

	var statements, proposals, properties int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM statements WHERE tenant_id=$1`, tenantID).Scan(&statements); err != nil {
		t.Fatalf("count statements: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM proposed_properties WHERE tenant_id=$1`, tenantID).Scan(&proposals); err != nil {
		t.Fatalf("count proposed properties: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM properties WHERE tenant_id=$1 AND slug='launch_date'`, tenantID).Scan(&properties); err != nil {
		t.Fatalf("count properties: %v", err)
	}
	if statements != 1 {
		t.Fatalf("statements=%d want 1", statements)
	}
	if proposals != 0 {
		t.Fatalf("proposals=%d want 0", proposals)
	}
	if properties != 1 {
		t.Fatalf("properties=%d want 1", properties)
	}
}

func TestFactPipelineExtractOnce_CostGuardrailBlocksFrontierProviders(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()

	p := &FactPipeline{
		DB:                     pool,
		LLM:                    stubLLM{},
		LLMProvider:            "openai",
		CostGuardrailThreshold: 1000,
	}
	err := p.ExtractOnce(ctx, tenantID, 1001)
	if err == nil {
		t.Fatal("expected cost guardrail error")
	}
}

func TestFactPipelineExtractOnce_CostGuardrailAllowsLocalProvider(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()

	p := &FactPipeline{
		DB:                     pool,
		LLM:                    stubLLM{},
		LLMProvider:            "local",
		CostGuardrailThreshold: 1000,
	}
	if err := p.ExtractOnce(ctx, tenantID, 1001); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}
}

func TestFactPipelineExtractOnce_CostGuardrailBypassesWithConfirm(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()

	p := &FactPipeline{
		DB:                     pool,
		LLM:                    stubLLM{},
		LLMProvider:            "anthropic",
		ConfirmCost:            true,
		CostGuardrailThreshold: 1000,
	}
	if err := p.ExtractOnce(ctx, tenantID, 1001); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}
}
