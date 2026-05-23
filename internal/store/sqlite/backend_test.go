package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/store"
)

func TestSQLiteStoreImplementsInterfaces(_ *testing.T) {
	var _ store.Store = (*Store)(nil)
	var _ store.VectorStore = (*Store)(nil)
}

func TestSQLiteStoreWrapsStoreQueries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t)
	tenantID := uuidFromString("00000000-0000-0000-0000-000000000092")
	otherTenantID := uuidFromString("00000000-0000-0000-0000-000000000192")

	mustExec(t, s, `INSERT INTO entities (id, tenant_id, ext_id, label, embedding) VALUES (?, ?, ?, ?, ?)`,
		"10000000-0000-0000-0000-000000000092", testUUIDString(tenantID), "sqlite-acme", "Acme", EncodeVector(vectorWith(1, 0)))
	mustExec(t, s, `INSERT INTO entities (id, tenant_id, ext_id, label, embedding) VALUES (?, ?, ?, ?, ?)`,
		"10000000-0000-0000-0000-000000000192", testUUIDString(otherTenantID), "sqlite-other", "Other", EncodeVector(vectorWith(1, 0)))
	mustExec(t, s, `INSERT INTO properties (id, tenant_id, slug, label, value_type) VALUES (?, ?, ?, ?, ?)`,
		"20000000-0000-0000-0000-000000000092", testUUIDString(tenantID), "founded", "Founded", "date")
	mustExec(t, s, `INSERT INTO properties (id, tenant_id, slug, label, value_type) VALUES (?, NULL, ?, ?, ?)`,
		"20000000-0000-0000-0000-000000000001", "source_url", "Source URL", "url")
	mustExec(t, s, `INSERT INTO sources (id, tenant_id, url, content_hash, status) VALUES (?, ?, ?, ?, ?)`,
		"30000000-0000-0000-0000-000000000092", testUUIDString(tenantID), "https://example.com/a", "hash-a", "collected")
	mustExec(t, s, `INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence) VALUES (?, ?, ?, ?, ?, ?)`,
		"40000000-0000-0000-0000-000000000092", testUUIDString(tenantID), "10000000-0000-0000-0000-000000000092", "20000000-0000-0000-0000-000000000092", "1999", "0.900")
	mustExec(t, s, `INSERT INTO qualifiers (id, statement_id, property_id, val_text) VALUES (?, ?, ?, ?)`,
		"50000000-0000-0000-0000-000000000092", "40000000-0000-0000-0000-000000000092", "20000000-0000-0000-0000-000000000001", "https://example.com/a")

	entities, err := s.ListEntitiesByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListEntitiesByTenant: %v", err)
	}
	if len(entities) != 1 || entities[0].Label != "Acme" {
		t.Fatalf("expected only tenant Acme entity, got %#v", entities)
	}

	properties, err := s.ListPropertiesByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListPropertiesByTenant: %v", err)
	}
	if len(properties) != 2 {
		t.Fatalf("expected tenant plus global property, got %#v", properties)
	}

	sources, err := s.ListSourcesByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListSourcesByTenant: %v", err)
	}
	if len(sources) != 1 || sources[0].Url != "https://example.com/a" {
		t.Fatalf("expected tenant source, got %#v", sources)
	}

	statements, err := s.ListStatementsBySubject(ctx, uuidFromString("10000000-0000-0000-0000-000000000092"))
	if err != nil {
		t.Fatalf("ListStatementsBySubject: %v", err)
	}
	if len(statements) != 1 || !statements[0].ValText.Valid || statements[0].ValText.String != "1999" {
		t.Fatalf("expected statement with text value, got %#v", statements)
	}

	qualifiers, err := s.ListQualifiersByStatement(ctx, uuidFromString("40000000-0000-0000-0000-000000000092"))
	if err != nil {
		t.Fatalf("ListQualifiersByStatement: %v", err)
	}
	if len(qualifiers) != 1 || !qualifiers[0].ValText.Valid || qualifiers[0].ValText.String != "https://example.com/a" {
		t.Fatalf("expected qualifier with text value, got %#v", qualifiers)
	}
}

func TestSQLiteStoreSearchNearest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t)
	tenantID := uuidFromString("00000000-0000-0000-0000-000000000292")
	otherTenantID := uuidFromString("00000000-0000-0000-0000-000000000392")
	near := vectorWith(1, 0)
	far := vectorWith(0, 1)

	mustExec(t, s, `INSERT INTO entities (id, tenant_id, ext_id, label, embedding) VALUES (?, ?, ?, ?, ?)`,
		"10000000-0000-0000-0000-000000000292", testUUIDString(tenantID), "sqlite-near", "Near", EncodeVector(near))
	mustExec(t, s, `INSERT INTO entities (id, tenant_id, ext_id, label, embedding) VALUES (?, ?, ?, ?, ?)`,
		"10000000-0000-0000-0000-000000000293", testUUIDString(tenantID), "sqlite-far", "Far", EncodeVector(far))
	mustExec(t, s, `INSERT INTO entities (id, tenant_id, ext_id, label, embedding) VALUES (?, ?, ?, ?, ?)`,
		"10000000-0000-0000-0000-000000000392", testUUIDString(otherTenantID), "sqlite-other", "Other Tenant", EncodeVector(near))

	got, err := s.SearchNearest(ctx, tenantID, near, 1)
	if err != nil {
		t.Fatalf("SearchNearest: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one result, got %d", len(got))
	}
	if got[0].Entity.Label != "Near" {
		t.Fatalf("expected nearest tenant entity, got %q", got[0].Entity.Label)
	}
	if got[0].Score < 0.99 {
		t.Fatalf("expected near-identical score, got %f", got[0].Score)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "factvault.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return s
}

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func uuidFromString(s string) pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		panic(err)
	}
	return id
}

func testUUIDString(id pgtype.UUID) string {
	return id.String()
}

func vectorWith(first, second float32) []float32 {
	v := make([]float32, 1024)
	v[0] = first
	v[1] = second
	return v
}
