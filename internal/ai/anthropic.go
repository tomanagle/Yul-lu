package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Anthropic implements Reasoner against the Anthropic Messages API.
// Anthropic does not currently publish a text-embedding model, so pair this
// with a separate Embedder (fastembed, ollama, openai, voyage) for storage.
type Anthropic struct {
	apiKey string
	model  string
	rec    UsageRecorder
	http   *http.Client
}

func NewAnthropic(apiKey, model string, rec UsageRecorder) *Anthropic {
	return &Anthropic{apiKey: apiKey, model: model, rec: rec, http: &http.Client{}}
}

func (a *Anthropic) ID() string { return "anthropic:" + a.model }

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicReq struct {
	Model     string         `json:"model"`
	System    string         `json:"system,omitempty"`
	Messages  []anthropicMsg `json:"messages"`
	MaxTokens int            `json:"max_tokens"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage anthropicUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (a *Anthropic) Reason(ctx context.Context, req ReasonRequest) (string, error) {
	start := time.Now()
	event := UsageEvent{
		At: start, Provider: "anthropic", Model: a.model, Kind: KindReason, Items: len(req.Messages),
	}
	defer func() {
		event.LatencyMs = time.Since(start).Milliseconds()
		recordSafely(ctx, a.rec, event)
	}()

	msgs := make([]anthropicMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, anthropicMsg{Role: m.Role, Content: m.Content})
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 1024
	}
	body, _ := json.Marshal(anthropicReq{
		Model: a.model, System: req.System, Messages: msgs, MaxTokens: maxTok,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		event.ErrorMsg = err.Error()
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := a.http.Do(httpReq)
	if err != nil {
		event.ErrorMsg = err.Error()
		return "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		msg := fmt.Sprintf("anthropic %s: %s", resp.Status, string(raw))
		event.ErrorMsg = msg
		return "", fmt.Errorf("%s", msg)
	}
	var parsed anthropicResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		event.ErrorMsg = err.Error()
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	if parsed.Error != nil {
		event.ErrorMsg = parsed.Error.Message
		return "", fmt.Errorf("anthropic error: %s", parsed.Error.Message)
	}
	var out bytes.Buffer
	for _, c := range parsed.Content {
		if c.Type == "text" {
			out.WriteString(c.Text)
		}
	}
	event.OK = true
	event.InputTokens = parsed.Usage.InputTokens
	event.OutputTokens = parsed.Usage.OutputTokens
	event.CostMicrocentsUSD = computeCost("anthropic", a.model, event.InputTokens, event.OutputTokens)
	return out.String(), nil
}
