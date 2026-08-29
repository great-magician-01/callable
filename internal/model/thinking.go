package model

// Effort is the unified reasoning-effort level. It maps onto each provider's
// native thinking controls (see Thinking).
type Effort string

const (
	// EffortOff disables thinking entirely (the default zero value).
	EffortOff Effort = ""
	// EffortLow asks the model for brief reasoning.
	EffortLow Effort = "low"
	// EffortMedium is a balanced default.
	EffortMedium Effort = "medium"
	// EffortHigh asks for deep reasoning.
	EffortHigh Effort = "high"
)

// Thinking configures model reasoning ("thinking" / "extended thinking" /
// "reasoning"). The zero value disables thinking.
//
// The unified Effort level is translated per provider:
//
//	Anthropic           -> thinking.budget_tokens (low≈2048 / medium≈8192 / high≈16384)
//	OpenAI Responses    -> reasoning.effort ("low" / "medium" / "high")
//	OpenAI Chat Compl.  -> reasoning_effort
//	GLM compat          -> thinking: {type: "enabled"} + reasoning_effort (medium→high)
//	Ark compat          -> thinking: {type: "enabled"} + reasoning_effort
//	DeepSeek compat     -> thinking: {type: "enabled"} + reasoning_effort (on by default; medium maps to high)
//	Qwen compat         -> enable_thinking: true (+ thinking_budget from BudgetTokens)
//
// BudgetTokens sets an explicit thinking budget for Anthropic (budget_tokens)
// and Qwen (thinking_budget); other providers ignore it.
type Thinking struct {
	Effort Effort
	// BudgetTokens is an explicit thinking budget (Anthropic budget_tokens,
	// Qwen thinking_budget). When set (and Effort is unset) it implies
	// enabled for all providers.
	BudgetTokens int
}

// Enabled reports whether thinking is on.
func (t Thinking) Enabled() bool {
	return t.Effort != EffortOff || t.BudgetTokens > 0
}
