package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	defer tx.Rollback(ctx)
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

	h := New(pool, pub).Router()
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
