package workers

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/db"
)

type Corroborator struct {
	DB     *pgxpool.Pool
	Logger *slog.Logger
}

type sourceEvidence struct {
	StatementID string
	SourceID    string
	URL         string
	Publisher   string
	RawText     string
	Confidence  float64
}

func (c *Corroborator) CorroborateOnce(ctx context.Context, tenantID string) error {
	if c == nil || c.DB == nil {
		return fmt.Errorf("corroborate worker: nil db pool")
	}
	if tenantID == "" {
		return fmt.Errorf("tenant id required")
	}
	var tenant pgtype.UUID
	if err := tenant.Scan(tenantID); err != nil {
		return fmt.Errorf("invalid tenant id: %w", err)
	}
	txCtx := db.WithPool(ctx, c.DB)
	txCtx, tx, err := db.TenantContext(txCtx, tenant)
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil {
			// Expected after commit; ignore.
		}
	}()

	rows, err := tx.Query(txCtx, `
		SELECT ss.statement_id::text, ss.source_id::text, s.url, COALESCE(s.publisher, ''), COALESCE(s.raw_text, ''), COALESCE(ss.confidence::float8, 0.5)
		FROM statement_sources ss
		JOIN sources s ON s.id = ss.source_id
		WHERE ss.tenant_id = $1::uuid
		ORDER BY ss.statement_id, ss.source_id
	`, tenantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byStatement := map[string][]sourceEvidence{}
	for rows.Next() {
		var ev sourceEvidence
		if err := rows.Scan(&ev.StatementID, &ev.SourceID, &ev.URL, &ev.Publisher, &ev.RawText, &ev.Confidence); err != nil {
			return err
		}
		byStatement[ev.StatementID] = append(byStatement[ev.StatementID], ev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for statementID, evidence := range byStatement {
		independent := CountIndependentSources(evidence)
		confidences := make([]float64, 0, len(evidence))
		for _, ev := range evidence {
			confidences = append(confidences, ev.Confidence)
		}
		confidence := assembler.ComputeConfidence(independent, confidences)
		if _, err := tx.Exec(txCtx, "UPDATE statements SET confidence = $1 WHERE id = $2 AND tenant_id = $3::uuid", confidence, statementID, tenantID); err != nil {
			return err
		}
	}
	c.logger().InfoContext(txCtx, "corroborate worker completed", "statements", len(byStatement))
	if err := tx.Commit(txCtx); err != nil {
		return err
	}
	return nil
}

type sourceCluster struct {
	domain string
	texts  []string
}

func CountIndependentSources(evidence []sourceEvidence) int {
	var clusters []sourceCluster
	for _, ev := range evidence {
		domain := evidenceDomain(ev)
		if domain == "" {
			domain = ev.SourceID
		}
		textKey := canonicalText(ev.RawText)
		matched := false
		for i := range clusters {
			if clusters[i].domain != domain {
				continue
			}
			for _, existing := range clusters[i].texts {
				if trigramSimilarity(existing, textKey) >= 0.85 {
					clusters[i].texts = append(clusters[i].texts, textKey)
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			clusters = append(clusters, sourceCluster{domain: domain, texts: []string{textKey}})
		}
	}
	return len(clusters)
}

func evidenceDomain(ev sourceEvidence) string {
	if ev.Publisher != "" {
		return strings.ToLower(strings.TrimSpace(ev.Publisher))
	}
	parsed, err := url.Parse(ev.URL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
}

func canonicalText(text string) string {
	fields := strings.Fields(strings.ToLower(text))
	if len(fields) > 200 {
		fields = fields[:200]
	}
	return strings.Join(fields, " ")
}

func trigramSimilarity(a, b string) float64 {
	aSet := trigrams(a)
	bSet := trigrams(b)
	if len(aSet) == 0 && len(bSet) == 0 {
		return 1
	}
	var intersection int
	for tri := range aSet {
		if bSet[tri] {
			intersection++
		}
	}
	return float64(2*intersection) / float64(len(aSet)+len(bSet))
}

func trigrams(s string) map[string]bool {
	runes := []rune("  " + s + "  ")
	out := make(map[string]bool)
	if len(runes) < 3 {
		return out
	}
	for i := 0; i <= len(runes)-3; i++ {
		out[string(runes[i:i+3])] = true
	}
	return out
}

func (c *Corroborator) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

// CorroborateOnce preserves the original package-level smoke-test entry point.
func CorroborateOnce(ctx context.Context, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	logger.InfoContext(ctx, "corroborate worker requires Corroborator for database-backed execution")
	return nil
}
