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

// OpenAIEmbed implements Embedder against OpenAI's embeddings endpoint.
type OpenAIEmbed struct {
	apiKey string
	model  string
	rec    UsageRecorder
	http   *http.Client
}

func NewOpenAIEmbed(apiKey, model string, rec UsageRecorder) *OpenAIEmbed {
	return &OpenAIEmbed{apiKey: apiKey, model: model, rec: rec, http: &http.Client{}}
}

func (o *OpenAIEmbed) ID() string { return "openai:" + o.model }

func (o *OpenAIEmbed) Dim() int {
	switch o.model {
	case "text-embedding-3-small", "text-embedding-ada-002":
		return 1536
	case "text-embedding-3-large":
		return 3072
	default:
		return 0
	}
}

type openaiEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiEmbedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage openaiUsage  `json:"usage"`
	Error *openaiError `json:"error,omitempty"`
}

type openaiError struct {
	Message string `json:"message"`
}

func (o *OpenAIEmbed) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	event := UsageEvent{
		At: start, Provider: "openai", Model: o.model, Kind: KindEmbed, Items: len(texts),
	}
	defer func() {
		event.LatencyMs = time.Since(start).Milliseconds()
		recordSafely(ctx, o.rec, event)
	}()

	if len(texts) == 0 {
		event.OK = true
		return nil, nil
	}
	body, _ := json.Marshal(openaiEmbedReq{Model: o.model, Input: texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		event.ErrorMsg = err.Error()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.http.Do(req)
	if err != nil {
		event.ErrorMsg = err.Error()
		return nil, fmt.Errorf("openai embed request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		msg := fmt.Sprintf("openai embed %s: %s", resp.Status, string(raw))
		event.ErrorMsg = msg
		return nil, fmt.Errorf("%s", msg)
	}
	var parsed openaiEmbedResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		event.ErrorMsg = err.Error()
		return nil, fmt.Errorf("decode openai embed response: %w", err)
	}
	if parsed.Error != nil {
		event.ErrorMsg = parsed.Error.Message
		return nil, fmt.Errorf("openai error: %s", parsed.Error.Message)
	}
	if len(parsed.Data) != len(texts) {
		event.ErrorMsg = "vector count mismatch"
		return nil, fmt.Errorf("openai returned %d vectors for %d inputs", len(parsed.Data), len(texts))
	}
	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	event.OK = true
	event.InputTokens = parsed.Usage.PromptTokens
	if event.InputTokens == 0 {
		event.InputTokens = parsed.Usage.TotalTokens
	}
	event.CostMicrocentsUSD = computeCost("openai", o.model, event.InputTokens, 0)
	return out, nil
}

// OpenAIReason implements Reasoner against OpenAI's chat completions.
type OpenAIReason struct {
	apiKey string
	model  string
	rec    UsageRecorder
	http   *http.Client
}

func NewOpenAIReason(apiKey, model string, rec UsageRecorder) *OpenAIReason {
	return &OpenAIReason{apiKey: apiKey, model: model, rec: rec, http: &http.Client{}}
}

func (o *OpenAIReason) ID() string { return "openai:" + o.model }

type openaiChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiChatReq struct {
	Model     string          `json:"model"`
	Messages  []openaiChatMsg `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type openaiChatResp struct {
	Choices []struct {
		Message openaiChatMsg `json:"message"`
	} `json:"choices"`
	Usage openaiUsage  `json:"usage"`
	Error *openaiError `json:"error,omitempty"`
}

func (o *OpenAIReason) Reason(ctx context.Context, req ReasonRequest) (string, error) {
	start := time.Now()
	event := UsageEvent{
		At: start, Provider: "openai", Model: o.model, Kind: KindReason, Items: len(req.Messages),
	}
	defer func() {
		event.LatencyMs = time.Since(start).Milliseconds()
		recordSafely(ctx, o.rec, event)
	}()

	msgs := make([]openaiChatMsg, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openaiChatMsg{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, openaiChatMsg{Role: m.Role, Content: m.Content})
	}
	body, _ := json.Marshal(openaiChatReq{Model: o.model, Messages: msgs, MaxTokens: req.MaxTokens})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		event.ErrorMsg = err.Error()
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.http.Do(httpReq)
	if err != nil {
		event.ErrorMsg = err.Error()
		return "", fmt.Errorf("openai chat request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		msg := fmt.Sprintf("openai chat %s: %s", resp.Status, string(raw))
		event.ErrorMsg = msg
		return "", fmt.Errorf("%s", msg)
	}
	var parsed openaiChatResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		event.ErrorMsg = err.Error()
		return "", fmt.Errorf("decode openai chat response: %w", err)
	}
	if parsed.Error != nil {
		event.ErrorMsg = parsed.Error.Message
		return "", fmt.Errorf("openai error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		event.ErrorMsg = "no choices"
		return "", fmt.Errorf("openai returned no choices")
	}
	event.OK = true
	event.InputTokens = parsed.Usage.PromptTokens
	event.OutputTokens = parsed.Usage.CompletionTokens
	event.CostMicrocentsUSD = computeCost("openai", o.model, event.InputTokens, event.OutputTokens)
	return parsed.Choices[0].Message.Content, nil
}
