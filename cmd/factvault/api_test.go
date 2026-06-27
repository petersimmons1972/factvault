package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	internalapi "github.com/petersimmons1972/factvault/internal/api"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

// TestAPICmdAddrFlag verifies the --addr flag is registered and defaults to :8080.
// The env-fallback and flag-override logic is handled by config.ResolveString
// (tested in internal/config/contract_test.go and resolver_test.go).
func TestAPICmdAddrFlag(t *testing.T) {
	cmd := newAPICmd()
	f := cmd.Flags().Lookup("addr")
	if f == nil {
		t.Fatal("newAPICmd() missing --addr flag")
		return // unreachable; helps static analysis
	}
	if f.DefValue != ":8080" {
		t.Fatalf("--addr default = %q, want :8080", f.DefValue)
	}
}

// TestAPICmdEmbedderURLDefault verifies the --embedder-url flag is not present
// on the API command (embedder URL is resolved inside RunE, not exposed as a flag).
func TestAPICmdJWTPublicKeyFlag(t *testing.T) {
	cmd := newAPICmd()
	f := cmd.Flags().Lookup("jwt-public-key")
	if f == nil {
		t.Fatal("newAPICmd() missing --jwt-public-key flag")
	}
}

func TestBriefsLimitCapped(t *testing.T) {
	handler, token := seedBriefsAPI(t, 1005)

	req := httptest.NewRequest(http.MethodGet, "/briefs?limit=9999999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /briefs code=%d body=%s", resp.Code, resp.Body.String())
	}

	var briefs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&briefs); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(briefs) > 1000 {
		t.Fatalf("len(briefs)=%d want <= 1000", len(briefs))
	}
}

func TestBriefsLimitRespected(t *testing.T) {
	handler, token := seedBriefsAPI(t, 75)

	req := httptest.NewRequest(http.MethodGet, "/briefs?limit=50", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /briefs code=%d body=%s", resp.Code, resp.Body.String())
	}

	var briefs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&briefs); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(briefs) != 50 {
		t.Fatalf("len(briefs)=%d want 50", len(briefs))
	}
}

func seedBriefsAPI(t *testing.T, total int) (http.Handler, string) {
	t.Helper()

	ctx := context.Background()
	pool := testdb.Setup(ctx, t)

	tenantID := "11111111-1111-1111-1111-111111111111"
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
	for i := range total {
		if _, err := tx.Exec(ctx, `
			INSERT INTO evidence_briefs (tenant_id, source_kind, query, payload)
			VALUES ($1::uuid, 'story', $2, '{"items":[]}'::jsonb)
		`, tenantID, fmt.Sprintf("brief-%04d", i)); err != nil {
			t.Fatalf("insert brief %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	privPEM, pubPEM, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keys: %v", err)
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
		t.Fatalf("sign token: %v", err)
	}

	return internalapi.New(pool, pub, "").Router(), token
}
