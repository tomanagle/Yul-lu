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

// Voyage implements Embedder against Voyage AI's embedding endpoint.
type Voyage struct {
	apiKey string
	model  string
	rec    UsageRecorder
	http   *http.Client
}

func NewVoyage(apiKey, model string, rec UsageRecorder) *Voyage {
	return &Voyage{apiKey: apiKey, model: model, rec: rec, http: &http.Client{}}
}

func (v *Voyage) ID() string { return "voyage:" + v.model }

func (v *Voyage) Dim() int {
	switch v.model {
	case "voyage-code-3", "voyage-3", "voyage-3-large":
		return 1024
	case "voyage-3-lite":
		return 512
	default:
		return 0
	}
}

type voyageReq struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	InputType string   `json:"input_type,omitempty"`
}

type voyageResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Detail string `json:"detail,omitempty"`
}

func (v *Voyage) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	event := UsageEvent{
		At: start, Provider: "voyage", Model: v.model, Kind: KindEmbed, Items: len(texts),
	}
	defer func() {
		event.LatencyMs = time.Since(start).Milliseconds()
		recordSafely(ctx, v.rec, event)
	}()

	if len(texts) == 0 {
		event.OK = true
		return nil, nil
	}
	body, _ := json.Marshal(voyageReq{Model: v.model, Input: texts, InputType: "document"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.voyageai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		event.ErrorMsg = err.Error()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)
	resp, err := v.http.Do(req)
	if err != nil {
		event.ErrorMsg = err.Error()
		return nil, fmt.Errorf("voyage request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		msg := fmt.Sprintf("voyage embed %s: %s", resp.Status, string(raw))
		event.ErrorMsg = msg
		return nil, fmt.Errorf("%s", msg)
	}
	var parsed voyageResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		event.ErrorMsg = err.Error()
		return nil, fmt.Errorf("decode voyage response: %w", err)
	}
	if parsed.Detail != "" && len(parsed.Data) == 0 {
		event.ErrorMsg = parsed.Detail
		return nil, fmt.Errorf("voyage error: %s", parsed.Detail)
	}
	if len(parsed.Data) != len(texts) {
		event.ErrorMsg = "vector count mismatch"
		return nil, fmt.Errorf("voyage returned %d vectors for %d inputs", len(parsed.Data), len(texts))
	}
	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	event.OK = true
	event.InputTokens = parsed.Usage.TotalTokens
	if event.InputTokens == 0 {
		event.InputTokens = estimateTokens(texts...)
	}
	event.CostMicrocentsUSD = computeCost("voyage", v.model, event.InputTokens, 0)
	return out, nil
}
