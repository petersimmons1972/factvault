package api

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/briefs"
	"github.com/petersimmons1972/factvault/internal/retrieval"
)

type Server struct {
	Service  retrieval.Service
	Verifier auth.Verifier
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ReadyResponse struct {
	Ready  bool     `json:"ready"`
	Errors []string `json:"errors,omitempty"`
}

type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func New(pool *pgxpool.Pool, publicKey *rsa.PublicKey) *Server {
	return &Server{
		Service:  retrieval.Service{Pool: pool},
		Verifier: auth.Verifier{PublicKey: publicKey},
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ready := s.Service.Pool != nil
		resp := ReadyResponse{Ready: ready}
		if !ready {
			resp.Errors = []string{"database pool is not configured"}
		}
		writeJSON(w, http.StatusOK, resp)
	})
	r.Group(func(r chi.Router) {
		r.Use(s.jwtMiddleware)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	claims := ClaimsFromContext(r.Context())
	rec, err := (briefs.Service{Pool: s.Service.Pool}).GenerateAndStore(r.Context(), claims.TenantID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) getBriefs(w http.ResponseWriter, r *http.Request) {
	opts := briefs.ListOptions{Limit: 100}
	if q := r.URL.Query().Get("limit"); q != "" {
		if parsed, err := strconv.Atoi(q); err == nil {
			opts.Limit = parsed
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

func (s *Server) getBrief(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	rec, err := (briefs.Service{Pool: s.Service.Pool}).Get(r.Context(), claims.TenantID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, assembler.ErrEntityNotFound):
		writeProblem(w, http.StatusNotFound, "not found", err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		writeProblem(w, http.StatusNotFound, "not found", err.Error())
	case errors.Is(err, assembler.ErrInvalidDepth), errors.Is(err, assembler.ErrInvalidEntityCount):
		writeProblem(w, http.StatusBadRequest, "bad request", err.Error())
	default:
		writeProblem(w, http.StatusInternalServerError, "internal server error", err.Error())
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
	_ = json.NewEncoder(w).Encode(value)
}
