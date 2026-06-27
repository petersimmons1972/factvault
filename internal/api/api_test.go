package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

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

func TestBriefRoutesTenantScoped(t *testing.T) {
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
		t.Fatalf("parse private key: %v", err)
	}
	pub, err := auth.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	token, err := auth.SignRS256(priv, auth.Claims{TenantID: tenantID, Subject: "tester", IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	h := New(pool, pub, "").Router()
	body := []byte(`{"source_kind":"dossier","entity_id":"` + entityID + `","bundle":{"entity_id":"` + entityID + `","tenant_id":"` + tenantID + `","entities":[],"statements":[],"sources":[],"assembled_at":"2026-01-01T00:00:00Z"}}`)
	req := httptest.NewRequest(http.MethodPost, "/briefs/generate", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("generate code=%d body=%s", resp.Code, resp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/briefs", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listResp := httptest.NewRecorder()
	h.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list code=%d body=%s", listResp.Code, listResp.Body.String())
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
