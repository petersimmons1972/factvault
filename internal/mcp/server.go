// Package mcpserver implements the Model Context Protocol tooling surface for factvault.
package mcpserver

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petersimmons1972/factvault/internal/auth"
	"github.com/petersimmons1972/factvault/internal/embed"
	"github.com/petersimmons1972/factvault/internal/retrieval"
	"github.com/petersimmons1972/factvault/internal/store"
	pgstore "github.com/petersimmons1972/factvault/internal/store/postgres"
	"github.com/petersimmons1972/factvault/internal/version"
)

// Server owns MCP handlers and verification dependencies.
type Server struct {
	Service      retrieval.Service
	Verifier     auth.Verifier
	DefaultToken string
}

// EntityLookupArgs contains authorization and target entity for dossier lookup.
type EntityLookupArgs struct {
	Authorization string `json:"authorization" jsonschema:"Bearer token"`
	EntityID      string `json:"entity_id" jsonschema:"entity UUID"`
	Depth         int    `json:"depth,omitempty" jsonschema:"graph depth, normally 0 for dossier"`
}

// StoryQueryArgs contains authorization and query constraints for story search.
type StoryQueryArgs struct {
	Authorization string `json:"authorization" jsonschema:"Bearer token"`
	Query         string `json:"query" jsonschema:"story query text"`
	Depth         int    `json:"depth,omitempty" jsonschema:"graph depth from 1 to 3"`
}

// FactQueryArgs contains authorization and query text for fact search.
type FactQueryArgs struct {
	Authorization string `json:"authorization" jsonschema:"Bearer token"`
	Query         string `json:"query" jsonschema:"fact query text"`
}

// New builds a new MCP server wrapper around retrieval and auth dependencies.
// embedderURL is the base URL for the embedder service (e.g. FACTVAULT_EMBEDDER_URL).
// If empty, the cosine seed-search path is disabled and all queries fall back to ILIKE.
func New(pool *pgxpool.Pool, publicKey *rsa.PublicKey, defaultToken string, embedderURL string) *Server {
	var embedder retrieval.Embedder
	if embedderURL != "" {
		embedder = embed.NewClient(embedderURL, nil)
	}
	var vs store.VectorStore
	if pool != nil {
		vs = pgstore.New(pool)
	}
	return &Server{
		Service:      retrieval.NewService(pool, embedder, vs),
		Verifier:     auth.Verifier{PublicKey: publicKey},
		DefaultToken: defaultToken,
	}
}

// MCPServer builds the configured MCP tool surface.
func (s *Server) MCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "factvault", Version: version.Version}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "factvault__entity_lookup", Description: "Return an entity dossier bundle."}, s.entityLookup)
	mcp.AddTool(server, &mcp.Tool{Name: "factvault__story_query", Description: "Return a graph-expanded story bundle."}, s.storyQuery)
	mcp.AddTool(server, &mcp.Tool{Name: "factvault__fact_query", Description: "Return a fact query bundle."}, s.factQuery)
	return server
}

// RunStdio runs the MCP server using stdio transport.
func (s *Server) RunStdio(ctx context.Context) error {
	return s.MCPServer().Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) tenantFromAuthorization(authorization string) (string, error) {
	token := strings.TrimSpace(authorization)
	if token == "" {
		return "", fmt.Errorf("missing authorization token")
	}
	if bearer, ok := auth.BearerToken(token); ok {
		token = bearer
	}
	claims, err := s.Verifier.Verify(token)
	if err != nil {
		return "", fmt.Errorf("invalid authorization token: %w", err)
	}
	if claims.TenantID == "" {
		return "", fmt.Errorf("invalid authorization token: missing tenant_id")
	}
	return claims.TenantID, nil
}

func (s *Server) entityLookup(ctx context.Context, _ *mcp.CallToolRequest, args EntityLookupArgs) (*mcp.CallToolResult, any, error) {
	tenantID, err := s.tenantFromAuthorization(args.Authorization)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.Service.Dossier(ctx, tenantID, args.EntityID)
	if err != nil {
		return nil, nil, err
	}
	return nil, resp, nil
}

func (s *Server) storyQuery(ctx context.Context, _ *mcp.CallToolRequest, args StoryQueryArgs) (*mcp.CallToolResult, any, error) {
	tenantID, err := s.tenantFromAuthorization(args.Authorization)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.Service.Story(ctx, tenantID, retrieval.StoryRequest{Query: args.Query, Depth: args.Depth})
	if err != nil {
		return nil, nil, err
	}
	return nil, resp, nil
}

func (s *Server) factQuery(ctx context.Context, _ *mcp.CallToolRequest, args FactQueryArgs) (*mcp.CallToolResult, any, error) {
	tenantID, err := s.tenantFromAuthorization(args.Authorization)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.Service.FactsQuery(ctx, tenantID, retrieval.FactsQueryRequest{Query: args.Query})
	if err != nil {
		return nil, nil, err
	}
	return nil, resp, nil
}
