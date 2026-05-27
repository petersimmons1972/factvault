package extractors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/netx"
)

type StatementProposal struct {
	SubjectText        string `json:"subject_text"`
	PropertySlug       string `json:"property_slug"`
	Value              string `json:"value"`
	ValueType          string `json:"value_type"`
	Excerpt            string `json:"excerpt"`
	ExcerptOffsetStart int    `json:"excerpt_offset_start"`
	ExcerptOffsetEnd   int    `json:"excerpt_offset_end"`
}

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

func (c *LLMClient) Extract(ctx context.Context, source *db.Source, rawText string) ([]StatementProposal, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
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

	var wrapper struct {
		Proposals  []StatementProposal `json:"proposals"`
		Statements []StatementProposal `json:"statements"`
	}
	if err := json.Unmarshal(content, &wrapper); err == nil {
		if len(wrapper.Proposals) > 0 {
			return wrapper.Proposals, nil
		}
		if len(wrapper.Statements) > 0 {
			return wrapper.Statements, nil
		}
	}

	return nil, fmt.Errorf("unable to parse statement proposals")
}

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
