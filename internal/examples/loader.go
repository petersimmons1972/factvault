// Package examples provides loading utilities for example seed content and fixtures.
package examples

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

// Example is an in-memory representation of one example dataset.
type Example struct {
	Name       string
	Path       string
	Properties []Property `json:"properties"`
	Seeds      []Seed     `json:"seeds"`
	Fixtures   []string   `json:"fixtures"`
}

// Property describes one property definition in an example fixture.
type Property struct {
	Slug        string `yaml:"slug" json:"slug"`
	Label       string `yaml:"label" json:"label"`
	ValueType   string `yaml:"value_type" json:"value_type"`
	Description string `yaml:"description" json:"description,omitempty"`
}

// Seed describes one seed entity in an example fixture.
type Seed struct {
	ExtID       string `yaml:"ext_id" json:"ext_id"`
	Label       string `yaml:"label" json:"label"`
	TypeURI     string `yaml:"type_uri" json:"type_uri"`
	Description string `yaml:"description" json:"description,omitempty"`
}

// List returns immediate child directories under the example root as example names.
func List(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Load parses one example directory into an Example object.
func Load(root, name string) (*Example, error) {
	path := filepath.Join(root, name)
	ex := &Example{Name: name, Path: path}
	if err := readYAML(filepath.Join(path, "properties.yaml"), &ex.Properties); err != nil {
		return nil, err
	}
	if err := readYAML(filepath.Join(path, "seeds.yaml"), &ex.Seeds); err != nil {
		return nil, err
	}
	fixtures, err := filepath.Glob(filepath.Join(path, "fixtures", "*"))
	if err != nil {
		return nil, err
	}
	for _, fixture := range fixtures {
		info, err := os.Stat(fixture)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			ex.Fixtures = append(ex.Fixtures, fixture)
		}
	}
	sort.Strings(ex.Fixtures)
	return ex, nil
}

// Insert persists the example into the database for the provided tenant.
// All writes run inside one transaction with the app.tenant_id GUC set so the
// tenant_isolation RLS policies pass on app_user (runtime DSN) connections,
// not only on RLS-bypassing superuser connections.
func (e *Example) Insert(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	if pool == nil {
		return fmt.Errorf("nil database pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after successful commit

	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	for _, property := range e.Properties {
		if _, err := tx.Exec(ctx, `
			INSERT INTO properties (id, tenant_id, slug, label, value_type, description)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))
			ON CONFLICT (tenant_id, slug) DO UPDATE SET label = EXCLUDED.label, value_type = EXCLUDED.value_type, description = EXCLUDED.description
		`, uuid.NewString(), tenantID, property.Slug, property.Label, property.ValueType, property.Description); err != nil {
			return fmt.Errorf("insert property %s: %w", property.Slug, err)
		}
	}
	for _, seed := range e.Seeds {
		if _, err := tx.Exec(ctx, `
			INSERT INTO entities (id, tenant_id, ext_id, label, type_uri, description)
			VALUES ($1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), NULLIF($6, ''))
			ON CONFLICT (tenant_id, ext_id) DO UPDATE SET label = EXCLUDED.label, type_uri = EXCLUDED.type_uri, description = EXCLUDED.description
		`, uuid.NewString(), tenantID, seed.ExtID, seed.Label, seed.TypeURI, seed.Description); err != nil {
			return fmt.Errorf("insert seed %s: %w", seed.Label, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func readYAML(path string, target any) error {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
