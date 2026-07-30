// Package vocabulary resolves extracted labels against a controlled property catalog.
package vocabulary

import (
	"strings"
	"unicode"
)

// Mode controls how unknown properties are handled during normalization.
type Mode string

const (
	// ModeStrict returns only properties already present in the catalog.
	ModeStrict Mode = "strict"
	// ModePermissive returns a best-effort proposal when no catalog match exists.
	ModePermissive Mode = "permissive"
)

// Property defines a resolvable property label and type.
type Property struct {
	Slug      string
	Label     string
	ValueType string
}

// ProposedProperty captures a candidate property when strict matching fails.
type ProposedProperty struct {
	ProposedSlug      string
	ProposedValueType string
	ProposedBy        string
	ExampleExcerpt    string
}

// Result is the resolver outcome for one label lookup.
type Result struct {
	Property        Property
	Known           bool
	QueuedProposals []ProposedProperty
}

// Resolver resolves labels to property metadata for extractor normalization.
type Resolver struct {
	Mode    Mode
	Catalog map[string]Property
}

// NewResolver returns a resolver configured with the requested mode.
func NewResolver(mode Mode) Resolver {
	return Resolver{
		Mode:    mode,
		Catalog: defaultCatalog(),
	}
}

// Resolve maps a label into a property, including proposal metadata in strict mode.
func (r Resolver) Resolve(label, valueType, excerpt string) Result {
	catalog := r.Catalog
	if catalog == nil {
		catalog = defaultCatalog()
	}

	normLabel := normalizeLabel(label)
	if property, ok := catalog[normLabel]; ok {
		return Result{
			Property: property,
			Known:    true,
		}
	}

	slug := normalizeSlug(label)
	if slug == "" {
		slug = normLabel
	}

	if r.Mode == ModeStrict {
		return Result{
			Property: Property{
				Slug:      slug,
				Label:     label,
				ValueType: valueType,
			},
			Known: false,
			QueuedProposals: []ProposedProperty{
				{
					ProposedSlug:      slug,
					ProposedValueType: valueType,
					ProposedBy:        "resolver:strict",
					ExampleExcerpt:    excerpt,
				},
			},
		}
	}

	return Result{
		Property: Property{
			Slug:      slug,
			Label:     label,
			ValueType: valueType,
		},
		Known: true,
	}
}

func defaultCatalog() map[string]Property {
	properties := []Property{
		{Slug: "sec_cik", Label: "SEC CIK", ValueType: "string"},
		{Slug: "sec_cusip", Label: "SEC CUSIP", ValueType: "string"},
		{Slug: "sec_isin", Label: "SEC ISIN", ValueType: "string"},
		{Slug: "usd_amount", Label: "USD amount", ValueType: "number"},
		{Slug: "date", Label: "Date", ValueType: "date"},
		{Slug: "org_name", Label: "Organization name", ValueType: "string"},
		{Slug: "person_name", Label: "Person name", ValueType: "string"},
		{Slug: "doi", Label: "DOI", ValueType: "string"},
		{Slug: "clinicaltrials.gov_nct_id", Label: "ClinicalTrials.gov NCT ID", ValueType: "string"},
		{Slug: "isbn_13", Label: "ISBN-13", ValueType: "string"},
	}

	catalog := make(map[string]Property, len(properties))
	for _, property := range properties {
		catalog[normalizeLabel(property.Slug)] = property
		catalog[normalizeLabel(property.Label)] = property
	}
	return catalog
}

func normalizeLabel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}
