package core

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
	InputTokens      int
	OutputTokens     int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int
	// ContextTokens is the total number of tokens the request occupied in
	// the model's context window, normalized across providers: for OpenAI
	// it equals prompt tokens (which already include cached tokens); for
	// Anthropic it is input + cache-read + cache-creation tokens. Sessions
	// use it to measure context fill.
	ContextTokens int
}

// Add accumulates usage from another turn (used by the agent loop).
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.ReasoningTokens += o.ReasoningTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
	u.ContextTokens += o.ContextTokens
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
}
