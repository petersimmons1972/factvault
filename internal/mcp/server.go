package mcpserver

import (
	"context"
	"crypto/rsa"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petersimmons1972/factvault/internal/retrieval"
	"github.com/petersimmons1972/factvault/internal/version"
)

type Server struct {
	Service retrieval.Service
}

type EntityLookupArgs struct {
	TenantID string `json:"tenant_id" jsonschema:"tenant UUID"`
	EntityID string `json:"entity_id" jsonschema:"entity UUID"`
	Depth    int    `json:"depth,omitempty" jsonschema:"graph depth, normally 0 for dossier"`
}

type StoryQueryArgs struct {
	TenantID string `json:"tenant_id" jsonschema:"tenant UUID"`
	Query    string `json:"query" jsonschema:"story query text"`
	Depth    int    `json:"depth,omitempty" jsonschema:"graph depth from 1 to 3"`
}

type FactQueryArgs struct {
	TenantID string `json:"tenant_id" jsonschema:"tenant UUID"`
	Query    string `json:"query" jsonschema:"fact query text"`
}

func New(pool *pgxpool.Pool, _ *rsa.PublicKey) *Server {
	return &Server{Service: retrieval.Service{Pool: pool}}
}

func (s *Server) MCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "factvault", Version: version.Version}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "factvault__entity_lookup", Description: "Return an entity dossier bundle."}, s.entityLookup)
	mcp.AddTool(server, &mcp.Tool{Name: "factvault__story_query", Description: "Return a graph-expanded story bundle."}, s.storyQuery)
	mcp.AddTool(server, &mcp.Tool{Name: "factvault__fact_query", Description: "Return a fact query bundle."}, s.factQuery)
	return server
}

func (s *Server) RunStdio(ctx context.Context) error {
	return s.MCPServer().Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) entityLookup(ctx context.Context, _ *mcp.CallToolRequest, args EntityLookupArgs) (*mcp.CallToolResult, any, error) {
	resp, err := s.Service.Dossier(ctx, args.TenantID, args.EntityID)
	if err != nil {
		return nil, nil, err
	}
	return nil, resp, nil
}

func (s *Server) storyQuery(ctx context.Context, _ *mcp.CallToolRequest, args StoryQueryArgs) (*mcp.CallToolResult, any, error) {
	resp, err := s.Service.Story(ctx, args.TenantID, retrieval.StoryRequest{Query: args.Query, Depth: args.Depth})
	if err != nil {
		return nil, nil, err
	}
	return nil, resp, nil
}

func (s *Server) factQuery(ctx context.Context, _ *mcp.CallToolRequest, args FactQueryArgs) (*mcp.CallToolResult, any, error) {
	resp, err := s.Service.FactsQuery(ctx, args.TenantID, retrieval.FactsQueryRequest{Query: args.Query})
	if err != nil {
		return nil, nil, err
	}
	return nil, resp, nil
}
