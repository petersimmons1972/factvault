package mcpserver

import (
	"context"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestEntityLookupRequiresAuthorization(t *testing.T) {
	pool := testdb.Setup(context.Background(), t)
	server := New(pool, mustPublicKey(t), "", "")

	_, _, err := server.entityLookup(context.Background(), nil, EntityLookupArgs{EntityID: uuid.NewString()})
	if err == nil {
		t.Fatal("expected missing authorization error")
	}
}

func TestEntityLookupUsesAuthorizedTenant(t *testing.T) {
	pool := testdb.Setup(context.Background(), t)

	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	entityA := uuid.NewString()
	entityB := uuid.NewString()

	_, publicKey, tokenA := mustSignedToken(t, tenantA)
	server := New(pool, publicKey, "", "")

	_, err := pool.Exec(context.Background(),
		"INSERT INTO entities (id, tenant_id, label) VALUES ($1, $2, 'Tenant A'), ($3, $4, 'Tenant B')",
		entityA, tenantA, entityB, tenantB,
	)
	if err != nil {
		t.Fatalf("insert entities: %v", err)
	}

	_, _, err = server.entityLookup(context.Background(), nil, EntityLookupArgs{
		Authorization: "Bearer " + tokenA,
		EntityID:      entityA,
	})
	if err != nil {
		t.Fatalf("entity lookup own tenant failed: %v", err)
	}

	_, _, err = server.entityLookup(context.Background(), nil, EntityLookupArgs{
		Authorization: "Bearer " + tokenA,
		EntityID:      entityB,
	})
	if err == nil {
		t.Fatal("expected tenant A token to be denied on tenant B entity")
	}
	if !errors.Is(err, assembler.ErrEntityNotFound) {
		t.Fatalf("expected entity not found error, got %T %v", err, err)
	}
}

// TestTenantFromAuthorization_EmptyAuthReturnsError verifies S-03: empty
// authorization must be rejected immediately even when DefaultToken is set.
// Previously the code fell back to DefaultToken on empty auth, which meant a
// caller could be silently authenticated as the default tenant.
func TestTenantFromAuthorization_EmptyAuthReturnsError(t *testing.T) {
	pool := testdb.Setup(context.Background(), t)
	// Pass a non-empty DefaultToken; empty authorization must still be rejected.
	server := New(pool, mustPublicKey(t), "someDefaultToken", "")

	_, err := server.tenantFromAuthorization("")
	if err == nil {
		t.Fatal("expected an error for empty authorization, got nil")
	}
	if !strings.Contains(err.Error(), "missing authorization token") {
		t.Fatalf("expected 'missing authorization token', got: %v", err)
	}
}

func mustPublicKey(t *testing.T) *rsa.PublicKey {
	t.Helper()
	_, pubPEM, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	publicKey, err := auth.ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	return publicKey
}

func mustSignedToken(t *testing.T, tenantID string) (*rsa.PrivateKey, *rsa.PublicKey, string) {
	t.Helper()
	privatePEM, publicPEM, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	privateKey, err := auth.ParsePrivateKeyPEM(privatePEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	publicKey, err := auth.ParsePublicKeyPEM(publicPEM)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	now := time.Now().UTC()
	token, err := auth.SignRS256(privateKey, auth.Claims{
		TenantID:  tenantID,
		Subject:   "tenant-a",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return privateKey, publicKey, token
}
