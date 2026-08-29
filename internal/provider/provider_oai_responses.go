package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	model "github.com/great-magician-01/callable/internal/model"
)

// OpenAIResponsesProvider talks the OpenAI Responses format
// (POST {baseURL}/responses).
//
// Reasoning items received in a response are preserved verbatim on the
// assistant Message (via provider round-trip data) and replayed on the next
// request, which is how the Responses API maintains reasoning continuity
// across tool calls.
type OpenAIResponsesProvider struct {
	api httpAPI
}

// NewOpenAIResponsesProvider creates a Responses provider for the given
// endpoint. baseURL is the API root including any version prefix, e.g.
// OpenAIURL.
func NewOpenAIResponsesProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIResponsesProvider {
	cfg := defaultProviderConfig(baseURL)
	for _, o := range opts {
		o(&cfg)
	}
	return &OpenAIResponsesProvider{
		api: httpAPI{name: "openai-responses", cfg: cfg, decorate: bearerAuth(apiKey)},
	}
}

func (p *OpenAIResponsesProvider) Name() string { return "openai-responses" }

// ── wire types ─────────────────────────────────────────────────────────────

type respPayload struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           []any           `json:"input"`
	Tools           []any           `json:"tools,omitempty"` // respToolDef or a hosted tool entry
	Reasoning       *respReasoning  `json:"reasoning,omitempty"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Text            *respTextConfig `json:"text,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
}

// respTextConfig carries the Responses text.format structured-output control.
type respTextConfig struct {
	Format any `json:"format"`
}

type respReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"` // "auto" streams reasoning summaries
}

type respToolDef struct {
	Type        string         `json:"type"` // "function"
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type respUserItem struct {
	Role    string            `json:"role"` // "user"
	Content []respUserContent `json:"content"`
}

type respUserContent struct {
	Type     string `json:"type"` // "input_text" | "input_image"
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type respAssistantItem struct {
	Type    string                 `json:"type"` // "message"
	Role    string                 `json:"role"` // "assistant"
	Content []respAssistantContent `json:"content"`
}

type respAssistantContent struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

type respFunctionCallItem struct {
	Type      string `json:"type"` // "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type respFunctionCallOutputItem struct {
	Type   string `json:"type"` // "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// respOutputItem is the flattened union of response output item types.
type respOutputItem struct {
	Type      string `json:"type"` // "reasoning" | "message" | "function_call"
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Summary   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// ── request building ───────────────────────────────────────────────────────

func (p *OpenAIResponsesProvider) buildPayload(req *model.Request, stream bool) ([]byte, error) {
	instructions, input, err := p.buildInput(req.Messages)
	if err != nil {
		return nil, err
	}
	payload := respPayload{
		Model:        req.Model,
		Instructions: instructions,
		Input:        input,
	}
	for _, t := range req.Tools {
		def := t.Definition()
		payload.Tools = append(payload.Tools, respToolDef{
			Type:        "function",
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		})
	}
	if req.WebSearch && p.supportsWebSearch() == WebSearchServer {
		// Hosted web search tool; the server executes it and returns
		// web_search_call items, which round-trip verbatim via the raw
		// output replay.
		payload.Tools = append(payload.Tools, map[string]any{"type": "web_search"})
	}
	thinkingOn := req.Thinking != nil && req.Thinking.Enabled()
	if thinkingOn {
		payload.Reasoning = &respReasoning{
			Effort:  string(effectiveEffort(*req.Thinking)),
			Summary: "auto",
		}
	} else {
		payload.Temperature = req.Temperature
		payload.TopP = req.TopP
	}
	if f := respTextFormat(req.Format); f != nil {
		payload.Text = &respTextConfig{Format: f}
	}
	if req.MaxTokens > 0 {
		n := req.MaxTokens
		payload.MaxOutputTokens = &n
	}
	payload.Stream = stream
	return mergeExtraJSON(payload, req.Extra)
}

// respTextFormat maps the unified ResponseFormat onto the Responses
// text.format field.
func respTextFormat(f *model.ResponseFormat) any {
	if f == nil {
		return nil
	}
	if f.Schema == nil {
		return map[string]any{"type": "json_object"}
	}
	return map[string]any{
		"type":   "json_schema",
		"name":   schemaName(f),
		"schema": f.Schema,
		"strict": f.Strict,
	}
}

// buildInput converts unified messages into Responses input items. Assistant
// messages that carry raw output items from a previous response replay them
// verbatim (preserving reasoning items); others are reconstructed from parts.
func (p *OpenAIResponsesProvider) buildInput(messages []model.Message) (string, []any, error) {
	system, rest := splitSystem(messages)
	var input []any

	for _, m := range rest {
		if m.Role == model.RoleAssistant {
			if raw := m.ProviderExtra(p.Name()); len(raw) > 0 {
				var items []json.RawMessage
				if err := json.Unmarshal(raw, &items); err == nil && len(items) > 0 {
					for _, item := range items {
						input = append(input, item)
					}
					continue
				}
			}
			var text strings.Builder
			for _, part := range m.Parts {
				switch v := part.(type) {
				case model.TextPart:
					text.WriteString(v.Text)
				case model.ThinkingPart:
					// Reasoning items cannot be reconstructed faithfully;
					// they are only replayed via provider round-trip data.
				case model.ToolCallPart:
					input = append(input, respFunctionCallItem{
						Type:      "function_call",
						CallID:    v.ID,
						Name:      v.Name,
						Arguments: v.Arguments,
					})
				case model.ToolResultPart:
					input = append(input, respFunctionCallOutputItem{
						Type:   "function_call_output",
						CallID: v.ToolCallID,
						Output: v.Content,
					})
				}
			}
			if text.Len() > 0 {
				input = append(input, respAssistantItem{
					Type:    "message",
					Role:    "assistant",
					Content: []respAssistantContent{{Type: "output_text", Text: text.String()}},
				})
			}
			continue
		}

		// User / tool messages: function_call_output items must directly
		// follow the function_call items they answer.
		var contents []respUserContent
		var results []model.ToolResultPart
		for _, part := range m.Parts {
			switch v := part.(type) {
			case model.TextPart:
				contents = append(contents, respUserContent{Type: "input_text", Text: v.Text})
			case model.ImagePart:
				resolved, err := model.ResolveImage(v)
				if err != nil {
					return "", nil, err
				}
				u := resolved.URL
				if u == "" {
					u = resolved.DataURL()
				}
				contents = append(contents, respUserContent{Type: "input_image", ImageURL: u})
			case model.ToolResultPart:
				results = append(results, v)
			}
		}
		for _, r := range results {
			output := r.Content
			if r.IsError {
				output = "Error: " + output
			}
			input = append(input, respFunctionCallOutputItem{
				Type:   "function_call_output",
				CallID: r.ToolCallID,
				Output: output,
			})
		}
		if len(contents) > 0 {
			input = append(input, respUserItem{Role: "user", Content: contents})
		}
	}
	return system, input, nil
}

// ── response parsing ───────────────────────────────────────────────────────

type respUsageBody struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (u *respUsageBody) toUsage() model.Usage {
	if u == nil {
		return model.Usage{}
	}
	usage := model.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		// InputTokens already includes cached tokens, so it is the full
		// context footprint.
		ContextTokens: u.InputTokens,
	}
	if u.InputTokensDetails != nil {
		usage.CacheReadTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		usage.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return usage
}

type respEnvelope struct {
	ID     string            `json:"id"`
	Status string            `json:"status"` // completed | incomplete | failed
	Output []json.RawMessage `json:"output"`
	Usage  *respUsageBody    `json:"usage"`
	Error  *oaiErrorBody     `json:"error"`
	// IncompleteDetails carries the truncation reason for incomplete
	// responses.
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

// Create performs a non-streaming request.
func (p *OpenAIResponsesProvider) Create(ctx context.Context, req *model.Request) (*model.Response, error) {
	payload, err := p.buildPayload(req, false)
	if err != nil {
		return nil, err
	}
	body, err := p.api.postJSON(ctx, "/responses", payload)
	if err != nil {
		return nil, err
	}
	var env respEnvelope
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
	return p.assemble(&env)
}

// assemble converts a response envelope into the unified Response, attaching
// the raw output items to the assistant message for round-trip replay.
func (p *OpenAIResponsesProvider) assemble(env *respEnvelope) (*model.Response, error) {
	resp := &model.Response{Usage: env.Usage.toUsage()}
	assistant := model.Message{Role: model.RoleAssistant}

	for _, raw := range env.Output {
		var item respOutputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue // tolerate unknown item types
		}
		switch item.Type {
		case "reasoning":
			var thinking strings.Builder
			for _, s := range item.Summary {
				thinking.WriteString(s.Text)
			}
			for _, c := range item.Content { // raw reasoning text, when exposed
				thinking.WriteString(c.Text)
			}
			assistant.Parts = append(assistant.Parts, model.ThinkingPart{ID: item.ID, Text: thinking.String()})
		case "message":
			for _, c := range item.Content {
				if c.Text != "" {
					assistant.Parts = append(assistant.Parts, model.TextPart{Text: c.Text})
					resp.Text += c.Text
				}
			}
		case "function_call":
			assistant.Parts = append(assistant.Parts, model.ToolCallPart{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: compactJSON(item.Arguments),
			})
			resp.ToolCalls = append(resp.ToolCalls, model.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: compactJSON(item.Arguments),
			})
		}
	}

	switch {
	case len(resp.ToolCalls) > 0:
		resp.StopReason = model.StopReasonToolCalls
	case env.Status == "incomplete":
		resp.StopReason = model.StopReasonMaxTokens
	default:
		resp.StopReason = model.StopReasonEndTurn
	}

	// Preserve raw output items (reasoning continuity across tool calls).
	// This must happen before resp.Message is set: SetProviderExtra lazily
	// initializes the map on this Message value.
	if raw, err := json.Marshal(env.Output); err == nil && len(env.Output) > 0 {
		assistant.SetProviderExtra(p.Name(), raw)
	}
	resp.Message = assistant
	return resp, nil
}

// ── streaming ──────────────────────────────────────────────────────────────

// respStreamState tracks streamed events until response.completed delivers
// the authoritative response object.
type respStreamState struct {
	started bool
	items   []json.RawMessage // output_item.done payloads
}

// Stream performs a streaming request.
func (p *OpenAIResponsesProvider) Stream(ctx context.Context, req *model.Request, onEvent model.EventSink) (*model.Response, error) {
	payload, err := p.buildPayload(req, true)
	if err != nil {
		return nil, err
	}
	body, err := p.api.postJSONStream(ctx, "/responses", payload)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	state := &respStreamState{}
	var final *model.Response
	scanErr := scanSSE(body, func(ev sseMessage) error {
		if strings.TrimSpace(ev.data) == "" {
			return nil
		}
		out, err := p.processEvent(ev.data, state, onEvent)
		if err != nil {
			return err
		}
		if out != nil {
			final = out
			return errStopScan
		}
		return nil
	})
	if scanErr != nil && !errors.Is(scanErr, errStopScan) {
		if cerr := ctx.Err(); cerr != nil {
			// Graceful stop: assemble whatever completed output items were
			// received and return them alongside the context error.
			resp, aerr := p.assemble(&respEnvelope{Status: "incomplete", Output: state.items})
			if aerr != nil {
				return nil, cerr
			}
			return resp, cerr
		}
		return nil, fmt.Errorf("callable: read %s stream: %w", p.Name(), scanErr)
	}
	if final != nil {
		if onEvent != nil {
			onEvent(model.MessageDoneEvent{Message: final.Message, Usage: final.Usage, StopReason: final.StopReason})
		}
		return final, nil
	}
	// Stream ended without response.completed; fall back to accumulated
	// output_item.done items.
	env := &respEnvelope{Status: "completed", Output: state.items}
	resp, err := p.assemble(env)
	if err != nil {
		return nil, err
	}
	if onEvent != nil {
		onEvent(model.MessageDoneEvent{Message: resp.Message, Usage: resp.Usage, StopReason: resp.StopReason})
	}
	return resp, nil
}

// processEvent handles one SSE event. It returns a non-nil Response when the
// stream is complete (response.completed / response.incomplete).
func (p *OpenAIResponsesProvider) processEvent(data string, state *respStreamState, onEvent model.EventSink) (*model.Response, error) {
	var ev struct {
		Type        string          `json:"type"`
		Delta       string          `json:"delta"`
		OutputIndex int             `json:"output_index"`
		Item        json.RawMessage `json:"item"`
		Response    json.RawMessage `json:"response"`
		Error       *oaiErrorBody   `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}

	switch ev.Type {
	case "response.created":
		if !state.started {
			state.started = true
			if onEvent != nil {
				onEvent(model.MessageStartEvent{})
			}
		}
	case "response.output_item.added":
		var item struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
		}
		if json.Unmarshal(ev.Item, &item) == nil && item.Type == "function_call" && onEvent != nil {
			onEvent(model.ToolCallDeltaEvent{Index: ev.OutputIndex, ID: item.CallID, Name: item.Name})
		}
	case "response.output_text.delta":
		if onEvent != nil {
			onEvent(model.TextDeltaEvent{Delta: ev.Delta})
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if onEvent != nil {
			onEvent(model.ThinkingDeltaEvent{Delta: ev.Delta})
		}
	case "response.function_call_arguments.delta":
		if onEvent != nil {
			onEvent(model.ToolCallDeltaEvent{Index: ev.OutputIndex, ArgsDelta: ev.Delta})
		}
	case "response.output_item.done":
		if len(ev.Item) > 0 {
			state.items = append(state.items, ev.Item)
		}
	case "response.completed", "response.incomplete":
		var env respEnvelope
		if err := json.Unmarshal(ev.Response, &env); err != nil {
			return nil, fmt.Errorf("decode %s: %w", ev.Type, err)
		}
		if env.Status == "" {
			env.Status = strings.TrimPrefix(ev.Type, "response.")
		}
		return p.assemble(&env)
	case "response.failed":
		var env respEnvelope
		_ = json.Unmarshal(ev.Response, &env)
		e := env.Error
		if e == nil {
			e = ev.Error
		}
		if e == nil {
			e = &oaiErrorBody{Message: data}
		}
		return nil, &APIError{Provider: p.Name(), Type: e.Type, Message: e.Message, Body: data}
	case "error":
		e := ev.Error
		if e == nil {
			e = &oaiErrorBody{Message: data}
		}
		return nil, &APIError{Provider: p.Name(), Type: e.Type, Message: e.Message, Body: data}
	}
	return nil, nil
}
