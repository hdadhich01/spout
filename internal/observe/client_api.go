package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hdadhich01/spout/internal/config"
)

// Anthropic Messages API. Implemented with net/http rather than the SDK to
// keep Spout's dependency set minimal (one endpoint, no version churn).
const (
	anthropicURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	apiMaxTokens     = 512
)

// apiClient talks to the Anthropic Messages API. The frozen system block
// carries a cache_control breakpoint so repeat checks of a run reuse it.
type apiClient struct {
	key   string
	model string
	http  *http.Client
}

func newAPIClient(o *config.Observe, key string) *apiClient {
	return &apiClient{
		key:   key,
		model: o.ModelID(),
		http:  &http.Client{Timeout: callTimeout},
	}
}

type apiCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type apiBlock struct {
	Type         string           `json:"type"` // "text"
	Text         string           `json:"text"`
	CacheControl *apiCacheControl `json:"cache_control,omitempty"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    []apiBlock   `json:"system"`
	Messages  []apiMessage `json:"messages"`
}

type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *apiClient) Observe(ctx context.Context, req Request) (*Observation, error) {
	payload := apiRequest{
		Model:     c.model,
		MaxTokens: apiMaxTokens,
		System: []apiBlock{{
			Type:         "text",
			Text:         systemPrompt(req),
			CacheControl: &apiCacheControl{Type: "ephemeral"},
		}},
		Messages: []apiMessage{{Role: "user", Content: userPrompt(req)}},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.key)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, err
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("anthropic: %s", ar.Error.Message)
	}
	var text strings.Builder
	for _, blk := range ar.Content {
		if blk.Type == "text" {
			text.WriteString(blk.Text)
		}
	}
	return parseObservation(text.String())
}

// parseObservation extracts the first JSON object from the model's text and
// unmarshals it into an Observation. Tolerant of stray prose or code fences.
func parseObservation(text string) (*Observation, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in model output")
	}
	var obs Observation
	if err := json.Unmarshal([]byte(text[start:end+1]), &obs); err != nil {
		return nil, err
	}
	return &obs, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
