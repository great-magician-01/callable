package model

import (
	"encoding/json"
	"fmt"
)

// StopReason is why a model turn ended, normalized across providers.
type StopReason string

const (
	// StopReasonEndTurn means the model finished its answer naturally.
	StopReasonEndTurn StopReason = "end_turn"
	// StopReasonToolCalls means the model requested tool calls.
	StopReasonToolCalls StopReason = "tool_calls"
	// StopReasonMaxTokens means generation was cut off by the token limit.
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonOther covers provider-specific reasons (content filters, ...).
	StopReasonOther StopReason = "other"
)

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	// ID is the provider-assigned call id, echoed back with the result.
	ID string
	// Name is the tool name.
	Name string
	// Arguments is the raw JSON arguments object.
	Arguments string
}

// Usage is token accounting for a request, normalized across providers.
type Usage struct {
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	// ContextTokens is the total number of tokens the request occupied in
	// the model's context window, normalized across providers: for OpenAI
	// it equals prompt tokens (which already include cached tokens); for
	// Anthropic it is input + cache-read + cache-creation tokens. Sessions
	// use it to measure context fill.
	ContextTokens int `json:"context_tokens,omitempty"`
	// Extra preserves usage fields the unified model does not map — new
	// provider accounting fields or gateway extensions — keyed by field name
	// in their original JSON form.
	Extra map[string]json.RawMessage `json:"extra,omitempty"`
}

// Add accumulates usage from another turn (used by the agent loop). Unmodeled
// Extra fields merge with the later turn's value winning per key.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.ReasoningTokens += o.ReasoningTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.ContextTokens += o.ContextTokens
	for k, v := range o.Extra {
		if u.Extra == nil {
			u.Extra = map[string]json.RawMessage{}
		}
		u.Extra[k] = v
	}
}

// Response is a completed model turn in the unified model.
type Response struct {
	// Message is the full assistant message, including ThinkingPart and
	// ToolCallPart contents when present.
	Message Message
	// Text is the concatenated text output (convenience).
	Text string
	// ToolCalls lists requested tool invocations in order (convenience).
	ToolCalls []ToolCall
	// StopReason explains why the turn ended.
	StopReason StopReason
	// Usage reports token consumption for this turn.
	Usage Usage
	// Extra preserves top-level response fields the unified model does not
	// map — wire metadata such as id/model/created, gateway additions, or
	// provider fields added after this library version — keyed by field name
	// in their original JSON form.
	Extra map[string]json.RawMessage
}

// DecodeJSON unmarshals Text into v. It is the companion of structured
// output (Request.WithResponseFormat) but works with any JSON text response.
func (r *Response) DecodeJSON(v any) error {
	if err := json.Unmarshal([]byte(r.Text), v); err != nil {
		return fmt.Errorf("callable: decode response JSON: %w", err)
	}
	return nil
}
