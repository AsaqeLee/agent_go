package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultEmbedModel = "text-embedding-3-small"
const embedBatch = 64

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed implements Embedder via POST /v1/embeddings.
func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(texts) == 0 {
		return nil, nil
	}
	model := o.EmbedModel
	if model == "" {
		model = defaultEmbedModel
	}
	out := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += embedBatch {
		end := start + embedBatch
		if end > len(texts) {
			end = len(texts)
		}
		part, err := o.embedBatch(ctx, model, texts[start:end])
		if err != nil {
			return nil, err
		}
		copy(out[start:], part)
	}
	return out, nil
}

func (o *OpenAI) embedBatch(ctx context.Context, model string, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embeddingsRequest{Model: model, Input: texts})
	if err != nil {
		return nil, err
	}
	url := o.BaseURL + "/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	httpResp, err := o.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: embeddings http: %w", err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, httpStatusError(httpResp.StatusCode, raw)
	}
	var parsed embeddingsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("llm: embeddings unmarshal: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("llm: embeddings: %s", parsed.Error.Message)
	}
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("llm: embeddings index %d out of range", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("llm: missing embedding for input %d", i)
		}
	}
	return out, nil
}
