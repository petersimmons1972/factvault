package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/netx"
)

type CheckResult struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Required bool   `json:"required"`
	Detail   string `json:"detail,omitempty"`
	Remedy   string `json:"remedy,omitempty"`
	Elapsed  string `json:"elapsed"`
}

type Config struct {
	DatabaseURL string
	LLMURL      string
	EmbedderURL string
	WaybackURL  string
	HTTPClient  *http.Client
}

type checkFunc func(context.Context, Config) CheckResult

// requiredChecks are the checks that must pass for the system to function.
// Optional checks (LLM, embedder, Wayback) are run separately and do not
// block a healthy status when --required-only is used.
var requiredChecks = []checkFunc{CheckPostgres, CheckMigrations, CheckRLS, CheckCanary}
var optionalChecks = []checkFunc{CheckLLM, CheckEmbedder, CheckWayback}

func RunAll(ctx context.Context, cfg Config) []CheckResult {
	results := make([]CheckResult, 0, len(requiredChecks)+len(optionalChecks))
	for _, check := range requiredChecks {
		r := check(ctx, cfg)
		r.Required = true
		results = append(results, r)
	}
	for _, check := range optionalChecks {
		r := check(ctx, cfg)
		r.Required = false
		results = append(results, r)
	}
	return results
}

func AllOK(results []CheckResult) bool {
	for _, result := range results {
		if !result.OK {
			return false
		}
	}
	return true
}

// RequiredOK returns true if all required checks passed.
// Optional check failures are ignored.
func RequiredOK(results []CheckResult) bool {
	for _, result := range results {
		if result.Required && !result.OK {
			return false
		}
	}
	return true
}

func CheckPostgres(ctx context.Context, cfg Config) CheckResult {
	return timed("postgres", func() (string, string, error) {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return "", "set FACTVAULT_DATABASE_URL", err
		}
		defer pool.Close()
		var ext string
		if err := pool.QueryRow(ctx, "SELECT extname FROM pg_extension WHERE extname='vector'").Scan(&ext); err != nil {
			return "", "run factvault migrate", err
		}
		return "pgvector loaded", "", nil
	})
}

func CheckMigrations(ctx context.Context, cfg Config) CheckResult {
	return timed("migrations", func() (string, string, error) {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return "", "set FACTVAULT_DATABASE_URL", err
		}
		defer pool.Close()
		var version int64
		if err := pool.QueryRow(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
			return "", "run factvault migrate", err
		}
		if version < 2 {
			return "", "run factvault migrate", fmt.Errorf("schema version %d < 2", version)
		}
		return fmt.Sprintf("schema version %d", version), "", nil
	})
}

func CheckRLS(ctx context.Context, cfg Config) CheckResult {
	return timed("rls", func() (string, string, error) {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return "", "set FACTVAULT_DATABASE_URL", err
		}
		defer pool.Close()
		tenantA := uuid.NewString()
		tenantB := uuid.NewString()
		if _, err := pool.Exec(ctx, "INSERT INTO entities (tenant_id, label) VALUES ($1, 'A'), ($2, 'B')", tenantA, tenantB); err != nil {
			return "", "check schema permissions", err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return "", "check database", err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantA); err != nil {
			return "", "check app.tenant_id GUC", err
		}
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_user"); err != nil {
			return "", "ensure app_user role exists", err
		}
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM entities").Scan(&count); err != nil {
			return "", "check RLS policies", err
		}
		if count != 1 {
			return "", "check tenant_isolation policies", fmt.Errorf("visible rows=%d, want 1", count)
		}
		return "cross-tenant row hidden", "", nil
	})
}

func CheckLLM(ctx context.Context, cfg Config) CheckResult {
	url := strings.TrimRight(defaultString(cfg.LLMURL, "http://localhost:11434/v1"), "/") + "/models"
	return checkHTTP(ctx, cfg, "llm", url, http.StatusOK)
}

func CheckEmbedder(ctx context.Context, cfg Config) CheckResult {
	base := strings.TrimRight(defaultString(cfg.EmbedderURL, "http://localhost:8080"), "/")
	return checkHTTPAny(ctx, cfg, "embedder", []string{base + "/healthz", base + "/health"})
}

func CheckWayback(ctx context.Context, cfg Config) CheckResult {
	url := strings.TrimRight(defaultString(cfg.WaybackURL, "https://web.archive.org"), "/") + "/"
	return checkHTTP(ctx, cfg, "wayback", url, http.StatusOK)
}

func CheckCanary(ctx context.Context, cfg Config) CheckResult {
	return timed("canary", func() (string, string, error) {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return "", "set FACTVAULT_DATABASE_URL", err
		}
		defer pool.Close()
		tenantID := uuid.NewString()
		entityID := uuid.NewString()
		tx, err := pool.Begin(ctx)
		if err != nil {
			return "", "check database", err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
			return "", "check app.tenant_id GUC", err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO entities (id, tenant_id, label) VALUES ($1, $2, 'Canary Entity')", entityID, tenantID); err != nil {
			return "", "check entity insert", err
		}
		bundle, err := assembler.Assemble(ctx, tx, []string{entityID}, 0, tenantID)
		if err != nil {
			return "", "check assembler", err
		}
		data, _ := json.Marshal(bundle)
		return fmt.Sprintf("assembled %d bytes", len(data)), "", nil
	})
}

func checkHTTP(ctx context.Context, cfg Config, name, url string, want int) CheckResult {
	return timed(name, func() (string, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", "check URL", err
		}
		resp, err := httpClient(cfg).Do(req)
		if err != nil {
			return "", "start dependent service", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			return "", "check service health", fmt.Errorf("status=%d", resp.StatusCode)
		}
		return url, "", nil
	})
}

func checkHTTPAny(ctx context.Context, cfg Config, name string, urls []string) CheckResult {
	var last CheckResult
	for _, url := range urls {
		res := checkHTTP(ctx, cfg, name, url, http.StatusOK)
		if res.OK {
			return res
		}
		last = res
	}
	return last
}

func timed(name string, fn func() (detail, remedy string, err error)) CheckResult {
	start := time.Now()
	detail, remedy, err := fn()
	res := CheckResult{Name: name, OK: err == nil, Detail: detail, Remedy: remedy, Elapsed: time.Since(start).Round(time.Millisecond).String()}
	if err != nil {
		res.Detail = err.Error()
	}
	return res
}

func httpClient(cfg Config) *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return netx.NewHTTPClient(5*time.Second, netx.ClientPolicy{AllowPrivateHosts: true})
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
