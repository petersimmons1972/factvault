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

type Example struct {
	Name       string
	Path       string
	Properties []Property `json:"properties"`
	Seeds      []Seed     `json:"seeds"`
	Fixtures   []string   `json:"fixtures"`
}

type Property struct {
	Slug        string `yaml:"slug" json:"slug"`
	Label       string `yaml:"label" json:"label"`
	ValueType   string `yaml:"value_type" json:"value_type"`
	Description string `yaml:"description" json:"description,omitempty"`
}

type Seed struct {
	ExtID       string `yaml:"ext_id" json:"ext_id"`
	Label       string `yaml:"label" json:"label"`
	TypeURI     string `yaml:"type_uri" json:"type_uri"`
	Description string `yaml:"description" json:"description,omitempty"`
}

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

func (e *Example) Insert(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	if pool == nil {
		return fmt.Errorf("nil database pool")
	}
	for _, property := range e.Properties {
		if _, err := pool.Exec(ctx, `
			INSERT INTO properties (id, tenant_id, slug, label, value_type, description)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))
			ON CONFLICT (tenant_id, slug) DO UPDATE SET label = EXCLUDED.label, value_type = EXCLUDED.value_type, description = EXCLUDED.description
		`, uuid.NewString(), tenantID, property.Slug, property.Label, property.ValueType, property.Description); err != nil {
			return fmt.Errorf("insert property %s: %w", property.Slug, err)
		}
	}
	for _, seed := range e.Seeds {
		if _, err := pool.Exec(ctx, `
			INSERT INTO entities (id, tenant_id, ext_id, label, type_uri, description)
			VALUES ($1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), NULLIF($6, ''))
			ON CONFLICT (tenant_id, ext_id) DO UPDATE SET label = EXCLUDED.label, type_uri = EXCLUDED.type_uri, description = EXCLUDED.description
		`, uuid.NewString(), tenantID, seed.ExtID, seed.Label, seed.TypeURI, seed.Description); err != nil {
			return fmt.Errorf("insert seed %s: %w", seed.Label, err)
		}
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
