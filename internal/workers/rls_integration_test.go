package workers_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/briefs"
	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
	"github.com/petersimmons1972/factvault/internal/testdb"
	"github.com/petersimmons1972/factvault/internal/workers"
)

const restrictedAppName = "factvault-test"

func TestRLSRestrictedCollectOnceSucceedsWithTenantContext(t *testing.T) {
	ctx := context.Background()
	pool := testdb.RestrictedPool(ctx, t, restrictedAppName)
	assertRestrictedRoleNoBypassRLS(ctx, t, pool)

	tenantID := uuid.NewString()
	pipeline := &workers.SourcePipeline{DB: pool}
	collector := collectors.StaticCollector{
		CollectorName: "restricted-collect",
		Items: []collectors.Item{
			{URL: "https://example.com/restricted/collect", HTML: []byte("<html><body>alpha</body></html>")},
		},
	}

	if err := pipeline.CollectOnce(ctx, tenantID, collector); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		var count int
		if err := tx.QueryRow(txCtx, `
			SELECT count(*)
			FROM sources
			WHERE tenant_id = $1::uuid AND url = 'https://example.com/restricted/collect'
		`, tenantID).Scan(&count); err != nil {
			t.Fatalf("count sources: %v", err)
		}
		if count == 0 {
			t.Fatal("expected collected source row under tenant context")
		}
	})
}

func TestRLSRestrictedCollectOnceFailsWithoutTenantContext(t *testing.T) {
	ctx := context.Background()
	pool := testdb.RestrictedPool(ctx, t, restrictedAppName)
	assertRestrictedRoleNoBypassRLS(ctx, t, pool)

	tenantID := uuid.NewString()
	legacy := legacySourcePipeline{DB: pool}
	collector := collectors.StaticCollector{
		CollectorName: "legacy-collect",
		Items: []collectors.Item{
			{URL: "https://example.com/restricted/legacy", HTML: []byte("<html><body>legacy</body></html>")},
		},
	}

	err := legacy.CollectOnce(ctx, tenantID, collector)
	visibleRows := -1

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		var count int
		if queryErr := tx.QueryRow(txCtx, `
			SELECT count(*)
			FROM sources
			WHERE tenant_id = $1::uuid AND url = 'https://example.com/restricted/legacy'
		`, tenantID).Scan(&count); queryErr != nil {
			t.Fatalf("count sources: %v", queryErr)
		}
		visibleRows = count
		if err == nil && count != 0 {
			t.Fatalf("legacy collect unexpectedly inserted %d row(s) without tenant context", count)
		}
		if err != nil && count != 0 {
			t.Fatalf("legacy collect returned error but still inserted %d row(s)", count)
		}
	})

	if err == nil && visibleRows != 0 {
		t.Fatal("expected legacy collect path without tenant context to fail or be invisible under RLS")
	}
}

func TestRLSRestrictedArchiveOnceSucceedsWithTenantContext(t *testing.T) {
	ctx := context.Background()
	pool := testdb.RestrictedPool(ctx, t, restrictedAppName)
	assertRestrictedRoleNoBypassRLS(ctx, t, pool)

	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawHTML := []byte("<html><body>archive me</body></html>")
	compressed := mustCompress(t, rawHTML)

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(txCtx, `
			INSERT INTO sources (id, tenant_id, url, content_hash, raw_html, status, title)
			VALUES ($1, $2::uuid, $3, $4, $5, 'collected', 'archive me')
		`, sourceID, tenantID, "https://example.com/restricted/archive", sha256Hex(rawHTML), compressed); err != nil {
			t.Fatalf("insert collected source: %v", err)
		}
	})

	pipeline := &workers.SourcePipeline{DB: pool}
	if err := pipeline.ArchiveOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ArchiveOnce: %v", err)
	}

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		var status string
		var rawText string
		if err := tx.QueryRow(txCtx, `
			SELECT status, raw_text
			FROM sources
			WHERE id = $1::uuid
		`, sourceID).Scan(&status, &rawText); err != nil {
			t.Fatalf("select archived source: %v", err)
		}
		if status != "archived" {
			t.Fatalf("status=%q want archived", status)
		}
		if rawText == "" {
			t.Fatal("expected raw_text to be populated after archive")
		}
	})
}

func TestRLSRestrictedVerifyOnceSucceedsWithTenantContext(t *testing.T) {
	ctx := context.Background()
	pool := testdb.RestrictedPool(ctx, t, restrictedAppName)
	assertRestrictedRoleNoBypassRLS(ctx, t, pool)

	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	body := []byte("<html><body>verify me</body></html>")
	sourceURL := "https://example.com/restricted/verify"

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(txCtx, `
			INSERT INTO sources (id, tenant_id, url, content_hash, status, title)
			VALUES ($1, $2::uuid, $3, $4, 'archived', 'verify me')
		`, sourceID, tenantID, sourceURL, sha256Hex(body)); err != nil {
			t.Fatalf("insert archived source: %v", err)
		}
	})

	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	pipeline := &workers.SourcePipeline{DB: pool, HTTPClient: httpClient}
	if err := pipeline.VerifyOnce(ctx, tenantID, 0, 10); err != nil {
		t.Fatalf("VerifyOnce: %v", err)
	}

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		var status string
		var verifiedAt time.Time
		if err := tx.QueryRow(txCtx, `
			SELECT status, last_verified_at
			FROM sources
			WHERE id = $1::uuid
		`, sourceID).Scan(&status, &verifiedAt); err != nil {
			t.Fatalf("select verified source: %v", err)
		}
		if status != "verified" {
			t.Fatalf("status=%q want verified", status)
		}
		if verifiedAt.IsZero() {
			t.Fatal("expected last_verified_at to be updated")
		}

		var verificationStatus string
		if err := tx.QueryRow(txCtx, `
			SELECT status
			FROM source_verifications
			WHERE source_id = $1::uuid
			ORDER BY verified_at DESC
			LIMIT 1
		`, sourceID).Scan(&verificationStatus); err != nil {
			t.Fatalf("select verification row: %v", err)
		}
		if verificationStatus != "live" {
			t.Fatalf("verification status=%q want live", verificationStatus)
		}
	})
}

func TestRLSRestrictedBriefsTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.RestrictedPool(ctx, t, restrictedAppName)
	assertRestrictedRoleNoBypassRLS(ctx, t, pool)

	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	query := "acme expansion"
	svc := briefs.Service{Pool: pool}

	rec, err := svc.GenerateAndStore(ctx, tenantA, briefs.GenerateRequest{
		SourceKind: briefs.SourceKindStory,
		Query:      &query,
		Bundle:     rlsTestBundle(tenantA),
	})
	if err != nil {
		t.Fatalf("GenerateAndStore: %v", err)
	}

	got, err := svc.Get(ctx, tenantA, rec.ID)
	if err != nil {
		t.Fatalf("Get same tenant: %v", err)
	}
	if got.ID != rec.ID {
		t.Fatalf("Get returned id=%q want %q", got.ID, rec.ID)
	}

	_, err = svc.Get(ctx, tenantB, rec.ID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected wrapped pgx.ErrNoRows for cross-tenant get, got %v", err)
	}
}

func TestRLSRestrictedExtractOnceDoesNotHoldIdleTransactionDuringLLMCall(t *testing.T) {
	ctx := context.Background()
	pool := testdb.RestrictedPool(ctx, t, restrictedAppName)
	assertRestrictedRoleNoBypassRLS(ctx, t, pool)

	monitorPool := testdb.New(t)
	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawText := "Acme Corp launched a product."

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(txCtx, `
			INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, title)
			VALUES ($1, $2::uuid, $3, $4, $5, 'archived', 'Acme launch')
		`, sourceID, tenantID, "https://example.com/restricted/extract", "extract-hash", rawText); err != nil {
			t.Fatalf("insert archived source: %v", err)
		}
	})

	llm := newSlowLLMStub(100 * time.Millisecond)
	pipeline := &workers.FactPipeline{
		DB:            pool,
		Deterministic: noopExtractor{},
		LLM:           llm,
	}

	errCh := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		errCh <- pipeline.ExtractOnce(ctx, tenantID, 10)
		close(done)
	}()

	select {
	case <-llm.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow LLM call to start")
	}

	var idleSeen atomic.Int64
	pollErrCh := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				pollErrCh <- nil
				return
			case <-ticker.C:
				var idleCount int
				if err := monitorPool.QueryRow(ctx, `
					SELECT count(*)
					FROM pg_stat_activity
					WHERE state = 'idle in transaction'
					  AND application_name = $1
				`, restrictedAppName).Scan(&idleCount); err != nil {
					pollErrCh <- fmt.Errorf("poll pg_stat_activity: %w", err)
					return
				}
				if idleCount > 0 {
					idleSeen.Store(int64(idleCount))
				}
			}
		}
	}()

	if err := <-errCh; err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}
	if err := <-pollErrCh; err != nil {
		t.Fatal(err)
	}
	if idleSeen.Load() != 0 {
		t.Fatalf("expected no idle-in-transaction sessions during LLM call, saw %d", idleSeen.Load())
	}
}

// TestRLSRestrictedExtractOnceReusesGlobalProperty (issue #269) verifies
// that FactPipeline.ExtractOnce, running under a tenant-scoped app_user
// transaction (RLS enforced, not BYPASSRLS), can locate and reuse an
// existing global (tenant_id IS NULL) property when extracting a fact whose
// slug matches a shared-vocabulary entry. Before the properties RLS fix
// (migration 00008), the SELECT in ensureProperty (internal/workers/extract.go)
// would never see the global row under RLS, so extraction would instead
// create a duplicate tenant-scoped property with the same slug — defeating
// shared vocabulary reuse.
func TestRLSRestrictedExtractOnceReusesGlobalProperty(t *testing.T) {
	ctx := context.Background()
	pool := testdb.RestrictedPool(ctx, t, restrictedAppName)
	assertRestrictedRoleNoBypassRLS(ctx, t, pool)

	adminPool := testdb.New(t)
	globalPropertyID := uuid.NewString()
	const globalSlug = "sec_cik" // known catalog slug (vocabulary.defaultCatalog), resolves the same in strict or permissive mode.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES ($1, NULL, $2, 'SEC CIK', 'string')
	`, globalPropertyID, globalSlug); err != nil {
		t.Fatalf("seed global property: %v", err)
	}

	tenantID := uuid.NewString()
	sourceID := uuid.NewString()
	rawText := "Acme Corp filed SEC CIK 0000320193 today."
	excerpt := "0000320193"
	offsetStart := strings.Index(rawText, excerpt)
	if offsetStart < 0 {
		t.Fatalf("excerpt %q not found in rawText %q", excerpt, rawText)
	}
	offsetEnd := offsetStart + len(excerpt)

	withTenantTx(ctx, t, pool, tenantID, func(txCtx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(txCtx, `
			INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, title)
			VALUES ($1, $2::uuid, $3, $4, $5, 'archived', 'Acme SEC filing')
		`, sourceID, tenantID, "https://example.com/restricted/extract-global-property", "extract-global-hash", rawText); err != nil {
			t.Fatalf("insert archived source: %v", err)
		}
	})

	pipeline := &workers.FactPipeline{
		DB: pool,
		Deterministic: fixedFactExtractor{facts: []extractors.ExtractedFact{
			{
				SubjectText:        "Acme Corp",
				PropertySlug:       "SEC CIK",
				Value:              excerpt,
				ValueType:          "string",
				Excerpt:            excerpt,
				ExcerptOffsetStart: offsetStart,
				ExcerptOffsetEnd:   offsetEnd,
				ExtractionMethod:   "test-fixed",
			},
		}},
	}

	if err := pipeline.ExtractOnce(ctx, tenantID, 10); err != nil {
		t.Fatalf("ExtractOnce: %v", err)
	}

	var statementCount int
	var usedPropertyID string
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*), max(property_id::text)
		FROM statements
		WHERE tenant_id = $1::uuid
	`, tenantID).Scan(&statementCount, &usedPropertyID); err != nil {
		t.Fatalf("query statements: %v", err)
	}
	if statementCount != 1 {
		t.Fatalf("statement count=%d want 1", statementCount)
	}
	if usedPropertyID != globalPropertyID {
		t.Fatalf("statement property_id=%s want global property %s (global property was not reused)", usedPropertyID, globalPropertyID)
	}

	var propertyRowsWithSlug int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM properties WHERE slug = $1
	`, globalSlug).Scan(&propertyRowsWithSlug); err != nil {
		t.Fatalf("count properties with slug: %v", err)
	}
	if propertyRowsWithSlug != 1 {
		t.Fatalf("properties with slug %q=%d want 1 (extraction should not have cloned a tenant-scoped duplicate)", globalSlug, propertyRowsWithSlug)
	}
}

type fixedFactExtractor struct {
	facts []extractors.ExtractedFact
}

func (f fixedFactExtractor) Extract(context.Context, *db.Source, string) ([]extractors.ExtractedFact, error) {
	return f.facts, nil
}

type legacySourcePipeline struct {
	DB *pgxpool.Pool
}

func (p legacySourcePipeline) CollectOnce(ctx context.Context, tenantID string, c collectors.Collector) error {
	items, err := c.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	for _, item := range items {
		if strings.TrimSpace(item.URL) == "" || len(item.HTML) == 0 {
			continue
		}
		if _, err := p.DB.Exec(ctx, `
			INSERT INTO sources (id, tenant_id, url, content_hash, raw_html, status, meta)
			VALUES ($1, $2::uuid, $3, $4, $5, 'collected', '{}'::jsonb)
			ON CONFLICT (tenant_id, url) DO NOTHING
		`, uuid.NewString(), tenantID, item.URL, sha256Hex(item.HTML), item.HTML); err != nil {
			return fmt.Errorf("insert source: %w", err)
		}
	}
	return nil
}

type noopExtractor struct{}

func (noopExtractor) Extract(context.Context, *db.Source, string) ([]extractors.ExtractedFact, error) {
	return nil, nil
}

type slowLLMStub struct {
	sleep time.Duration

	started chan struct{}
	once    sync.Once
}

func newSlowLLMStub(sleep time.Duration) *slowLLMStub {
	return &slowLLMStub{
		sleep:   sleep,
		started: make(chan struct{}),
	}
}

func (s *slowLLMStub) Extract(context.Context, *db.Source, string) ([]extractors.StatementProposal, error) {
	s.once.Do(func() {
		close(s.started)
	})
	time.Sleep(s.sleep)
	return nil, nil
}

func assertRestrictedRoleNoBypassRLS(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	var bypass bool
	if err := pool.QueryRow(ctx, `
		SELECT rolbypassrls
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(&bypass); err != nil {
		t.Fatalf("query rolbypassrls: %v", err)
	}
	if bypass {
		t.Fatal("restricted pool unexpectedly bypasses RLS")
	}
}

func withTenantTx(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tenantID string, fn func(context.Context, pgx.Tx)) {
	t.Helper()

	var tenant pgtype.UUID
	if err := tenant.Scan(tenantID); err != nil {
		t.Fatalf("scan tenant uuid: %v", err)
	}
	txCtx := db.WithPool(ctx, pool)
	txCtx, tx, err := db.TenantContext(txCtx, tenant)
	if err != nil {
		t.Fatalf("TenantContext: %v", err)
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil && !isRollbackAfterCommit(err) {
			t.Fatalf("rollback tenant tx: %v", err)
		}
	}()

	fn(txCtx, tx)

	if err := tx.Commit(txCtx); err != nil {
		t.Fatalf("commit tenant tx: %v", err)
	}
}

func isRollbackAfterCommit(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "tx is closed") || strings.Contains(err.Error(), "already been closed"))
}

func mustCompress(t *testing.T, b []byte) []byte {
	t.Helper()

	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(b); err != nil {
		t.Fatalf("compress write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("compress close: %v", err)
	}
	return out.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func rlsTestBundle(tenantID string) *assembler.Bundle {
	return &assembler.Bundle{
		TenantID: tenantID,
		EntityID: uuid.NewString(),
		Statements: []assembler.BundleStatement{
			{
				ID:           uuid.NewString(),
				EntityID:     uuid.NewString(),
				PropertySlug: "role",
				Value:        "CEO",
				Confidence:   0.9,
				SourceIDs:    []string{uuid.NewString()},
			},
		},
		Sources: []assembler.BundleSource{
			{
				ID:                 uuid.NewString(),
				URL:                "https://example.com/brief",
				VerificationStatus: "verified",
			},
		},
	}
}
