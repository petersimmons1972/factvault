package workers_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
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
	serverURL, err := neturl.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	upstream := server.Client().Transport
	client := &http.Client{
		Transport: rewriteTransport{
			targetScheme: serverURL.Scheme,
			targetHost:   serverURL.Host,
			base:         upstream,
		},
	}

	p := &workers.SourcePipeline{DB: pool, HTTPClient: client}
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items:         []collectors.Item{{URL: "https://example.com/verify", HTML: []byte("<html><body>verify text</body></html>")}},
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
`, tenantID, "https://example.com/verify").Scan(&sourceStatus); err != nil {
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
`, tenantID, "https://example.com/verify").Scan(&verificationStatus); err != nil {
		t.Fatalf("select verification: %v", err)
	}
	if verificationStatus != "live" {
		t.Fatalf("verification status=%q want live", verificationStatus)
	}
}

func TestCollectOnce_SkipsUnsafeURL(t *testing.T) {
	pool := testdb.New(t)
	p := &workers.SourcePipeline{DB: pool}
	c := collectors.StaticCollector{
		CollectorName: "test",
		Items:         []collectors.Item{{URL: "http://127.0.0.1/internal", HTML: []byte("<html><body>x</body></html>")}},
	}
	if err := p.CollectOnce(context.Background(), tenantID, c); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM sources WHERE tenant_id=$1 AND url='http://127.0.0.1/internal'`, tenantID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("count=%d want 0", count)
	}
}

func TestVerifyOnce_BlocksUnsafeStoredURL(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
INSERT INTO sources (id, tenant_id, url, content_hash, status)
VALUES (gen_random_uuid(), $1, 'http://127.0.0.1/private', 'abc', 'archived')
`, tenantID)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	p := &workers.SourcePipeline{DB: pool}
	if err := p.VerifyOnce(ctx, tenantID, 1, 10); err != nil {
		t.Fatalf("VerifyOnce: %v", err)
	}
	var verificationStatus, notes string
	if err := pool.QueryRow(ctx, `
SELECT sv.status, COALESCE(sv.notes, '')
FROM source_verifications sv
JOIN sources s ON s.id = sv.source_id
WHERE s.tenant_id = $1
ORDER BY sv.verified_at DESC LIMIT 1
`, tenantID).Scan(&verificationStatus, &notes); err != nil {
		t.Fatalf("select verification: %v", err)
	}
	if verificationStatus != "link-rot" {
		t.Fatalf("verification status=%q want link-rot", verificationStatus)
	}
	if notes == "" {
		t.Fatal("expected security rejection note")
	}
}

func TestVerifyOnce_IncludesExtractedSources(t *testing.T) {
	pool := testdb.New(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html><body>ok</body></html>")
	}))
	defer server.Close()
	serverURL, err := neturl.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	upstream := server.Client().Transport
	client := &http.Client{
		Transport: rewriteTransport{
			targetScheme: serverURL.Scheme,
			targetHost:   serverURL.Host,
			base:         upstream,
		},
	}
	ctx := context.Background()
	_, err = pool.Exec(ctx, `
INSERT INTO sources (id, tenant_id, url, content_hash, status)
VALUES (gen_random_uuid(), $1, 'https://example.com/extracted', $2, 'extracted')
`, tenantID, "fca4fba10587858fa53ef6f1f5490ce4d3f4f4b8d6fd8a068d5f0f04f53f6b38")
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	p := &workers.SourcePipeline{DB: pool, HTTPClient: client}
	if err := p.VerifyOnce(ctx, tenantID, 1, 10); err != nil {
		t.Fatalf("VerifyOnce: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM source_verifications sv
JOIN sources s ON s.id = sv.source_id
WHERE s.tenant_id = $1 AND s.url = 'https://example.com/extracted'
`, tenantID).Scan(&count); err != nil {
		t.Fatalf("count verifications: %v", err)
	}
	if count == 0 {
		t.Fatal("expected extracted source to be verified")
	}
}

type rewriteTransport struct {
	targetScheme string
	targetHost   string
	base         http.RoundTripper
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL = cloneURL(req.URL)
	cloned.URL.Scheme = r.targetScheme
	cloned.URL.Host = r.targetHost
	cloned.Host = req.URL.Host
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}

func cloneURL(u *neturl.URL) *neturl.URL {
	copy := *u
	return &copy
}
