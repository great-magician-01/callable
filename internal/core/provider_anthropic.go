package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicProvider talks the Anthropic Messages format
// (POST {baseURL}/v1/messages).
type AnthropicProvider struct {
	api httpAPI
}

// NewAnthropicProvider creates an Anthropic Messages provider for the given
// endpoint, e.g. AnthropicURL. A baseURL already ending in /v1 is tolerated.
// Anthropic-compatible third-party endpoints are available as constants, e.g.
// DeepSeekAnthropicURL.
func NewAnthropicProvider(apiKey, baseURL string, opts ...ProviderOption) *AnthropicProvider {
	cfg := defaultProviderConfig(baseURL)
	for _, o := range opts {
		o(&cfg)
	}
	return &AnthropicProvider{
		api: httpAPI{
			name: "anthropic",
			cfg:  cfg,
			decorate: func(r *http.Request) {
				r.Header.Set("x-api-key", apiKey)
				r.Header.Set("anthropic-version", "2023-06-01")
			},
		},
	}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

// endpoint returns the messages path, tolerating a base URL that already
// includes /v1.
func (p *AnthropicProvider) endpoint() string {
	if strings.HasSuffix(p.api.cfg.baseURL, "/v1") {
		return "/messages"
	}
	return "/v1/messages"
}

// ── wire types ─────────────────────────────────────────────────────────────

type antPayload struct {
	Model         string           `json:"model"`
	MaxTokens     int              `json:"max_tokens"`
	System        string           `json:"system,omitempty"`
	Messages      []antMessage     `json:"messages"`
	Tools         []any            `json:"tools,omitempty"` // antTool or a server tool entry
	Thinking      *antThinking     `json:"thinking,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	OutputConfig  *antOutputConfig `json:"output_config,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
}

// antOutputConfig carries Anthropic's native structured-output control.
type antOutputConfig struct {
	Format antOutputFormat `json:"format"`
}

type antOutputFormat struct {
	Type   string         `json:"type"` // "json_schema"
	Schema map[string]any `json:"schema"`
}

type antThinking struct {
	Type         string `json:"type"` // "enabled"
	BudgetTokens int    `json:"budget_tokens"`
}

type antMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content any    `json:"content"`
}

type antTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type antTextBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type antImageBlock struct {
	Type   string         `json:"type"` // "image"
	Source antImageSource `json:"source"`
}

type antImageSource struct {
	Type      string `json:"type"` // "base64" | "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type antThinkingBlock struct {
	Type      string `json:"type"` // "thinking"
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type antToolUseBlock struct {
	Type  string          `json:"type"` // "tool_use"
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type antToolResultBlock struct {
	Type      string `json:"type"` // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ── request building ───────────────────────────────────────────────────────

func (p *AnthropicProvider) buildPayload(req *Request, stream bool) ([]byte, error) {
	system, messages, err := p.convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	payload := antPayload{
		Model:    req.Model,
		System:   system,
		Messages: messages,
	}
	for _, t := range req.Tools {
		def := t.Definition()
		schema := def.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		payload.Tools = append(payload.Tools, antTool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: schema,
		})
	}
	if req.WebSearch && p.supportsWebSearch() == webSearchServer {
		// Anthropic server-side web search; the server_tool_use and
		// web_search_tool_result blocks it emits are not mapped into the
		// unified message, only the final text is.
		payload.Tools = append(payload.Tools, map[string]any{
			"type": "web_search_20250305",
			"name": DefaultWebSearchToolName,
		})
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	if req.Thinking != nil && req.Thinking.Enabled() {
		budget := req.Thinking.anthropicBudgetTokens()
		if budget < 1024 { // Anthropic minimum
			budget = 1024
		}
		// Anthropic requires max_tokens > budget_tokens.
		if maxTokens <= budget {
			maxTokens = budget + 2048
		}
		payload.Thinking = &antThinking{Type: "enabled", BudgetTokens: budget}
	} else {
		// Thinking mode requires temperature 1; omit sampling parameters when on.
		payload.Temperature = req.Temperature
		payload.TopP = req.TopP
	}
	payload.StopSequences = req.Stop
	payload.OutputConfig = antOutputConfigFor(req.Format)
	payload.MaxTokens = maxTokens
	payload.Stream = stream
	return mergeExtraJSON(payload, req.Extra)
}

// antOutputConfigFor maps the unified ResponseFormat onto Anthropic's native
// output_config.format. Anthropic has no schema-less JSON mode, so a
// free-form JSON request becomes a permissive object schema. The Strict flag
// is inherent: Anthropic always enforces the schema.
func antOutputConfigFor(f *ResponseFormat) *antOutputConfig {
	if f == nil {
		return nil
	}
	schema := f.Schema
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return &antOutputConfig{Format: antOutputFormat{Type: "json_schema", Schema: schema}}
}

// convertMessages maps unified messages to Anthropic messages. Consecutive
// same-role messages are merged (Anthropic expects alternating turns; a
// thinking block is preserved with its signature so tool-use loops continue
// correctly).
func (p *AnthropicProvider) convertMessages(messages []Message) (string, []antMessage, error) {
	system, rest := splitSystem(messages)
	var out []antMessage
	appendBlocks := func(role string, blocks []any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content.([]any), blocks...)
			return
		}
		out = append(out, antMessage{Role: role, Content: blocks})
	}

	for _, m := range rest {
		role := "user"
		if m.Role == RoleAssistant {
			role = "assistant"
		}
		var blocks []any
		for _, part := range m.Parts {
			switch v := part.(type) {
			case TextPart:
				blocks = append(blocks, antTextBlock{Type: "text", Text: v.Text})
			case ThinkingPart:
				if v.Text == "" && v.Signature == "" {
					continue
				}
				blocks = append(blocks, antThinkingBlock{
					Type:      "thinking",
					Thinking:  v.Text,
					Signature: v.Signature,
				})
			case ToolCallPart:
				input := json.RawMessage(v.Arguments)
				if strings.TrimSpace(v.Arguments) == "" {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, antToolUseBlock{
					Type:  "tool_use",
					ID:    v.ID,
					Name:  v.Name,
					Input: input,
				})
			case ToolResultPart:
				content := v.Content
				if v.IsError && !strings.HasPrefix(content, "Error") {
					// Surface the failure to the model.
					content = "Error: " + content
				}
				blocks = append(blocks, antToolResultBlock{
					Type:      "tool_result",
					ToolUseID: v.ToolCallID,
					Content:   content,
					IsError:   v.IsError,
				})
			case ImagePart:
				if role != "user" {
					continue // images are user-only input
				}
				resolved, err := resolveImage(v)
				if err != nil {
					return "", nil, err
				}
				var source antImageSource
				if resolved.URL != "" {
					source = antImageSource{Type: "url", URL: resolved.URL}
				} else {
					source = antImageSource{
						Type:      "base64",
						MediaType: resolved.MediaType,
						Data:      base64.StdEncoding.EncodeToString(resolved.Data),
					}
				}
				blocks = append(blocks, antImageBlock{Type: "image", Source: source})
			}
		}
		appendBlocks(role, blocks)
	}
	return system, out, nil
}

// ── response parsing ───────────────────────────────────────────────────────

type antErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type antUsageBody struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u antUsageBody) toUsage() Usage {
	return Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		// Anthropic reports non-cached input only; the full context footprint
		// includes cache reads and creations.
		ContextTokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
	}
}

// antContentBlock is the flattened union of Anthropic content block types.
type antContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type antResponseEnvelope struct {
	Content    []antContentBlock `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      antUsageBody      `json:"usage"`
	Error      *antErrorBody     `json:"error"`
}

// Create performs a non-streaming request.
func (p *AnthropicProvider) Create(ctx context.Context, req *Request) (*Response, error) {
	payload, err := p.buildPayload(req, false)
	if err != nil {
		return nil, err
	}
	body, err := p.api.postJSON(ctx, p.endpoint(), payload)
	if err != nil {
		return nil, err
	}
	return p.parseResponse(body)
}

func (p *AnthropicProvider) parseResponse(body []byte) (*Response, error) {
	var env antResponseEnvelope
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
	return assembleAntMessage(env.Content, mapAntStopReason(env.StopReason), env.Usage.toUsage()), nil
}

func mapAntStopReason(reason string) StopReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return StopReasonEndTurn
	case "tool_use":
		return StopReasonToolCalls
	case "max_tokens":
		return StopReasonMaxTokens
	default:
		return StopReasonOther
	}
}

// assembleAntMessage builds the unified Response from content blocks.
func assembleAntMessage(blocks []antContentBlock, stop StopReason, usage Usage) *Response {
	assistant := Message{Role: RoleAssistant}
	resp := &Response{Usage: usage, StopReason: stop}
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			assistant.Parts = append(assistant.Parts, ThinkingPart{
				Text:      b.Thinking,
				Signature: b.Signature,
			})
		case "text":
			assistant.Parts = append(assistant.Parts, TextPart{Text: b.Text})
			resp.Text += b.Text
		case "tool_use":
			args := compactJSON(string(b.Input))
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			assistant.Parts = append(assistant.Parts, ToolCallPart{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: args,
			})
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: args,
			})
		}
	}
	resp.Message = assistant
	return resp
}

// ── streaming ──────────────────────────────────────────────────────────────

// antBlockAccum accumulates one streamed content block.
type antBlockAccum struct {
	Type      string
	Text      strings.Builder
	Thinking  strings.Builder
	Signature string
	ID        string
	Name      string
	inputJSON strings.Builder
}

type antStreamState struct {
	started    bool
	blocks     []*antBlockAccum
	stopReason string
	usage      antUsageBody
}

// Stream performs a streaming request, reassembling content_block events.
func (p *AnthropicProvider) Stream(ctx context.Context, req *Request, onEvent eventSink) (*Response, error) {
	payload, err := p.buildPayload(req, true)
	if err != nil {
		return nil, err
	}
	body, err := p.api.postJSONStream(ctx, p.endpoint(), payload)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	state := &antStreamState{}
	scanErr := scanSSE(body, func(ev sseMessage) error {
		if strings.TrimSpace(ev.data) == "" {
			return nil
		}
		return state.processEvent(ev, onEvent)
	})
	if scanErr != nil && !errors.Is(scanErr, errStopScan) {
		if cerr := ctx.Err(); cerr != nil {
			// Graceful stop: return the partially assembled response
			// alongside the context error.
			return state.assemble(), cerr
		}
		return nil, fmt.Errorf("callable: read %s stream: %w", p.Name(), scanErr)
	}
	resp := state.assemble()
	if onEvent != nil {
		onEvent(MessageDoneEvent{Message: resp.Message, Usage: resp.Usage, StopReason: resp.StopReason})
	}
	return resp, nil
}

// assemble builds the response from the blocks accumulated so far.
func (s *antStreamState) assemble() *Response {
	if s.stopReason == "" {
		// Stream ended without message_delta; infer the stop reason.
		s.stopReason = "end_turn"
		for _, b := range s.blocks {
			if b.Type == "tool_use" {
				s.stopReason = "tool_use"
				break
			}
		}
	}
	blocks := make([]antContentBlock, len(s.blocks))
	for i, b := range s.blocks {
		input := b.inputJSON.String()
		if strings.TrimSpace(input) == "" {
			input = "{}"
		}
		blocks[i] = antContentBlock{
			Type:      b.Type,
			Text:      b.Text.String(),
			Thinking:  b.Thinking.String(),
			Signature: b.Signature,
			ID:        b.ID,
			Name:      b.Name,
			Input:     json.RawMessage(input),
		}
	}
	return assembleAntMessage(blocks, mapAntStopReason(s.stopReason), s.usage.toUsage())
}

func (s *antStreamState) processEvent(ev sseMessage, onEvent eventSink) error {
	var env struct {
		Type    string `json:"type"`
		Message struct {
			Usage antUsageBody `json:"usage"`
		} `json:"message"`
		Index        int              `json:"index"`
		ContentBlock *antContentBlock `json:"content_block"`
		Delta        json.RawMessage  `json:"delta"`
		DeltaUsage   *antUsageBody    `json:"usage"` // message_delta
		StopReason   string           `json:"stop_reason"`
		Error        *antErrorBody    `json:"error"`
	}
	if err := json.Unmarshal([]byte(ev.data), &env); err != nil {
		return fmt.Errorf("decode event: %w", err)
	}
	kind := env.Type
	if kind == "" {
		kind = ev.event
	}

	switch kind {
	case "message_start":
		if !s.started {
			s.started = true
			s.usage = env.Message.Usage
			if onEvent != nil {
				onEvent(MessageStartEvent{})
			}
		}
	case "content_block_start":
		block := &antBlockAccum{}
		if env.ContentBlock != nil {
			block.Type = env.ContentBlock.Type
			block.ID = env.ContentBlock.ID
			block.Name = env.ContentBlock.Name
		}
		for len(s.blocks) <= env.Index {
			s.blocks = append(s.blocks, &antBlockAccum{})
		}
		s.blocks[env.Index] = block
		if block.Type == "tool_use" && onEvent != nil {
			onEvent(ToolCallDeltaEvent{Index: env.Index, ID: block.ID, Name: block.Name})
		}
	case "content_block_delta":
		if env.Index >= len(s.blocks) {
			return fmt.Errorf("content_block_delta for unknown block %d", env.Index)
		}
		block := s.blocks[env.Index]
		var delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			Signature   string `json:"signature"`
			PartialJSON string `json:"partial_json"`
		}
		if err := json.Unmarshal(env.Delta, &delta); err != nil {
			return fmt.Errorf("decode content_block_delta: %w", err)
		}
		switch delta.Type {
		case "text_delta":
			block.Text.WriteString(delta.Text)
			if onEvent != nil {
				onEvent(TextDeltaEvent{Delta: delta.Text})
			}
		case "thinking_delta":
			block.Thinking.WriteString(delta.Thinking)
			if onEvent != nil {
				onEvent(ThinkingDeltaEvent{Delta: delta.Thinking})
			}
		case "signature_delta":
			block.Signature += delta.Signature
		case "input_json_delta":
			block.inputJSON.WriteString(delta.PartialJSON)
			if onEvent != nil {
				onEvent(ToolCallDeltaEvent{Index: env.Index, ArgsDelta: delta.PartialJSON})
			}
		}
	case "content_block_stop":
		// nothing to do; block accumulated already
	case "message_delta":
		// {"delta":{"stop_reason":...},"usage":{"output_tokens":...}}
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		if len(env.Delta) > 0 {
			if err := json.Unmarshal(env.Delta, &delta); err == nil && delta.StopReason != "" {
				s.stopReason = delta.StopReason
			}
		}
		if env.DeltaUsage != nil {
			s.usage.OutputTokens = env.DeltaUsage.OutputTokens
			if env.DeltaUsage.InputTokens > 0 {
				s.usage.InputTokens = env.DeltaUsage.InputTokens
			}
		}
	case "message_stop":
		return errStopScan
	case "ping":
		// keep-alive
	case "error":
		e := env.Error
		if e == nil {
			e = &antErrorBody{Message: ev.data}
		}
		return &APIError{Provider: "anthropic", Type: e.Type, Message: e.Message, Body: ev.data}
	}
	return nil
}
