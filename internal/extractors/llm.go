package extractors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/petersimmons1972/factvault/internal/config"
	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/netx"
)

// StatementProposal is a candidate factual statement extracted from raw source text.
type StatementProposal struct {
	SubjectText        string `json:"subject_text"`
	PropertySlug       string `json:"property_slug"`
	Value              string `json:"value"`
	ValueType          string `json:"value_type"`
	Excerpt            string `json:"excerpt"`
	ExcerptOffsetStart int    `json:"excerpt_offset_start"`
	ExcerptOffsetEnd   int    `json:"excerpt_offset_end"`
}

// LLMClient queries an LLM completion endpoint and parses statement proposals.
type LLMClient struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

type chatCompletionRequest struct {
	Model          string                  `json:"model"`
	Messages       []chatCompletionMessage `json:"messages"`
	ResponseFormat map[string]string       `json:"response_format"`
	Temperature    float64                 `json:"temperature,omitempty"`
}

type chatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

const defaultMaxLLMBodyBytes = 4 * 1024 * 1024

// Extract sends source text to the configured LLM and returns candidate proposals.
func (c *LLMClient) Extract(ctx context.Context, _ *db.Source, rawText string) ([]StatementProposal, error) {
	client := c.httpClient()
	requestBody := chatCompletionRequest{
		Model: c.Model,
		Messages: []chatCompletionMessage{
			{
				Role:    "system",
				Content: "Return only JSON with statement proposals for the supplied source text.",
			},
			{
				Role:    "user",
				Content: rawText,
			},
		},
		ResponseFormat: map[string]string{
			"type": "json_object",
		},
		Temperature: 0,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"

	// E-09: retry on 429 (rate-limit) and 5xx (transient server errors).
	// Delays: 0s on first attempt, 1s before second, 2s before third.
	// Context cancellation aborts early.
	maxBodyBytes := maxLLMBodyBytes()
	var (
		resp *http.Response
		body []byte
	)
	for attempt := range 3 {
		if attempt > 0 {
			var delay time.Duration
			switch attempt {
			case 1:
				delay = 1 * time.Second
			default:
				delay = 2 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		// Recreate the request each attempt: the body reader is consumed per attempt.
		retryReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if reqErr != nil {
			return nil, reqErr
		}
		retryReq.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			retryReq.Header.Set("Authorization", "Bearer "+c.APIKey)
		}

		resp, err = client.Do(retryReq)
		if err != nil {
			return nil, err
		}
		body, err = io.ReadAll(io.LimitReader(resp.Body, int64(maxBodyBytes)+1))
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close: %v\n", closeErr)
		}
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			fmt.Fprintf(os.Stderr, "llm request: status %d, retrying (attempt %d/3)\n", resp.StatusCode, attempt+1)
			continue
		}
		break
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("llm response body exceeds %d bytes", maxBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("llm request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, err
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("llm response missing choices")
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return nil, fmt.Errorf("llm response missing message content")
	}

	return parseStatementProposals([]byte(content))
}

func (c *LLMClient) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return netx.NewHTTPClient(30*time.Second, netx.ClientPolicy{AllowPrivateHosts: true})
}

func parseStatementProposals(content []byte) ([]StatementProposal, error) {
	var direct []StatementProposal
	if err := json.Unmarshal(content, &direct); err == nil {
		return direct, nil
	}

	// E-05: if the wrapper unmarshals successfully, return the result even when
	// both slices are empty — that means the LLM found no facts, which is valid.
	var wrapper struct {
		Proposals  []StatementProposal `json:"proposals"`
		Statements []StatementProposal `json:"statements"`
	}
	if err := json.Unmarshal(content, &wrapper); err == nil {
		if len(wrapper.Proposals) > 0 {
			return wrapper.Proposals, nil
		}
		// wrapper.Statements may also be empty — still not a parse error.
		return wrapper.Statements, nil
	}

	return nil, fmt.Errorf("unable to parse statement proposals")
}

// VerifyExcerptOffset checks that an excerpt appears at the claimed rune offsets.
func VerifyExcerptOffset(rawText, excerpt string, offsetStart, offsetEnd int) bool {
	if offsetStart < 0 || offsetEnd < offsetStart {
		return false
	}

	startByte, ok := runeOffsetToByteOffset(rawText, offsetStart)
	if !ok {
		return false
	}
	endByte, ok := runeOffsetToByteOffset(rawText, offsetEnd)
	if !ok {
		return false
	}
	if startByte > endByte || endByte > len(rawText) {
		return false
	}
	return rawText[startByte:endByte] == excerpt
}

// FilterVerifiedStatementProposals drops proposals that fail excerpt verification.
func FilterVerifiedStatementProposals(rawText string, proposals []StatementProposal) []StatementProposal {
	out := make([]StatementProposal, 0, len(proposals))
	for _, proposal := range proposals {
		if VerifyExcerptOffset(rawText, proposal.Excerpt, proposal.ExcerptOffsetStart, proposal.ExcerptOffsetEnd) {
			out = append(out, proposal)
		}
	}
	return out
}

func runeOffsetToByteOffset(rawText string, runeOffset int) (int, bool) {
	if runeOffset == 0 {
		return 0, true
	}
	if runeOffset < 0 {
		return 0, false
	}
	index := 0
	for byteOffset := range rawText {
		if index == runeOffset {
			return byteOffset, true
		}
		index++
	}
	if index == runeOffset {
		return len(rawText), true
	}
	return 0, false
}

func maxLLMBodyBytes() int {
	size, err := config.ResolveInt(nil, "FACTVAULT_MAX_LLM_BODY_BYTES", defaultMaxLLMBodyBytes, false)
	if err != nil || size <= 0 {
		return defaultMaxLLMBodyBytes
	}
	return size
}
