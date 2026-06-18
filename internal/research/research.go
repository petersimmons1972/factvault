// Package research provides the active acquisition loop for factvault.
//
// It generates perspective-angled web search queries for a given seed entity
// via exactly 2 LLM calls, then returns the deduplicated queries as plain
// Query strings. It emits ONLY query strings — it has NO DB write path and
// MUST NOT import internal/workers.
package research

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/petersimmons1972/factvault/internal/collectors"
)

// Query is a plain search query string produced by the research package.
// It is the ONLY output type — no DB records, no fact writes.
type Query string

// Entity identifies the seed entity for the research run.
type Entity struct {
	Label string // human-readable name, e.g. "Rome"
	Type  string // entity type, e.g. "City", "Person"
}

// Config controls the bounds on generation and fetching.
type Config struct {
	Perspectives            int // max number of perspectives (default 5)
	QuestionsPerPerspective int // max queries per perspective (default 4)
	ResultsPerQuery         int // max search results per query (default 5)
	MaxTotalFetches         int // hard ceiling on page fetches per run (default 40)
}

// LLMConfig holds connection settings for the OpenAI-compatible LLM endpoint.
type LLMConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// GenerateQueries runs 2 staged LLM calls to produce perspective-angled search queries.
//
//   - Call 1: entity label/type (+ optional grounding hints) → N perspectives
//   - Call 2: all perspectives → batched questions+queries
//
// Caps are deterministically enforced: LLM over-production is silently truncated.
// Queries are normalised (lowercase, collapse whitespace, strip stopwords) and
// exact-deduped before return.
//
// hints are property slugs from already-known facets of the entity; the LLM is
// prompted to steer AWAY from those facets (warm-entity gap-steering). Pass nil
// for cold-start entities.
func GenerateQueries(ctx context.Context, entity Entity, cfg Config, hints []string, llm LLMConfig) ([]Query, error) {
	client := newLLMHTTPClient(llm)

	// --- Call 1: entity → perspectives ---
	perspectives, err := generatePerspectives(ctx, client, llm, entity, cfg.Perspectives, hints)
	if err != nil {
		return nil, fmt.Errorf("generate perspectives: %w", err)
	}

	// --- Call 2: all perspectives → batched queries ---
	raw, err := generateQueriesForPerspectives(ctx, client, llm, entity, perspectives, cfg.QuestionsPerPerspective)
	if err != nil {
		return nil, fmt.Errorf("generate queries: %w", err)
	}

	// Dedup and return.
	return dedup(raw), nil
}

// --- LLM call helpers ---

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model          string            `json:"model"`
	Messages       []chatMsg         `json:"messages"`
	ResponseFormat map[string]string `json:"response_format"`
	Temperature    float64           `json:"temperature,omitempty"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func newLLMHTTPClient(llm LLMConfig) *http.Client {
	if llm.HTTPClient != nil {
		return llm.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func chatComplete(ctx context.Context, client *http.Client, llm LLMConfig, systemPrompt, userPrompt string) (string, error) {
	req := chatReq{
		Model: llm.Model,
		Messages: []chatMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
		Temperature:    0,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(llm.BaseURL, "/")+"/chat/completions",
		bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if llm.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+llm.APIKey)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("llm returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var cr chatResp
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", fmt.Errorf("parse llm response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm returned empty choices")
	}
	return cr.Choices[0].Message.Content, nil
}

// generatePerspectives issues Call 1: entity → perspectives.
func generatePerspectives(ctx context.Context, client *http.Client, llm LLMConfig, entity Entity, maxCount int, hints []string) ([]string, error) {
	systemPrompt := `You are a research strategist. Given a seed entity, return a JSON object with a "perspectives" array of distinct research angles. Each perspective is a short phrase (5-10 words). Return ONLY valid JSON.`

	var sb strings.Builder
	fmt.Fprintf(&sb, "Entity: %q (type: %s)\n", entity.Label, entity.Type)            //nolint:errcheck
	fmt.Fprintf(&sb, "Generate up to %d distinct research perspectives.\n", maxCount) //nolint:errcheck
	if len(hints) > 0 {
		fmt.Fprintf(&sb, "Already-covered facets (steer AWAY from these): %s\n", strings.Join(hints, ", ")) //nolint:errcheck
	}

	content, err := chatComplete(ctx, client, llm, systemPrompt, sb.String())
	if err != nil {
		return nil, err
	}

	var result struct {
		Perspectives []string `json:"perspectives"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parse perspectives json: %w", err)
	}
	// Deterministically enforce cap.
	if len(result.Perspectives) > maxCount {
		result.Perspectives = result.Perspectives[:maxCount]
	}
	return result.Perspectives, nil
}

// perspectiveItem holds queries for one perspective.
type perspectiveItem struct {
	Perspective string   `json:"perspective"`
	Queries     []string `json:"queries"`
}

// generateQueriesForPerspectives issues Call 2: all perspectives → batched queries.
func generateQueriesForPerspectives(ctx context.Context, client *http.Client, llm LLMConfig, entity Entity, perspectives []string, maxPerPerspective int) ([]string, error) {
	if len(perspectives) == 0 {
		return nil, nil
	}
	systemPrompt := `You are a research strategist. Given an entity and research perspectives, return a JSON object with an "items" array. Each element has "perspective" (string) and "queries" (array of web search query strings). Return ONLY valid JSON.`

	var sb strings.Builder
	fmt.Fprintf(&sb, "Entity: %q\n", entity.Label)                                                             //nolint:errcheck
	fmt.Fprintf(&sb, "For each perspective below, generate up to %d web search queries.\n", maxPerPerspective) //nolint:errcheck
	sb.WriteString("Perspectives:\n")
	for i, p := range perspectives {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, p) //nolint:errcheck
	}

	content, err := chatComplete(ctx, client, llm, systemPrompt, sb.String())
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []perspectiveItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parse queries json: %w", err)
	}

	var out []string
	for _, item := range result.Items {
		queries := item.Queries
		// Deterministically enforce per-perspective cap.
		if len(queries) > maxPerPerspective {
			queries = queries[:maxPerPerspective]
		}
		out = append(out, queries...)
	}
	return out, nil
}

// --- Normalisation & dedup ---

// English stopwords for query normalization.
var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "of": {}, "in": {}, "on": {}, "at": {},
	"to": {}, "for": {}, "and": {}, "or": {}, "but": {}, "is": {}, "are": {},
	"was": {}, "were": {}, "be": {}, "been": {}, "by": {}, "with": {},
	"from": {}, "as": {}, "into": {}, "that": {}, "this": {}, "it": {},
	"its": {}, "not": {}, "do": {}, "does": {}, "did": {}, "has": {},
	"have": {}, "had": {}, "will": {}, "would": {}, "can": {}, "could": {},
}

// normalizeQuery lowercases, collapses whitespace, and strips stopwords.
func normalizeQuery(q string) string {
	lower := strings.ToLower(q)
	// Collapse all whitespace (including tabs, multiple spaces) to single space.
	parts := strings.FieldsFunc(lower, unicode.IsSpace)
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if _, isStop := stopwords[p]; !isStop {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, " ")
}

// dedup returns queries with normalized duplicates removed.
// The original (un-normalised) query string is preserved.
func dedup(raw []string) []Query {
	seen := make(map[string]struct{}, len(raw))
	out := make([]Query, 0, len(raw))
	for _, q := range raw {
		key := normalizeQuery(q)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Query(q))
	}
	return out
}

// --- SearchCollector ---

// SearchCollector implements collectors.Collector. It takes a slice of Query
// strings, performs a web search for each via SearXNG, fetches the top-N URLs,
// and returns []collectors.Item.
//
// The load-bearing guarantee: a LIVE fetch counter inside Collect hard-stops
// fetching at MaxTotalFetches, regardless of how many queries/results upstream
// produced.
type SearchCollector struct {
	Queries         []Query
	ResultsPerQuery int
	MaxTotalFetches int
	// SearchURL is the SearXNG base URL (e.g. "https://searxng.petersimmons.com").
	// Defaults to https://searxng.petersimmons.com.
	SearchURL  string
	HTTPClient *http.Client
}

// Name implements collectors.Collector.
func (s *SearchCollector) Name() string { return "search" }

// Collect implements collectors.Collector.
// It hard-stops fetching at MaxTotalFetches regardless of upstream supply.
func (s *SearchCollector) Collect(ctx context.Context) ([]collectors.Item, error) {
	client := s.httpClient()
	searchBase := s.searchBase()
	fetched := 0

	var items []collectors.Item
	for _, q := range s.Queries {
		if fetched >= s.MaxTotalFetches {
			break
		}
		urls, err := s.search(ctx, client, searchBase, string(q))
		if err != nil {
			// Non-fatal: log and continue to next query.
			continue
		}
		for _, u := range urls {
			if fetched >= s.MaxTotalFetches {
				break
			}
			html, title, err := fetchPage(ctx, client, u)
			if err != nil {
				continue
			}
			fetched++
			items = append(items, collectors.Item{
				URL:   u,
				HTML:  html,
				Title: title,
				Meta:  map[string]any{"trust_tier": "web"},
			})
		}
	}
	return items, nil
}

// search calls SearXNG and returns up to ResultsPerQuery URLs.
type searxResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type searxResponse struct {
	Results []searxResult `json:"results"`
}

func (s *SearchCollector) search(ctx context.Context, client *http.Client, searchBase, query string) ([]string, error) {
	limit := s.ResultsPerQuery
	if limit <= 0 {
		limit = 5
	}
	u := searchBase + "/search?q=" + url.QueryEscape(query) + "&format=json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var sr searxResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}
	var urls []string
	for i, r := range sr.Results {
		if i >= limit {
			break
		}
		if r.URL != "" {
			urls = append(urls, r.URL)
		}
	}
	return urls, nil
}

// fetchPage GETs a URL and returns the raw HTML body + title (from <title> tag if present).
const maxFetchBodyBytes = 5 * 1024 * 1024

func fetchPage(ctx context.Context, client *http.Client, rawURL string) (html []byte, title string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "factvault-research/1 (+https://github.com/petersimmons1972/factvault)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("fetch %s: status %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBodyBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxFetchBodyBytes {
		return nil, "", fmt.Errorf("fetch %s: response too large", rawURL)
	}
	title = extractTitle(string(body))
	return body, title, nil
}

// extractTitle extracts the text content of the first <title> tag.
func extractTitle(html string) string {
	lower := strings.ToLower(html)
	start := strings.Index(lower, "<title>")
	if start < 0 {
		return ""
	}
	start += len("<title>")
	end := strings.Index(lower[start:], "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(html[start : start+end])
}

func (s *SearchCollector) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (s *SearchCollector) searchBase() string {
	if s.SearchURL != "" {
		return strings.TrimRight(s.SearchURL, "/")
	}
	return "https://searxng.petersimmons.com"
}

// Ensure SearchCollector implements collectors.Collector at compile time.
var _ collectors.Collector = (*SearchCollector)(nil)
