package workers_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestVerifyOnce_WritesVerificationAndStatus(t *testing.T) {
	pool := testdb.New(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html><body>verify text</body></html>")
	}))
	defer server.Close()

	p := &workers.SourcePipeline{DB: pool, HTTPClient: server.Client()}
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items:         []collectors.Item{{URL: server.URL, HTML: []byte("<html><body>verify text</body></html>")}},
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
`, tenantID, server.URL).Scan(&sourceStatus); err != nil {
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
`, tenantID, server.URL).Scan(&verificationStatus); err != nil {
		t.Fatalf("select verification: %v", err)
	}
	if verificationStatus != "live" {
		t.Fatalf("verification status=%q want live", verificationStatus)
	}
}
