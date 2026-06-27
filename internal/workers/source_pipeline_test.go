package workers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/testdb"
	"github.com/petersimmons1972/factvault/internal/workers"
)

const tenantID = "11111111-1111-1111-1111-111111111111"

func TestCollectOnce_InsertsCollectedSource(t *testing.T) {
	pool := testdb.New(t)
	p := &workers.SourcePipeline{DB: pool}
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items: []collectors.Item{
			{URL: "https://example.com/a", HTML: []byte("<html><body>alpha</body></html>")},
		},
	}
	if err := p.CollectOnce(context.Background(), tenantID, c); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}

	var status, contentHash string
	var rawHTML []byte
	err := pool.QueryRow(context.Background(), `
SELECT status, content_hash, raw_html
FROM sources WHERE tenant_id = $1 AND url = 'https://example.com/a'
`, tenantID).Scan(&status, &contentHash, &rawHTML)
	if err != nil {
		t.Fatalf("select source: %v", err)
	}
	if status != "collected" {
		t.Fatalf("status=%q want collected", status)
	}
	if len(rawHTML) == 0 || len(contentHash) != 64 {
		t.Fatalf("unexpected raw_html/content_hash: len(raw_html)=%d hash=%q", len(rawHTML), contentHash)
	}
}

func TestArchiveOnce_PromotesCollectedToArchived(t *testing.T) {
	pool := testdb.New(t)
	p := &workers.SourcePipeline{DB: pool}
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items: []collectors.Item{
			{URL: "https://example.com/b", HTML: []byte("<html><body>beta body text</body></html>")},
		},
	}
	if err := p.CollectOnce(context.Background(), tenantID, c); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	if err := p.ArchiveOnce(context.Background(), tenantID, 10); err != nil {
		t.Fatalf("ArchiveOnce: %v", err)
	}

	var status, rawText string
	err := pool.QueryRow(context.Background(), `
SELECT status, raw_text FROM sources
WHERE tenant_id = $1 AND url = 'https://example.com/b'
`, tenantID).Scan(&status, &rawText)
	if err != nil {
		t.Fatalf("select archived source: %v", err)
	}
	if status != "archived" {
		t.Fatalf("status=%q want archived", status)
	}
	if rawText == "" {
		t.Fatal("raw_text should not be empty after archive")
	}
}

func TestCollectOnce_IdempotentAndMetadata(t *testing.T) {
	pool := testdb.New(t)
	p := &workers.SourcePipeline{DB: pool}
	ts := time.Now().UTC().Truncate(time.Second)
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items: []collectors.Item{{
			URL:   "https://example.com/c",
			HTML:  []byte("<html><body>c</body></html>"),
			Title: "Title C", Publisher: "Pub", PublishedAt: &ts, Topic: "topic", Tags: []string{"x", "y"},
		}},
	}
	if err := p.CollectOnce(context.Background(), tenantID, c); err != nil {
		t.Fatalf("CollectOnce #1: %v", err)
	}
	if err := p.CollectOnce(context.Background(), tenantID, c); err != nil {
		t.Fatalf("CollectOnce #2: %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM sources WHERE tenant_id=$1 AND url='https://example.com/c'`, tenantID).Scan(&count); err != nil {
		t.Fatalf("count sources: %v", err)
	}
	if count != 1 {
		t.Fatalf("count=%d want 1", count)
	}
	var title, publisher string
	if err := pool.QueryRow(context.Background(), `SELECT title, publisher FROM sources WHERE tenant_id=$1 AND url='https://example.com/c'`, tenantID).Scan(&title, &publisher); err != nil {
		t.Fatalf("select metadata: %v", err)
	}
	if title != "Title C" || !strings.Contains(publisher, "topic=topic") {
		t.Fatalf("unexpected metadata title=%q publisher=%q", title, publisher)
	}
}

func TestVerifyOnce_WritesVerificationAndStatus(t *testing.T) {
	pool := testdb.New(t)
	const sourceURL = "https://example.com/verify"
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := io.NopCloser(strings.NewReader("<html><body>verify text</body></html>"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       body,
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	p := &workers.SourcePipeline{DB: pool, HTTPClient: httpClient}
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items:         []collectors.Item{{URL: sourceURL, HTML: []byte("<html><body>verify text</body></html>")}},
	}
	ctx := context.Background()
	if err := p.CollectOnce(ctx, tenantID, c); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	if err := p.ArchiveOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ArchiveOnce: %v", err)
	}
	if err := p.VerifyOnce(ctx, tenantID, 0, 10); err != nil {
		t.Fatalf("VerifyOnce: %v", err)
	}

	var sourceStatus string
	if err := pool.QueryRow(ctx, `
SELECT status FROM sources
WHERE tenant_id = $1 AND url = $2
`, tenantID, sourceURL).Scan(&sourceStatus); err != nil {
		t.Fatalf("select source status: %v", err)
	}
	if sourceStatus != "verified" {
		t.Fatalf("source status=%q want verified", sourceStatus)
	}

	var verificationStatus string
	if err := pool.QueryRow(ctx, `
SELECT sv.status FROM source_verifications sv
JOIN sources s ON s.id = sv.source_id
WHERE s.tenant_id = $1 AND s.url = $2
ORDER BY sv.verified_at DESC LIMIT 1
`, tenantID, sourceURL).Scan(&verificationStatus); err != nil {
		t.Fatalf("select verification: %v", err)
	}
	if verificationStatus != "live" {
		t.Fatalf("verification status=%q want live", verificationStatus)
	}
}

func TestVerifyOnce_IncludesExtractedSourcesAndPreservesExtractedStatus(t *testing.T) {
	pool := testdb.New(t)
	const sourceURL = "https://example.com/extracted-verify"
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := io.NopCloser(strings.NewReader("<html><body>same body</body></html>"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       body,
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	p := &workers.SourcePipeline{DB: pool, HTTPClient: httpClient}
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items:         []collectors.Item{{URL: sourceURL, HTML: []byte("<html><body>same body</body></html>")}},
	}
	ctx := context.Background()
	if err := p.CollectOnce(ctx, tenantID, c); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	if err := p.ArchiveOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ArchiveOnce: %v", err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE sources
SET status = 'extracted'
WHERE tenant_id = $1 AND url = $2
`, tenantID, sourceURL); err != nil {
		t.Fatalf("mark extracted: %v", err)
	}

	if err := p.VerifyOnce(ctx, tenantID, 0, 10); err != nil {
		t.Fatalf("VerifyOnce: %v", err)
	}

	var sourceStatus string
	if err := pool.QueryRow(ctx, `
SELECT status FROM sources
WHERE tenant_id = $1 AND url = $2
`, tenantID, sourceURL).Scan(&sourceStatus); err != nil {
		t.Fatalf("select source status: %v", err)
	}
	if sourceStatus != "extracted" {
		t.Fatalf("source status=%q want extracted", sourceStatus)
	}

	var verificationStatus string
	if err := pool.QueryRow(ctx, `
SELECT sv.status FROM source_verifications sv
JOIN sources s ON s.id = sv.source_id
WHERE s.tenant_id = $1 AND s.url = $2
ORDER BY sv.verified_at DESC LIMIT 1
`, tenantID, sourceURL).Scan(&verificationStatus); err != nil {
		t.Fatalf("select verification: %v", err)
	}
	if verificationStatus != "live" {
		t.Fatalf("verification status=%q want live", verificationStatus)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCollectOnce_WritesMeta(t *testing.T) {
	pool := testdb.New(t)
	p := &workers.SourcePipeline{DB: pool}
	c := collectors.StaticCollector{
		CollectorName: "test-meta",
		Items: []collectors.Item{
			{
				URL:  "https://example.com/meta-test",
				HTML: []byte("<html><body>meta test page</body></html>"),
				Meta: map[string]any{"trust_tier": "web"},
			},
		},
	}
	if err := p.CollectOnce(context.Background(), tenantID, c); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}

	var rawMeta []byte
	err := pool.QueryRow(context.Background(), `
SELECT meta FROM sources WHERE tenant_id = $1 AND url = 'https://example.com/meta-test'
`, tenantID).Scan(&rawMeta)
	if err != nil {
		t.Fatalf("select meta: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if meta["trust_tier"] != "web" {
		t.Errorf("expected meta.trust_tier=web, got %v", meta["trust_tier"])
	}
}

// TestCollectOnce_SavepointAllowsBatchContinueOnItemError (W-19) verifies that
// CollectOnce processes all items even when an individual item's INSERT would
// normally abort the transaction.
//
// The current INSERT uses ON CONFLICT (tenant_id, url) DO NOTHING, which means
// duplicate URLs never raise an error.  W-19's savepoint protection guards
// against future constraints and transient failures.  This test exercises the
// code path by collecting 3 items (all valid) and verifying all 3 are persisted —
// proving the savepoint release/continue loop doesn't break normal operation.
// Failure-injection (e.g., CHECK constraint violation) requires interface-level
// mocking of pgxpool which is out of scope for this integration test suite.
func TestCollectOnce_SavepointBatchPreservesAllItems(t *testing.T) {
	pool := testdb.New(t)
	p := &workers.SourcePipeline{DB: pool}
	batchTenantID := "22222222-2222-2222-2222-222222222219"

	c := collectors.StaticCollector{
		CollectorName: "test-w19",
		Items: []collectors.Item{
			{URL: "https://example.com/w19/a", HTML: []byte("<html>item A</html>")},
			{URL: "https://example.com/w19/b", HTML: []byte("<html>item B</html>")},
			{URL: "https://example.com/w19/c", HTML: []byte("<html>item C</html>")},
		},
	}
	if err := p.CollectOnce(context.Background(), batchTenantID, c); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM sources WHERE tenant_id = $1
	`, batchTenantID).Scan(&count); err != nil {
		t.Fatalf("count sources: %v", err)
	}
	if count != 3 {
		t.Errorf("W-19: expected 3 sources inserted, got %d (savepoint batch loop may be broken)", count)
	}
}
