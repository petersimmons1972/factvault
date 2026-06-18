// Package embed provides a client for factvault's embedder service.
// It calls POST /embed and returns 1024-dim float32 vectors.
// The request/response shape mirrors the one used by internal/doctor/checks.go
// (CheckEmbedder), which is the proven working contract for this service.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Client calls the factvault embedder service at EmbedderURL.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient returns a Client pointed at the given base URL.
// If httpClient is nil the default http.Client is used.
func NewClient(embedderURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(embedderURL, "/"),
		httpClient: httpClient,
	}
}

// embedRequest is the JSON body for POST /embed.
type embedRequest struct {
	Texts []string `json:"texts"`
}

// embedResponse is the JSON body returned by POST /embed.
type embedResponse struct {
	Vectors [][]float64 `json:"vectors"`
}

// Embed sends texts to POST /embed and returns one []float32 vector per input text.
// Returns an empty slice (not nil) for empty input without calling the service.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	body, err := json.Marshal(embedRequest{Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: POST /embed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// best-effort close; response already consumed
			_ = err
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: POST /embed status %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}

	// Convert [][]float64 → [][]float32 (pgvector uses float32)
	out := make([][]float32, len(result.Vectors))
	for i, v64 := range result.Vectors {
		v32 := make([]float32, len(v64))
		for j, f := range v64 {
			v32[j] = float32(f)
		}
		out[i] = v32
	}
	return out, nil
}
