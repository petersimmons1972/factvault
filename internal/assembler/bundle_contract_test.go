package assembler

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

// moduleRoot walks up from this test file until go.mod is found.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root (no go.mod walking up from bundle_contract_test.go)")
		}
		dir = parent
	}
}

type openAPIDoc struct {
	Components struct {
		Schemas map[string]map[string]any `yaml:"schemas"`
	} `yaml:"components"`
}

func loadOpenAPIBundleSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "docs", "api", "openapi.yaml")
	raw, err := os.ReadFile(path) //nolint:gosec // test reads repo-relative OpenAPI path
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	var doc openAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	needed := []string{
		"Bundle", "BundleEntity", "BundleStatement",
		"BundleSource", "BundleQualifier", "BundleRelation",
	}
	out := make(map[string]map[string]any, len(needed))
	for _, name := range needed {
		schema, ok := doc.Components.Schemas[name]
		if !ok {
			t.Fatalf("openapi.yaml missing components.schemas.%s", name)
		}
		out[name] = schema
	}
	return out
}

func schemaPropertyNames(schema map[string]any) map[string]struct{} {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return map[string]struct{}{}
	}
	names := make(map[string]struct{}, len(props))
	for k := range props {
		names[k] = struct{}{}
	}
	return names
}

func schemaRequiredNames(schema map[string]any) []string {
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func objectKeys(m map[string]any) map[string]struct{} {
	keys := make(map[string]struct{}, len(m))
	for k := range m {
		keys[k] = struct{}{}
	}
	return keys
}

func assertKeysMatchSchema(t *testing.T, path string, jsonObj map[string]any, schema map[string]any) {
	t.Helper()
	schemaProps := schemaPropertyNames(schema)
	jsonKeys := objectKeys(jsonObj)

	for key := range jsonKeys {
		if _, ok := schemaProps[key]; !ok {
			t.Errorf("%s: marshaled JSON has key %q not present in OpenAPI schema properties", path, key)
		}
	}
	for _, req := range schemaRequiredNames(schema) {
		if _, ok := jsonKeys[req]; !ok {
			t.Errorf("%s: OpenAPI required field %q missing from marshaled JSON", path, req)
		}
		if _, ok := schemaProps[req]; !ok {
			t.Errorf("%s: OpenAPI lists %q as required but it is not in properties", path, req)
		}
	}
}

func firstObject(t *testing.T, path string, v any) map[string]any {
	t.Helper()
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("%s: expected non-empty array of objects", path)
	}
	obj, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("%s: expected object element, got %T", path, arr[0])
	}
	return obj
}

// TestBundleOpenAPIContractAssembledJSONMatchesSchema builds a real Bundle via
// Assemble, marshals it, and diffs keys against docs/api/openapi.yaml.
// Fully-populated fixtures ensure omitempty fields appear so optional schema
// properties are also exercised when present.
func TestBundleOpenAPIContractAssembledJSONMatchesSchema(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()

	tenantID := uuid.NewString()
	entityA := uuid.NewString()
	entityB := uuid.NewString()
	propID := uuid.NewString()
	qualPropID := uuid.NewString()
	stmtID := uuid.NewString()
	sourceID := uuid.NewString()
	qualifierID := uuid.NewString()
	relationID := uuid.NewString()
	ssID := uuid.NewString()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("rollback: %v", err)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO properties (id, slug, label, value_type)
		VALUES
			($1, 'acquired', 'Acquired', 'string'),
			($2, 'as_of', 'As of', 'string')
	`, propID, qualPropID); err != nil {
		t.Fatalf("insert properties: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label, type_uri, description, created_at, updated_at)
		VALUES
			($1, $3, 'megacorp', 'MegaCorp', 'https://schema.org/Organization', 'Acquirer', now(), now()),
			($2, $3, 'acme', 'Acme Corp', 'https://schema.org/Organization', 'Target', now(), now())
	`, entityA, entityB, tenantID); err != nil {
		t.Fatalf("insert entities: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence, rank)
		VALUES ($1, $2, $3, $4, 'Acme Corp', 0.850, 'preferred')
	`, stmtID, tenantID, entityA, propID); err != nil {
		t.Fatalf("insert statement: %v", err)
	}

	publishedAt := time.Date(2025, 11, 14, 18, 30, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, content_hash, archive_url, published_at, status, raw_text)
		VALUES (
			$1, $2,
			'https://www.reuters.com/markets/deals/megacorp-acquires-acme-2025-11-14/',
			'hash-contract-test',
			'https://web.archive.org/web/20251114183000/https://www.reuters.com/...',
			$3,
			'verified',
			'full source body unused by bundle raw_text field'
		)
	`, sourceID, tenantID, publishedAt); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	excerpt := "MegaCorp Inc. announced Tuesday it would acquire Acme Corp for $4.2 billion."
	if _, err := tx.Exec(ctx, `
		INSERT INTO statement_sources (
			id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end,
			extraction_method, confidence, tenant_id
		) VALUES ($1, $2, $3, $4, 0, $5, 'test', 0.900, $6)
	`, ssID, stmtID, sourceID, excerpt, len(excerpt), tenantID); err != nil {
		t.Fatalf("insert statement_sources: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO qualifiers (id, statement_id, property_id, val_text)
		VALUES ($1, $2, $3, '2025-11-14')
	`, qualifierID, stmtID, qualPropID); err != nil {
		t.Fatalf("insert qualifier: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO relations (id, tenant_id, source_id, target_id, type, confidence)
		VALUES ($1, $2, $3, $4, 'acquired', 0.900)
	`, relationID, tenantID, entityA, entityB); err != nil {
		t.Fatalf("insert relation: %v", err)
	}

	// Include both entities so relations are in-scope at depth 0.
	bundle, err := Assemble(ctx, tx, []string{entityA, entityB}, 0, tenantID)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if bundle == nil {
		t.Fatal("Assemble returned nil bundle")
	}

	rawJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	var jsonRoot map[string]any
	if err := json.Unmarshal(rawJSON, &jsonRoot); err != nil {
		t.Fatalf("unmarshal marshaled bundle: %v", err)
	}

	schemas := loadOpenAPIBundleSchemas(t)
	assertKeysMatchSchema(t, "Bundle", jsonRoot, schemas["Bundle"])

	// With a fully-populated fixture, omitempty fields must be present so the
	// JSON key set equals the schema property set (anti-drift both directions).
	bundleProps := schemaPropertyNames(schemas["Bundle"])
	jsonKeys := objectKeys(jsonRoot)
	if len(jsonKeys) != len(bundleProps) {
		t.Errorf("Bundle: expected exact key-set match with OpenAPI properties; json=%v schema=%v",
			jsonKeys, bundleProps)
	}
	for k := range bundleProps {
		if _, ok := jsonKeys[k]; !ok {
			t.Errorf("Bundle: OpenAPI property %q missing from fully-populated marshaled bundle", k)
		}
	}

	entityObj := firstObject(t, "entities", jsonRoot["entities"])
	assertKeysMatchSchema(t, "BundleEntity", entityObj, schemas["BundleEntity"])
	for k := range schemaPropertyNames(schemas["BundleEntity"]) {
		if _, ok := entityObj[k]; !ok {
			t.Errorf("BundleEntity: OpenAPI property %q missing from marshaled entity", k)
		}
	}

	stmtObj := firstObject(t, "statements", jsonRoot["statements"])
	assertKeysMatchSchema(t, "BundleStatement", stmtObj, schemas["BundleStatement"])
	for k := range schemaPropertyNames(schemas["BundleStatement"]) {
		if _, ok := stmtObj[k]; !ok {
			t.Errorf("BundleStatement: OpenAPI property %q missing from marshaled statement", k)
		}
	}

	sourceObj := firstObject(t, "sources", jsonRoot["sources"])
	assertKeysMatchSchema(t, "BundleSource", sourceObj, schemas["BundleSource"])
	for k := range schemaPropertyNames(schemas["BundleSource"]) {
		if _, ok := sourceObj[k]; !ok {
			t.Errorf("BundleSource: OpenAPI property %q missing from marshaled source", k)
		}
	}

	qualObj := firstObject(t, "qualifiers", jsonRoot["qualifiers"])
	assertKeysMatchSchema(t, "BundleQualifier", qualObj, schemas["BundleQualifier"])

	relObj := firstObject(t, "relations", jsonRoot["relations"])
	assertKeysMatchSchema(t, "BundleRelation", relObj, schemas["BundleRelation"])

	// Sanity: flat shape, not the obsolete nested README shape.
	if _, ok := stmtObj["property"]; ok {
		t.Error("statement must not nest property as an object; expected flat property_slug")
	}
	if _, ok := sourceObj["publisher"]; ok {
		t.Error("source must not include publisher (not in BundleSource json tags)")
	}
	if _, ok := sourceObj["content_hash"]; ok {
		t.Error("source must not include content_hash (not in BundleSource json tags)")
	}
	if _, ok := sourceObj["excerpt"]; ok {
		t.Error("source must not include excerpt; field is raw_text")
	}
	if _, ok := stmtObj["sources"]; ok {
		t.Error("statement must not nest sources[]; use top-level sources + source_ids")
	}
}

// TestBundleOpenAPIContractRejectsSpecDriftWithoutCodeChange is adversarial:
// if the OpenAPI Bundle schema gains a property the assembler does not emit,
// or drops a required property the assembler always emits, the comparator must fail.
func TestBundleOpenAPIContractRejectsSpecDriftWithoutCodeChange(t *testing.T) {
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
			t.Logf("rollback: %v", err)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label, type_uri, created_at, updated_at)
		VALUES ($1, $2, 'drift', 'Drift Entity', 'https://schema.org/Thing', now(), now())
	`, entityID, tenantID); err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	bundle, err := Assemble(ctx, tx, []string{entityID}, 0, tenantID)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	rawJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var jsonRoot map[string]any
	if err := json.Unmarshal(rawJSON, &jsonRoot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	schemas := loadOpenAPIBundleSchemas(t)
	realSchema := schemas["Bundle"]

	// Drift A: invent a required OpenAPI field the JSON will never have.
	driftExtra := cloneSchema(realSchema)
	props, ok := driftExtra["properties"].(map[string]any)
	if !ok {
		t.Fatal("cloneSchema: driftExtra properties is not a map")
	}
	props["publisher"] = map[string]any{"type": "string"}
	driftExtra["required"] = append(append([]any{}, schemaRequiredAsAny(realSchema)...), "publisher")
	if !contractWouldFail(jsonRoot, driftExtra) {
		t.Fatal("expected comparator to fail when OpenAPI requires publisher absent from JSON")
	}

	// Drift B: drop a required OpenAPI property that JSON still emits, and leave
	// it out of properties entirely — JSON key without schema property must fail.
	driftMissingProp := cloneSchema(realSchema)
	propsB, ok := driftMissingProp["properties"].(map[string]any)
	if !ok {
		t.Fatal("cloneSchema: driftMissingProp properties is not a map")
	}
	delete(propsB, "tenant_id")
	if !contractWouldFail(jsonRoot, driftMissingProp) {
		t.Fatal("expected comparator to fail when JSON has tenant_id but schema properties omit it")
	}

	// Drift C: obsolete nested README keys must not be accepted as BundleSource properties.
	sourceSchema := schemas["BundleSource"]
	for _, forbidden := range []string{"publisher", "content_hash", "excerpt"} {
		if _, ok := schemaPropertyNames(sourceSchema)[forbidden]; ok {
			t.Errorf("BundleSource OpenAPI must not document obsolete field %q", forbidden)
		}
	}
}

func schemaRequiredAsAny(schema map[string]any) []any {
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	out := make([]any, len(raw))
	copy(out, raw)
	return out
}

func cloneSchema(schema map[string]any) map[string]any {
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		if k == "properties" {
			src, ok := v.(map[string]any)
			if !ok {
				src = map[string]any{}
			}
			cp := make(map[string]any, len(src))
			maps.Copy(cp, src)
			out[k] = cp
			continue
		}
		if k == "required" {
			out[k] = schemaRequiredAsAny(schema)
			continue
		}
		out[k] = v
	}
	return out
}

// contractWouldFail mirrors assertKeysMatchSchema failure conditions without t.Error.
func contractWouldFail(jsonObj map[string]any, schema map[string]any) bool {
	schemaProps := schemaPropertyNames(schema)
	jsonKeys := objectKeys(jsonObj)
	for key := range jsonKeys {
		if _, ok := schemaProps[key]; !ok {
			return true
		}
	}
	for _, req := range schemaRequiredNames(schema) {
		if _, ok := jsonKeys[req]; !ok {
			return true
		}
		if _, ok := schemaProps[req]; !ok {
			return true
		}
	}
	return false
}
