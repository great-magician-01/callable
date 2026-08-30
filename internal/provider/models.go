package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ModelInfo describes one model from a provider's model listing.
type ModelInfo struct {
	// ID is the model id to pass to requests, e.g. "gpt-5.2" or
	// "claude-sonnet-5".
	ID string
	// DisplayName is the human-readable name (Anthropic only; empty
	// otherwise).
	DisplayName string
	// OwnedBy is the model owner (OpenAI-compatible endpoints only; empty
	// otherwise).
	OwnedBy string
	// Created is the model's creation time; zero when the endpoint omits it.
	Created time.Time
}

// ModelLister is implemented by providers that can list the models available
// at their endpoint (GET /models). All three built-in providers implement it.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// ── OpenAI-compatible (Chat Completions and Responses) ─────────────────────

type oaiModelListEnvelope struct {
	Data []struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
	Error *oaiErrorBody `json:"error"`
}

// listOpenAIModels fetches GET {baseURL}/models, shared by the OpenAI Chat
// Completions and Responses providers.
func listOpenAIModels(ctx context.Context, api *httpAPI) ([]ModelInfo, error) {
	body, err := api.getJSON(ctx, "/models", nil)
	if err != nil {
		return nil, err
	}
	var env oaiModelListEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, &APIError{
			Provider: api.name,
			Message:  fmt.Sprintf("decode response: %v", err),
			Body:     string(body),
		}
	}
	if env.Error != nil {
		return nil, &APIError{
			Provider: api.name,
			Type:     env.Error.Type,
			Message:  env.Error.Message,
			Body:     string(body),
		}
	}
	models := make([]ModelInfo, 0, len(env.Data))
	for _, m := range env.Data {
		info := ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy}
		if m.Created > 0 {
			info.Created = time.Unix(m.Created, 0).UTC()
		}
		models = append(models, info)
	}
	return models, nil
}

// ListModels returns the models available at the endpoint
// (GET {baseURL}/models).
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return listOpenAIModels(ctx, &p.api)
}

// ListModels returns the models available at the endpoint
// (GET {baseURL}/models).
func (p *OpenAIResponsesProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return listOpenAIModels(ctx, &p.api)
}

// ── Anthropic ───────────────────────────────────────────────────────────────

type antModelListEnvelope struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		CreatedAt   string `json:"created_at"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// modelsEndpoint returns the model-listing path, tolerating a base URL that
// already includes /v1 (mirrors endpoint()).
func (p *AnthropicProvider) modelsEndpoint() string {
	if strings.HasSuffix(p.api.cfg.baseURL, "/v1") {
		return "/models"
	}
	return "/v1/models"
}

// ListModels returns the models available at the endpoint
// (GET {baseURL}/v1/models), following pagination until the full list is
// collected.
func (p *AnthropicProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var models []ModelInfo
	afterID := ""
	for {
		q := url.Values{"limit": {strconv.Itoa(1000)}}
		if afterID != "" {
			q.Set("after_id", afterID)
		}
		body, err := p.api.getJSON(ctx, p.modelsEndpoint()+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var env antModelListEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, &APIError{
				Provider: p.Name(),
				Message:  fmt.Sprintf("decode response: %v", err),
				Body:     string(body),
			}
		}
		if env.Error != nil {
			return nil, &APIError{
				Provider: p.Name(),
				Type:     env.Error.Type,
				Message:  env.Error.Message,
				Body:     string(body),
			}
		}
		for _, m := range env.Data {
			info := ModelInfo{ID: m.ID, DisplayName: m.DisplayName}
			if m.CreatedAt != "" {
				if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
					info.Created = t
				}
			}
			models = append(models, info)
		}
		if !env.HasMore || env.LastID == "" {
			return models, nil
		}
		afterID = env.LastID
	}
}
