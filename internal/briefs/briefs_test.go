package briefs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

func testBundle(tenantID, entityID string) *assembler.Bundle {
	return &assembler.Bundle{
		TenantID:   tenantID,
		EntityID:   entityID,
		Statements: []assembler.BundleStatement{{ID: "s1", EntityID: entityID, PropertySlug: "role", Value: "CEO", Confidence: 0.9, SourceIDs: []string{"src1"}}, {ID: "s2", EntityID: entityID, PropertySlug: "role", Value: "CTO", Confidence: 0.8}},
		Sources:    []assembler.BundleSource{{ID: "src1", URL: "https://example.com/1", VerificationStatus: "verified"}},
	}
}

func TestGenerateDeterministic(t *testing.T) {
	g := BriefGenerator{}
	b := testBundle("t", "e1")
	a, err := g.Generate(b)
	if err != nil {
		t.Fatalf("generate a: %v", err)
	}
	c, err := g.Generate(b)
	if err != nil {
		t.Fatalf("generate c: %v", err)
	}
	if string(a) != string(c) {
		t.Fatalf("expected deterministic output")
	}

	var parsed map[string]any
	if err := json.Unmarshal(a, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"key_claims", "citations", "conflicts", "source_health", "evidence_gaps", "writer_prompts"} {
		if _, ok := parsed[key]; !ok {
			t.Fatalf("missing key %q", key)
		}
	}
}

func TestServiceGenerateListGetTenantScoped(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	entityID := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantA); err != nil {
		t.Fatalf("tenant set: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entities (id, tenant_id, label, type_uri) VALUES ($1,$2,'Acme','https://schema.org/Organization')`, entityID, tenantA); err != nil {
		t.Fatalf("insert entity: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	svc := Service{
		Pool: pool,
		BundleLoader: BundleLoaderFunc(func(context.Context, string, GenerateRequest) (*assembler.Bundle, error) {
			return testBundle(tenantA, entityID), nil
		}),
	}
	rec, err := svc.GenerateAndStore(ctx, tenantA, GenerateRequest{SourceKind: SourceKindDossier, EntityID: &entityID})
	if err != nil {
		t.Fatalf("generate/store: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("missing id")
	}

	listA, err := svc.List(ctx, tenantA, ListOptions{Limit: 10})
	if err != nil || len(listA) != 1 {
		t.Fatalf("listA err=%v len=%d", err, len(listA))
	}
	got, err := svc.Get(ctx, tenantA, rec.ID)
	if err != nil || got.ID != rec.ID {
		t.Fatalf("get err=%v got=%+v", err, got)
	}
	if _, err := svc.Get(ctx, tenantB, rec.ID); err == nil {
		t.Fatal("expected cross-tenant get denial")
	}
}

func TestServiceListFilters(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()
	tenantID := uuid.NewString()
	entityA := uuid.NewString()
	entityB := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("tenant set: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entities (id, tenant_id, ext_id, label, type_uri) VALUES ($1,$2,'entity-a','A','https://schema.org/Organization'),($3,$2,'entity-b','B','https://schema.org/Organization')`, entityA, tenantID, entityB); err != nil {
		t.Fatalf("insert entities: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	svc := Service{
		Pool: pool,
		BundleLoader: BundleLoaderFunc(func(_ context.Context, tenant string, req GenerateRequest) (*assembler.Bundle, error) {
			entity := entityA
			if req.EntityID != nil {
				entity = *req.EntityID
			}
			return testBundle(tenant, entity), nil
		}),
	}
	if _, err := svc.GenerateAndStore(ctx, tenantID, GenerateRequest{SourceKind: SourceKindDossier, EntityID: &entityA}); err != nil {
		t.Fatalf("store dossier: %v", err)
	}
	q := "sec investigations"
	if _, err := svc.GenerateAndStore(ctx, tenantID, GenerateRequest{SourceKind: SourceKindStory, EntityID: &entityB, Query: &q}); err != nil {
		t.Fatalf("store story: %v", err)
	}

	kind := SourceKindDossier
	rows, err := svc.List(ctx, tenantID, ListOptions{Limit: 10, SourceKind: &kind})
	if err != nil {
		t.Fatalf("list by source_kind: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceKind != SourceKindDossier {
		t.Fatalf("unexpected source_kind rows=%+v", rows)
	}

	rows, err = svc.List(ctx, tenantID, ListOptions{Limit: 10, EntityID: &entityB})
	if err != nil {
		t.Fatalf("list by entity: %v", err)
	}
	if len(rows) != 1 || rows[0].EntityID == nil || *rows[0].EntityID != entityB {
		t.Fatalf("unexpected entity filter rows=%+v", rows)
	}
}

func TestGenerateAndStoreRejectsMismatchedBundleTenant(t *testing.T) {
	svc := Service{}

	entityID := uuid.NewString()
	_, err := svc.generateAndStoreBundle(context.Background(), "tenant-a", GenerateRequest{
		SourceKind: SourceKindDossier,
		EntityID:   &entityID,
	}, testBundle("tenant-b", entityID))
	if !errors.Is(err, ErrBundleTenantMismatch) {
		t.Fatalf("expected ErrBundleTenantMismatch, got %v", err)
	}
}

func TestGenerateAndStoreRejectsMismatchedBundleEntity(t *testing.T) {
	svc := Service{}

	entityID := uuid.NewString()
	bundle := testBundle("tenant-a", uuid.NewString())
	_, err := svc.generateAndStoreBundle(context.Background(), "tenant-a", GenerateRequest{
		SourceKind: SourceKindDossier,
		EntityID:   &entityID,
	}, bundle)
	if !errors.Is(err, ErrBundleEntityMismatch) {
		t.Fatalf("expected ErrBundleEntityMismatch, got %v", err)
	}
}

func TestGenerateAndStoreRequiresBundleLoader(t *testing.T) {
	svc := Service{}

	entityID := uuid.NewString()
	_, err := svc.GenerateAndStore(context.Background(), "tenant-a", GenerateRequest{
		SourceKind: SourceKindDossier,
		EntityID:   &entityID,
	})
	if !errors.Is(err, ErrBundleLoaderRequired) {
		t.Fatalf("expected ErrBundleLoaderRequired, got %v", err)
	}
}
