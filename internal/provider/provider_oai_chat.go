package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	model "github.com/great-magician-01/callable/internal/model"
)

// OpenAIProvider talks the OpenAI Chat Completions format
// (POST {baseURL}/chat/completions). Because most third-party LLM endpoints
// are OpenAI-compatible, pointing baseURL at them makes this provider
// work for GLM, DeepSeek, Qwen, vLLM and friends; thinking-mode dialects are
// auto-detected (see Compat).
type OpenAIProvider struct {
	api    httpAPI
	compat Compat
}

// NewOpenAIProvider creates a Chat Completions provider for the given endpoint.
// baseURL is the API root including any version prefix; well-known endpoints
// are available as constants (OpenAIURL, DeepSeekURL, GLMURL, ...).
func NewOpenAIProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIProvider {
	cfg := defaultProviderConfig(baseURL)
	for _, o := range opts {
		o(&cfg)
	}
	compat := cfg.compat | detectCompat(cfg.baseURL)
	return &OpenAIProvider{
		api:    httpAPI{name: "openai", cfg: cfg, decorate: bearerAuth(apiKey)},
		compat: compat,
	}
}

func (p *OpenAIProvider) Name() string { return "openai" }

// ── wire types ─────────────────────────────────────────────────────────────

type oaiChatPayload struct {
	Model               string             `json:"model"`
	Messages            []oaiChatMessage   `json:"messages"`
	Tools               []any              `json:"tools,omitempty"` // oaiChatTool or a built-in tool entry
	ToolChoice          string             `json:"tool_choice,omitempty"`
	MaxTokens           *int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	Stop                []string           `json:"stop,omitempty"`
	ResponseFormat      any                `json:"response_format,omitempty"`
	ReasoningEffort     string             `json:"reasoning_effort,omitempty"`
	Thinking            *oaiCompatThinking `json:"thinking,omitempty"`        // GLM / Ark / DeepSeek
	EnableThinking      *bool              `json:"enable_thinking,omitempty"` // Qwen
	ThinkingBudget      *int               `json:"thinking_budget,omitempty"` // Qwen
	EnableSearch        bool               `json:"enable_search,omitempty"`   // Qwen built-in web search
	Stream              bool               `json:"stream,omitempty"`
	StreamOptions       *oaiStreamOptions  `json:"stream_options,omitempty"`
}

type oaiCompatThinking struct {
	Type string `json:"type"` // "enabled" | "disabled"
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiChatMessage struct {
	Role       string            `json:"role"`
	Content    any               `json:"content,omitempty"` // string | []oaiUserContentPart
	ToolCalls  []oaiToolCallWire `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	// ReasoningContent is resent in assistant messages for thinking models on
	// compatible endpoints (GLM/Qwen).
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type oaiUserContentPart struct {
	Type     string       `json:"type"` // "text" | "image_url"
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiImageURL struct {
	URL string `json:"url"` // remote URL or data: URL
}

type oaiToolCallWire struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"` // "function"
	Function oaiFunctionCallWire `json:"function"`
	// Index is present only in streaming deltas.
	Index *int `json:"index,omitempty"`
}

type oaiFunctionCallWire struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiChatTool struct {
	Type     string             `json:"type"` // "function"
	Function oaiChatFunctionDef `json:"function"`
}

type oaiChatFunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ── request building ───────────────────────────────────────────────────────

func (p *OpenAIProvider) buildPayload(req *model.Request, stream bool) ([]byte, error) {
	messages, err := p.convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	payload := oaiChatPayload{Model: req.Model, Messages: messages}

	kimiBuiltin := req.WebSearch && p.supportsWebSearch() == WebSearchEcho
	for _, t := range req.Tools {
		def := t.Definition()
		if kimiBuiltin && def.Name == kimiWebSearchToolName {
			// Kimi's built-in search is advertised as builtin_function, not
			// as a plain function tool; the agent echoes the call arguments
			// back so the server performs the search on the next request.
			payload.Tools = append(payload.Tools, map[string]any{
				"type":     "builtin_function",
				"function": map[string]any{"name": kimiWebSearchToolName},
			})
			continue
		}
		payload.Tools = append(payload.Tools, oaiChatTool{
			Type: "function",
			Function: oaiChatFunctionDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
		})
	}
	if req.WebSearch {
		switch {
		case p.compat&CompatGLM != 0:
			// GLM / Z.AI server-side search.
			payload.Tools = append(payload.Tools, map[string]any{
				"type": "web_search",
				"web_search": map[string]any{
					"enable":        true,
					"search_result": true,
				},
			})
		case p.compat&CompatQwen != 0:
			// Qwen server-side search is a top-level switch, not a tool entry.
			payload.EnableSearch = true
		}
	}
	if len(payload.Tools) > 0 {
		payload.ToolChoice = "auto"
	}

	if req.MaxTokens > 0 {
		n := req.MaxTokens
		if isOfficialOpenAI(p.api.cfg.baseURL) {
			// Reasoning models reject max_tokens in favor of
			// max_completion_tokens.
			payload.MaxCompletionTokens = &n
		} else {
			payload.MaxTokens = &n
		}
	}

	thinkingOn := req.Thinking != nil && req.Thinking.Enabled()
	switch {
	case thinkingOn && p.compat&CompatGLM != 0:
		// GLM-5.2+: reasoning_effort alongside the switch. medium is mapped
		// to high: GLM-5.3 rejects medium outright, and 5.2 folds low/medium
		// into high server-side anyway.
		payload.Thinking = &oaiCompatThinking{Type: "enabled"}
		payload.ReasoningEffort = string(glmEffort(effectiveEffort(*req.Thinking)))
	case thinkingOn && p.compat&CompatArk != 0:
		// Ark accepts low/medium/high natively; pass the effort through.
		payload.Thinking = &oaiCompatThinking{Type: "enabled"}
		payload.ReasoningEffort = string(effectiveEffort(*req.Thinking))
	case thinkingOn && p.compat&CompatDeepSeek != 0:
		// DeepSeek V4: explicit switch plus effort. medium maps to high
		// server-side; thinking is on by default when nothing is sent.
		payload.Thinking = &oaiCompatThinking{Type: "enabled"}
		payload.ReasoningEffort = string(effectiveEffort(*req.Thinking))
	case thinkingOn && p.compat&CompatQwen != 0:
		payload.EnableThinking = ptr(true)
		if req.Thinking.BudgetTokens > 0 {
			payload.ThinkingBudget = ptr(req.Thinking.BudgetTokens)
		}
	case thinkingOn:
		payload.ReasoningEffort = string(effectiveEffort(*req.Thinking))
	case req.Thinking != nil: // explicitly disabled
		// DeepSeek defaults to thinking on, so disabling must be explicit.
		// (GLM-5.3 rejects "disabled" outright; its 400 says to use low.)
		if p.compat&(CompatGLM|CompatArk|CompatDeepSeek) != 0 {
			payload.Thinking = &oaiCompatThinking{Type: "disabled"}
		}
		if p.compat&CompatQwen != 0 {
			payload.EnableThinking = ptr(false)
		}
	}
	if !thinkingOn {
		// Thinking models reject custom sampling parameters; omit when thinking.
		payload.Temperature = req.Temperature
		payload.TopP = req.TopP
	}
	payload.Stop = req.Stop
	payload.ResponseFormat = oaiChatResponseFormat(req.Format)
	if req.Format != nil && req.Format.Schema != nil && p.compat&(CompatDeepSeek|CompatGLM) != 0 {
		// DeepSeek rejects json_schema outright and GLM/Z.AI documents only
		// text/json_object. Downgrade to json_object and spell the schema out
		// in the prompt instead — both vendors' recommended way to steer
		// JSON-mode conformance.
		payload.ResponseFormat = map[string]any{"type": "json_object"}
		payload.Messages = appendSchemaHint(payload.Messages, req.Format.Schema)
	}

	if stream {
		payload.Stream = true
		if isOfficialOpenAI(p.api.cfg.baseURL) {
			payload.StreamOptions = &oaiStreamOptions{IncludeUsage: true}
		}
	}
	return mergeExtraJSON(payload, req.Extra)
}

// oaiChatResponseFormat maps the unified ResponseFormat onto the Chat
// Completions response_format field.
func oaiChatResponseFormat(f *model.ResponseFormat) any {
	if f == nil {
		return nil
	}
	if f.Schema == nil {
		return map[string]any{"type": "json_object"}
	}
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   schemaName(f),
			"schema": f.Schema,
			"strict": f.Strict,
		},
	}
}

// appendSchemaHint appends a "conform to this JSON Schema" instruction to the
// last user message of already-converted chat messages (or adds a trailing
// user message when there is none). Used by the DeepSeek json_object
// downgrade; the caller's Messages are untouched.
func appendSchemaHint(messages []oaiChatMessage, schema map[string]any) []oaiChatMessage {
	hint := "\n\nRespond with a JSON object that conforms to this JSON Schema: " + mustJSON(schema)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		switch c := messages[i].Content.(type) {
		case string:
			messages[i].Content = c + hint
		case []oaiUserContentPart:
			messages[i].Content = append(c, oaiUserContentPart{Type: "text", Text: hint})
		case nil:
			messages[i].Content = strings.TrimSpace(hint)
		}
		return messages
	}
	return append(messages, oaiChatMessage{Role: "user", Content: strings.TrimSpace(hint)})
}

// convertMessages maps unified messages to chat-completions messages. System
// messages are merged into a leading system message; tool results become
// role="tool" messages; images become image_url parts (data URLs for local
// files).
func (p *OpenAIProvider) convertMessages(messages []model.Message) ([]oaiChatMessage, error) {
	system, rest := splitSystem(messages)
	var out []oaiChatMessage
	if system != "" {
		out = append(out, oaiChatMessage{Role: "system", Content: system})
	}
	for _, m := range rest {
		var (
			text        strings.Builder
			reasoning   strings.Builder
			images      []model.ImagePart
			toolCalls   []oaiToolCallWire
			toolResults []model.ToolResultPart
		)
		for _, part := range m.Parts {
			switch v := part.(type) {
			case model.TextPart:
				text.WriteString(v.Text)
			case model.ImagePart:
				images = append(images, v)
			case model.ThinkingPart:
				reasoning.WriteString(v.Text)
			case model.ToolCallPart:
				toolCalls = append(toolCalls, oaiToolCallWire{
					ID:       v.ID,
					Type:     "function",
					Function: oaiFunctionCallWire{Name: v.Name, Arguments: v.Arguments},
				})
			case model.ToolResultPart:
				toolResults = append(toolResults, v)
			}
		}

		// Tool results must directly follow the assistant tool_calls message.
		for _, tr := range toolResults {
			out = append(out, oaiChatMessage{
				Role:       "tool",
				ToolCallID: tr.ToolCallID,
				Content:    tr.Content,
			})
		}

		switch m.Role {
		case model.RoleAssistant:
			msg := oaiChatMessage{Role: "assistant", ToolCalls: toolCalls}
			if text.Len() > 0 {
				msg.Content = text.String()
			}
			msg.ReasoningContent = reasoning.String()
			out = append(out, msg)
		default:
			if text.Len() == 0 && len(images) == 0 {
				continue
			}
			if len(images) == 0 {
				out = append(out, oaiChatMessage{Role: "user", Content: text.String()})
				continue
			}
			parts := make([]oaiUserContentPart, 0, len(images)+1)
			if text.Len() > 0 {
				parts = append(parts, oaiUserContentPart{Type: "text", Text: text.String()})
			}
			for _, img := range images {
				resolved, err := model.ResolveImage(img)
				if err != nil {
					return nil, err
				}
				u := resolved.URL
				if u == "" {
					u = resolved.DataURL()
				}
				parts = append(parts, oaiUserContentPart{
					Type:     "image_url",
					ImageURL: &oaiImageURL{URL: u},
				})
			}
			out = append(out, oaiChatMessage{Role: "user", Content: parts})
		}
	}
	return out, nil
}

// ── response parsing ───────────────────────────────────────────────────────

type oaiErrorBody struct {
	Type    string `json:"type"`
	Code    any    `json:"code"`
	Message string `json:"message"`
}

type oaiUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	// DeepSeek-style cache accounting.
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
}

func (u *oaiUsage) toUsage() model.Usage {
	if u == nil {
		return model.Usage{}
	}
	usage := model.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		// PromptTokens already includes cached tokens, so it is the full
		// context footprint.
		ContextTokens: u.PromptTokens,
	}
	if u.PromptTokensDetails != nil {
		usage.CacheReadTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		usage.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	usage.CacheReadTokens += u.PromptCacheHitTokens
	return usage
}

type oaiChatResponseEnvelope struct {
	Choices []struct {
		Message struct {
			Role             string            `json:"role"`
			Content          string            `json:"content"`
			ReasoningContent string            `json:"reasoning_content"`
			Reasoning        string            `json:"reasoning"` // some compatible endpoints
			ToolCalls        []oaiToolCallWire `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage     `json:"usage"`
	Error *oaiErrorBody `json:"error"`
}

// Create performs a non-streaming request.
func (p *OpenAIProvider) Create(ctx context.Context, req *model.Request) (*model.Response, error) {
	payload, err := p.buildPayload(req, false)
	if err != nil {
		return nil, err
	}
	body, err := p.api.postJSON(ctx, "/chat/completions", payload, req.Headers)
	if err != nil {
		return nil, err
	}
	return p.parseResponse(body)
}

func (p *OpenAIProvider) parseResponse(body []byte) (*model.Response, error) {
	var env oaiChatResponseEnvelope
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
	if len(env.Choices) == 0 {
		return nil, &APIError{
			Provider: p.Name(),
			Message:  "response contained no choices",
			Body:     string(body),
		}
	}
	choice := env.Choices[0]
	assistant := model.Message{Role: model.RoleAssistant}
	if thinking := firstNonEmpty(choice.Message.ReasoningContent, choice.Message.Reasoning); thinking != "" {
		assistant.Parts = append(assistant.Parts, model.ThinkingPart{Text: thinking})
	}
	if choice.Message.Content != "" {
		assistant.Parts = append(assistant.Parts, model.TextPart{Text: choice.Message.Content})
	}
	resp := &model.Response{Text: choice.Message.Content}
	for _, tc := range choice.Message.ToolCalls {
		if tc.Function.Name == "" && tc.ID == "" {
			continue
		}
		args := compactJSON(tc.Function.Arguments)
		assistant.Parts = append(assistant.Parts, model.ToolCallPart{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
		resp.ToolCalls = append(resp.ToolCalls, model.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	resp.Message = assistant // assign after all parts are appended
	resp.StopReason = mapChatFinishReason(choice.FinishReason, len(resp.ToolCalls))
	resp.Usage = env.Usage.toUsage()
	return resp, nil
}

func mapChatFinishReason(reason string, toolCallCount int) model.StopReason {
	switch reason {
	case "tool_calls":
		return model.StopReasonToolCalls
	case "stop", "end_turn":
		return model.StopReasonEndTurn
	case "length":
		return model.StopReasonMaxTokens
	case "":
		if toolCallCount > 0 {
			return model.StopReasonToolCalls
		}
		return model.StopReasonOther
	default:
		return model.StopReasonOther
	}
}

// ── streaming ──────────────────────────────────────────────────────────────

// errStopScan is a sentinel that cleanly terminates SSE scanning.
var errStopScan = errors.New("sse stream complete")

// chatStreamState accumulates chat-completions stream chunks.
type chatStreamState struct {
	started   bool
	text      strings.Builder
	thinking  strings.Builder
	toolCalls []oaiToolCallWire
	finish    string
	usage     *oaiUsage
}

// Stream performs a streaming request. Tool-call arguments arrive as
// index-addressed fragments and are reassembled here.
func (p *OpenAIProvider) Stream(ctx context.Context, req *model.Request, onEvent model.EventSink) (*model.Response, error) {
	payload, err := p.buildPayload(req, true)
	if err != nil {
		return nil, err
	}
	body, err := p.api.postJSONStream(ctx, "/chat/completions", payload, req.Headers)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	state := &chatStreamState{}
	scanErr := scanSSE(body, func(ev sseMessage) error {
		if ev.data == "[DONE]" {
			return errStopScan
		}
		if strings.TrimSpace(ev.data) == "" {
			return nil
		}
		return state.processChunk(ev.data, onEvent)
	})
	if scanErr != nil && !errors.Is(scanErr, errStopScan) {
		if cerr := ctx.Err(); cerr != nil {
			// Graceful stop: return the partially assembled response
			// alongside the context error.
			resp, _ := state.assemble()
			return resp, cerr
		}
		return nil, fmt.Errorf("callable: read %s stream: %w", p.Name(), scanErr)
	}
	resp, err := state.assemble()
	if err != nil {
		return nil, err
	}
	if onEvent != nil {
		onEvent(model.MessageDoneEvent{Message: resp.Message, Usage: resp.Usage, StopReason: resp.StopReason})
	}
	return resp, nil
}

func (s *chatStreamState) processChunk(data string, onEvent model.EventSink) error {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          string            `json:"content"`
				ReasoningContent string            `json:"reasoning_content"`
				Reasoning        string            `json:"reasoning"`
				ToolCalls        []oaiToolCallWire `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *oaiUsage     `json:"usage"`
		Error *oaiErrorBody `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("decode chunk: %w", err)
	}
	if chunk.Error != nil {
		return &APIError{
			Provider: "openai",
			Type:     chunk.Error.Type,
			Message:  chunk.Error.Message,
			Body:     data,
		}
	}
	if !s.started {
		s.started = true
		if onEvent != nil {
			onEvent(model.MessageStartEvent{})
		}
	}
	for i := range chunk.Choices {
		choice := &chunk.Choices[i]
		delta := &choice.Delta
		if delta.Content != "" {
			s.text.WriteString(delta.Content)
			if onEvent != nil {
				onEvent(model.TextDeltaEvent{Delta: delta.Content})
			}
		}
		if thinking := firstNonEmpty(delta.ReasoningContent, delta.Reasoning); thinking != "" {
			s.thinking.WriteString(thinking)
			if onEvent != nil {
				onEvent(model.ThinkingDeltaEvent{Delta: thinking})
			}
		}
		for pos, tc := range delta.ToolCalls {
			idx := pos
			if tc.Index != nil {
				idx = *tc.Index
			}
			for len(s.toolCalls) <= idx {
				s.toolCalls = append(s.toolCalls, oaiToolCallWire{Type: "function"})
			}
			acc := &s.toolCalls[idx]
			isNew := acc.Function.Name == "" && tc.Function.Name != ""
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Function.Name = tc.Function.Name
			}
			acc.Function.Arguments += tc.Function.Arguments
			if onEvent != nil {
				onEvent(model.ToolCallDeltaEvent{
					Index:     idx,
					ID:        firstIf(isNew, tc.ID, ""),
					Name:      firstIf(isNew, tc.Function.Name, ""),
					ArgsDelta: tc.Function.Arguments,
				})
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finish = *choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		u := *chunk.Usage
		s.usage = &u
	}
	return nil
}

func (s *chatStreamState) assemble() (*model.Response, error) {
	assistant := model.Message{Role: model.RoleAssistant}
	if s.thinking.Len() > 0 {
		assistant.Parts = append(assistant.Parts, model.ThinkingPart{Text: s.thinking.String()})
	}
	if s.text.Len() > 0 {
		assistant.Parts = append(assistant.Parts, model.TextPart{Text: s.text.String()})
	}
	resp := &model.Response{Text: s.text.String()}
	for _, tc := range s.toolCalls {
		if tc.Function.Name == "" && tc.ID == "" && tc.Function.Arguments == "" {
			continue
		}
		args := compactJSON(tc.Function.Arguments)
		assistant.Parts = append(assistant.Parts, model.ToolCallPart{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
		resp.ToolCalls = append(resp.ToolCalls, model.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	resp.Message = assistant // assign after all parts are appended
	resp.StopReason = mapChatFinishReason(s.finish, len(resp.ToolCalls))
	resp.Usage = s.usage.toUsage()
	return resp, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstIf(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func ptr[T any](v T) *T { return &v }
