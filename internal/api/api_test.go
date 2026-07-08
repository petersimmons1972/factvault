package api

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

const restrictedAPIAppName = "factvault-api-test"

func TestDossierRouteRequiresAndAcceptsJWT(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	entityID := uuid.NewString()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback (expected after commit): %v", err)
		}
	}()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entities (id, tenant_id, label, type_uri) VALUES ($1, $2, 'Acme', 'https://schema.org/Organization')`, entityID, tenantID); err != nil {
		t.Fatalf("insert entity: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	privPEM, pubPEM, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	priv, err := auth.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("private: %v", err)
	}
	pub, err := auth.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	token, err := auth.SignRS256(priv, auth.Claims{TenantID: tenantID, Subject: "tester", IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	h := New(pool, pub, "").Router()
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/entities/"+entityID+"/dossier", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized code=%d", unauth.Code)
	}
	authReq := httptest.NewRequest(http.MethodGet, "/entities/"+entityID+"/dossier", nil)
	authReq.Header.Set("Authorization", "Bearer "+token)
	authResp := httptest.NewRecorder()
	h.ServeHTTP(authResp, authReq)
	if authResp.Code != http.StatusOK {
		t.Fatalf("authorized code=%d body=%s", authResp.Code, authResp.Body.String())
	}
}

func TestPostBriefGenerateRejectsClientForgedBundle(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	entityID := uuid.NewString()
	insertBriefTestEntity(t, ctx, pool, tenantID, entityID)
	token, pub := newSignedToken(t, tenantID)

	h := New(pool, pub, "").Router()
	body := []byte(`{"source_kind":"dossier","entity_id":"` + entityID + `","bundle":{"entity_id":"` + entityID + `","tenant_id":"` + uuid.NewString() + `","entities":[],"statements":[],"sources":[],"assembled_at":"2026-01-01T00:00:00Z"}}`)
	req := httptest.NewRequest(http.MethodPost, "/briefs/generate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("generate code=%d body=%s", resp.Code, resp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/briefs", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listResp := httptest.NewRecorder()
	h.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list code=%d body=%s", listResp.Code, listResp.Body.String())
	}

	var briefs []map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&briefs); err != nil {
		t.Fatalf("decode briefs: %v", err)
	}
	if len(briefs) != 0 {
		t.Fatalf("expected forged bundle request to persist no briefs, got %d", len(briefs))
	}
}

func TestPostBriefGenerateAssemblesBundleServerSide(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	entityID := uuid.NewString()
	propertyID := uuid.NewString()
	statementID := uuid.NewString()
	sourceID := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback (expected after commit): %v", err)
		}
	}()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO properties (id, slug, label, value_type)
		VALUES ($1, 'role', 'Role', 'string')
	`, propertyID); err != nil {
		t.Fatalf("insert property: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, label, type_uri)
		VALUES ($1, $2, 'Acme', 'https://schema.org/Organization')
	`, entityID, tenantID); err != nil {
		t.Fatalf("insert entity: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence)
		VALUES ($1, $2, $3, $4, 'CEO', 0.92)
	`, statementID, tenantID, entityID, propertyID); err != nil {
		t.Fatalf("insert statement: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status)
		VALUES ($1, $2, 'https://example.com/acme-ceo', 'hash-acme-ceo', 'Acme announced its CEO.', 'verified')
	`, sourceID, tenantID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO statement_sources (id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id)
		VALUES (gen_random_uuid(), $1, $2, 'Acme announced its CEO.', 0, 22, 'test', 0.92, $3)
	`, statementID, sourceID, tenantID); err != nil {
		t.Fatalf("insert statement source: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	token, pub := newSignedToken(t, tenantID)
	h := New(pool, pub, "").Router()
	body := []byte(`{"source_kind":"dossier","entity_id":"` + entityID + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/briefs/generate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("generate code=%d body=%s", resp.Code, resp.Body.String())
	}

	var rec struct {
		ID      string `json:"id"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("expected persisted brief ID")
	}
	payloadBytes, err := base64.StdEncoding.DecodeString(rec.Payload)
	if err != nil {
		t.Fatalf("decode payload bytes: %v", err)
	}

	var payload struct {
		KeyClaims []struct {
			StatementID  string  `json:"statement_id"`
			EntityID     string  `json:"entity_id"`
			PropertySlug string  `json:"property_slug"`
			Value        string  `json:"value"`
			Confidence   float64 `json:"confidence"`
		} `json:"key_claims"`
		Citations []struct {
			SourceID           string `json:"source_id"`
			URL                string `json:"url"`
			VerificationStatus string `json:"verification_status"`
		} `json:"citations"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.KeyClaims) != 1 {
		t.Fatalf("expected 1 key claim, got %d payload=%s", len(payload.KeyClaims), payloadBytes)
	}
	if got := payload.KeyClaims[0]; got.StatementID != statementID || got.EntityID != entityID || got.PropertySlug != "role" || got.Value != "CEO" {
		t.Fatalf("unexpected key claim: %+v", got)
	}
	if len(payload.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d payload=%s", len(payload.Citations), payloadBytes)
	}
	if got := payload.Citations[0]; got.SourceID != sourceID || got.URL != "https://example.com/acme-ceo" || got.VerificationStatus != "verified" {
		t.Fatalf("unexpected citation: %+v", got)
	}
}

func TestPostBriefGenerateRestrictedPoolAllowsTenantOwnedEntity(t *testing.T) {
	ctx := context.Background()
	adminPool := testdb.Setup(ctx, t)
	defer adminPool.Close()
	restrictedPool := testdb.RestrictedPool(ctx, t, restrictedAPIAppName)
	defer restrictedPool.Close()

	tenantID := uuid.NewString()
	entityID := uuid.NewString()
	insertBriefTestEntity(t, ctx, adminPool, tenantID, entityID)
	token, pub := newSignedToken(t, tenantID)

	h := New(restrictedPool, pub, "").Router()
	body := []byte(`{"source_kind":"dossier","entity_id":"` + entityID + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/briefs/generate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with restricted pool, got %d body=%s", resp.Code, resp.Body.String())
	}
}

// TestPostBriefGenerate_ForeignEntityIDRejected verifies A-10: posting to
// /briefs/generate with a valid tenant-A JWT but a tenant-B entity_id must
// return HTTP 403 Forbidden, not 200 OK.
func TestPostBriefGenerate_ForeignEntityIDRejected(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	entityA := uuid.NewString()
	entityB := uuid.NewString()

	// Insert both entities in a committed transaction.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, label, type_uri) VALUES
		($1, $2, 'Entity A', 'https://schema.org/Thing'),
		($3, $4, 'Entity B', 'https://schema.org/Thing')
	`, entityA, tenantA, entityB, tenantB); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			t.Logf("rollback: %v", rbErr)
		}
		t.Fatalf("insert entities: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Sign a JWT for tenant-A.
	privPEM, pubPEM, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	priv, err := auth.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	pub, err := auth.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	tokenA, err := auth.SignRS256(priv, auth.Claims{
		TenantID:  tenantA,
		Subject:   "tester-a",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	h := New(pool, pub, "").Router()

	// POST with tenant-A's token but tenant-B's entity_id — must be 403.
	body := []byte(`{"source_kind":"dossier","entity_id":"` + entityB + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/briefs/generate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-tenant entity_id, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestPostBriefGenerateRejectsMalformedEntityID(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	token, pub := newSignedToken(t, tenantID)

	h := New(pool, pub, "").Router()
	body := []byte(`{"source_kind":"dossier","entity_id":"not-a-uuid"}`)
	req := httptest.NewRequest(http.MethodPost, "/briefs/generate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for malformed entity_id, got %d body=%s", resp.Code, resp.Body.String())
	}
}

// TestWriteError_InternalErrorDoesNotLeakDetail verifies X8: a 500 response must
// not expose raw err.Error() to the caller; it must include only a correlation ref.
func TestWriteError_InternalErrorDoesNotLeakDetail(t *testing.T) {
	internalMsg := "pq: relation \"secret_table\" does not exist"
	w := httptest.NewRecorder()
	writeError(w, errors.New(internalMsg))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var p Problem
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if strings.Contains(p.Detail, internalMsg) {
		t.Fatalf("detail %q leaks internal error message (X8 violation)", p.Detail)
	}
	if !strings.HasPrefix(p.Detail, "ref: ") {
		t.Fatalf("detail %q does not contain correlation ref", p.Detail)
	}
}

// TestWriteError_NotFoundDoesNotLeakDetail verifies X8: 404 responses must use a
// static detail string, not err.Error(), to avoid leaking internal identifiers.
func TestWriteError_NotFoundDoesNotLeakDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrEntityNotFound", assembler.ErrEntityNotFound},
		{"pgx.ErrNoRows", pgx.ErrNoRows},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeError(w, tc.err)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
			var p Problem
			if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if p.Detail != "" {
				t.Fatalf("detail = %q, want empty (no internal message should be returned)", p.Detail)
			}
		})
	}
}

// TestReadyzReturns503WhenNotReady verifies X7: GET /readyz must return 503
// (not 200) when the database pool is nil, so load balancers and k8s readiness
// probes correctly remove the pod from rotation before the DB is available.
func TestReadyzReturns503WhenNotReady(t *testing.T) {
	// Construct a server with no pool — simulates startup before DB is connected.
	h := New(nil, nil, "").Router()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz (no pool) = %d, want 503", w.Code)
	}
}

// TestReadyzReturns200WhenReady verifies the happy path: with a live pool,
// /readyz returns 200 OK with ready=true.
func TestReadyzReturns200WhenReady(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	h := New(pool, nil, "").Router()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /readyz (live pool) = %d, want 200", w.Code)
	}
}

// TestPostStory_MaxBytesReaderEnforced verifies M1: a request body exceeding 1 MiB
// to an authenticated POST handler must be rejected with 400 Bad Request, not cause
// OOM/hang. Uses a nil pool because the middleware fires before any DB access.
func TestPostStory_MaxBytesReaderEnforced(t *testing.T) {
	privPEM, pubPEM, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	priv, err := auth.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("parse private: %v", err)
	}
	pub, err := auth.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("parse public: %v", err)
	}
	token, err := auth.SignRS256(priv, auth.Claims{
		TenantID:  "00000000-0000-0000-0000-000000000001",
		Subject:   "tester",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Body is 2 MiB — double the 1 MiB limit.
	oversizedBody := bytes.Repeat([]byte("x"), 2<<20)

	h := New(nil, pub, "").Router()
	req := httptest.NewRequest(http.MethodPost, "/stories", bytes.NewReader(oversizedBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: got %d, want 400 (MaxBytesReader enforcement)", w.Code)
	}
}

func insertBriefTestEntity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, entityID string) {
	t.Helper()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback (expected after commit): %v", err)
		}
	}()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entities (id, tenant_id, label, type_uri) VALUES ($1, $2, 'Acme', 'https://schema.org/Organization')`, entityID, tenantID); err != nil {
		t.Fatalf("insert entity: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func newSignedToken(t *testing.T, tenantID string) (string, *rsa.PublicKey) {
	t.Helper()

	privPEM, pubPEM, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	priv, err := auth.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	pub, err := auth.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	token, err := auth.SignRS256(priv, auth.Claims{
		TenantID:  tenantID,
		Subject:   "tester",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return token, pub
}
