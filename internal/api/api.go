// Package api exposes the HTTP endpoints for factvault service operations.
package api

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/briefs"
	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/embed"
	"github.com/petersimmons1972/factvault/internal/retrieval"
	"github.com/petersimmons1972/factvault/internal/store"
	pgstore "github.com/petersimmons1972/factvault/internal/store/postgres"
)

// Server hosts HTTP handlers for retrieval, briefs, and liveness endpoints.
type Server struct {
	Service  retrieval.Service
	Verifier auth.Verifier
}

// HealthResponse is the JSON payload for /healthz.
type HealthResponse struct {
	Status string `json:"status"`
}

// ReadyResponse is the JSON payload for /readyz.
type ReadyResponse struct {
	Ready  bool     `json:"ready"`
	Errors []string `json:"errors,omitempty"`
}

// Problem follows RFC 7807-compatible error response structure.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

const defaultMaxBriefLimit = 1000

// maxRequestBodyBytes caps request bodies at 1 MiB to prevent heap-exhaustion DoS.
const maxRequestBodyBytes = 1 << 20

// New constructs a Server with retrieval service and verifier dependencies.
// embedderURL is the base URL for the embedder service (e.g. FACTVAULT_EMBEDDER_URL).
// If empty, the cosine seed-search path is disabled and all queries fall back to ILIKE.
func New(pool *pgxpool.Pool, publicKey *rsa.PublicKey, embedderURL string) *Server {
	var embedder retrieval.Embedder
	if embedderURL != "" {
		embedder = embed.NewClient(embedderURL, nil)
	}
	var vs store.VectorStore
	if pool != nil {
		vs = pgstore.New(pool)
	}
	return &Server{
		Service:  retrieval.NewService(pool, embedder, vs),
		Verifier: auth.Verifier{PublicKey: publicKey},
	}
}

// Router wires API routes and middleware.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		ready := s.Service.Pool != nil
		resp := ReadyResponse{Ready: ready}
		status := http.StatusOK
		if !ready {
			resp.Errors = []string{"database pool is not configured"}
			status = http.StatusServiceUnavailable // X7: 503 when not ready
		}
		writeJSON(w, status, resp)
	})
	r.Group(func(r chi.Router) {
		r.Use(s.jwtMiddleware)
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				req.Body = http.MaxBytesReader(w, req.Body, maxRequestBodyBytes)
				next.ServeHTTP(w, req)
			})
		})
		r.Get("/entities/{id}/dossier", s.getDossier)
		r.Post("/briefs/generate", s.postBriefGenerate)
		r.Get("/briefs", s.getBriefs)
		r.Get("/briefs/{id}", s.getBrief)
		r.Post("/stories", s.postStory)
		r.Post("/facts/query", s.postFactsQuery)
	})
	return r
}

func (s *Server) getDossier(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	resp, err := s.Service.Dossier(r.Context(), claims.TenantID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) postStory(w http.ResponseWriter, r *http.Request) {
	var req retrieval.StoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	claims := ClaimsFromContext(r.Context())
	resp, err := s.Service.Story(r.Context(), claims.TenantID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) postFactsQuery(w http.ResponseWriter, r *http.Request) {
	var req retrieval.FactsQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	claims := ClaimsFromContext(r.Context())
	resp, err := s.Service.FactsQuery(r.Context(), claims.TenantID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) postBriefGenerate(w http.ResponseWriter, r *http.Request) {
	var req briefs.GenerateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if strings.Contains(err.Error(), `unknown field "bundle"`) {
			fmt.Fprintf(os.Stderr, "rejected caller-supplied brief bundle: %v\n", err)
		}
		writeProblem(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	claims := ClaimsFromContext(r.Context())
	// A-10: validate that entity_id, if provided, belongs to the calling tenant.
	if req.EntityID != nil {
		if _, err := uuid.Parse(*req.EntityID); err != nil {
			writeProblem(w, http.StatusBadRequest, "bad request", "entity_id must be a valid UUID")
			return
		}
		exists, err := s.entityBelongsToTenant(r.Context(), claims.TenantID, *req.EntityID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !exists {
			writeProblem(w, http.StatusForbidden, "entity_id does not belong to tenant", "")
			return
		}
	}
	rec, err := (briefs.Service{
		Pool: s.Service.Pool,
		BundleLoader: briefs.BundleLoaderFunc(func(ctx context.Context, tenantID string, req briefs.GenerateRequest) (*assembler.Bundle, error) {
			return s.assembleBriefBundle(ctx, tenantID, req)
		}),
	}).GenerateAndStore(r.Context(), claims.TenantID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) entityBelongsToTenant(ctx context.Context, tenantID, entityID string) (bool, error) {
	var tenant pgtype.UUID
	if err := tenant.Scan(tenantID); err != nil {
		return false, fmt.Errorf("briefs/generate entity ownership: invalid tenant id: %w", err)
	}
	txCtx := db.WithPool(ctx, s.Service.Pool)
	txCtx, tx, err := db.TenantContext(txCtx, tenant)
	if err != nil {
		return false, fmt.Errorf("briefs/generate entity ownership: %w", err)
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			fmt.Fprintf(os.Stderr, "briefs/generate entity ownership rollback: %v\n", err)
		}
	}()

	var exists bool
	if err := tx.QueryRow(txCtx, "SELECT EXISTS(SELECT 1 FROM entities WHERE id=$1::uuid)", entityID).Scan(&exists); err != nil {
		return false, fmt.Errorf("briefs/generate entity ownership query: %w", err)
	}
	if err := tx.Commit(txCtx); err != nil {
		return false, fmt.Errorf("briefs/generate entity ownership commit: %w", err)
	}
	return exists, nil
}

func (s *Server) assembleBriefBundle(ctx context.Context, tenantID string, req briefs.GenerateRequest) (*assembler.Bundle, error) {
	switch req.SourceKind {
	case briefs.SourceKindDossier:
		if req.EntityID == nil || strings.TrimSpace(*req.EntityID) == "" {
			return nil, fmt.Errorf("%w: dossier briefs require entity_id", briefs.ErrInvalidGenerateRequest)
		}
		resp, err := s.Service.Dossier(ctx, tenantID, *req.EntityID)
		if err != nil {
			return nil, err
		}
		return resp.Bundle, nil
	case briefs.SourceKindStory:
		if req.Query == nil || strings.TrimSpace(*req.Query) == "" {
			return nil, fmt.Errorf("%w: story briefs require query", briefs.ErrInvalidGenerateRequest)
		}
		resp, err := s.Service.Story(ctx, tenantID, retrieval.StoryRequest{Query: *req.Query})
		if err != nil {
			return nil, err
		}
		return resp.Bundle, nil
	default:
		return nil, fmt.Errorf("%w: unsupported source_kind %q", briefs.ErrInvalidGenerateRequest, req.SourceKind)
	}
}

func (s *Server) getBriefs(w http.ResponseWriter, r *http.Request) {
	opts := briefs.ListOptions{Limit: 100}
	if q := r.URL.Query().Get("limit"); q != "" {
		if parsed, err := strconv.Atoi(q); err == nil {
			opts.Limit = min(parsed, maxBriefLimit())
		}
	}
	if sourceKind := r.URL.Query().Get("source_kind"); sourceKind != "" {
		s := briefs.SourceKind(sourceKind)
		opts.SourceKind = &s
	}
	if entityID := r.URL.Query().Get("entity_id"); entityID != "" {
		opts.EntityID = &entityID
	}
	if createdAfter := r.URL.Query().Get("created_after"); createdAfter != "" {
		if parsed, err := time.Parse(time.RFC3339, createdAfter); err == nil {
			opts.CreatedAfter = &parsed
		}
	}
	if createdBefore := r.URL.Query().Get("created_before"); createdBefore != "" {
		if parsed, err := time.Parse(time.RFC3339, createdBefore); err == nil {
			opts.CreatedBefore = &parsed
		}
	}
	claims := ClaimsFromContext(r.Context())
	records, err := (briefs.Service{Pool: s.Service.Pool}).List(r.Context(), claims.TenantID, opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func maxBriefLimit() int {
	limit, err := config.ResolveInt(nil, "FACTVAULT_MAX_BRIEFS_LIMIT", defaultMaxBriefLimit, false)
	if err != nil || limit <= 0 {
		return defaultMaxBriefLimit
	}
	return limit
}

func (s *Server) getBrief(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	rec, err := (briefs.Service{Pool: s.Service.Pool}).Get(r.Context(), claims.TenantID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// writeError maps known errors to 4xx responses and unknown errors to a 500
// with a correlation ID. Internal error detail is never sent to the client (X8):
//   - 4xx: static detail string; no err.Error() in the response body
//   - 5xx: generate a correlation ref, log the full error to stderr, send only the ref
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, assembler.ErrEntityNotFound), errors.Is(err, pgx.ErrNoRows):
		writeProblem(w, http.StatusNotFound, "not found", "")
	case errors.Is(err, briefs.ErrInvalidGenerateRequest):
		writeProblem(w, http.StatusBadRequest, "bad request", err.Error())
	case errors.Is(err, assembler.ErrInvalidDepth), errors.Is(err, assembler.ErrInvalidEntityCount):
		writeProblem(w, http.StatusBadRequest, "bad request", err.Error())
	default:
		corrID := uuid.NewString()
		fmt.Fprintf(os.Stderr, "error [%s]: %v\n", corrID, err)
		writeProblem(w, http.StatusInternalServerError, "internal server error", "ref: "+corrID)
	}
}

func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	writeJSONStatus(w, status, Problem{Type: "about:blank", Title: title, Status: status, Detail: detail})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	writeJSONStatus(w, status, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// Client disconnected before we could finish writing; nothing to do.
		_ = err
	}
}
