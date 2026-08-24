package core

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

// anthropicBudget maps a unified effort level to an Anthropic
// thinking.budget_tokens value. Anthropic requires a budget of at least 1024
// tokens and strictly less than max_tokens.
func (e Effort) anthropicBudget() int {
	switch e {
	case EffortLow:
		return 2048
	case EffortMedium:
		return 8192
	case EffortHigh:
		return 16384
	default:
		return 0
	}
}

// Thinking configures model reasoning ("thinking" / "extended thinking" /
// "reasoning"). The zero value disables thinking.
//
// The unified Effort level is translated per provider:
//
//	Anthropic           -> thinking.budget_tokens (low≈2048 / medium≈8192 / high≈16384)
//	OpenAI Responses    -> reasoning.effort ("low" / "medium" / "high")
//	OpenAI Chat Compl.  -> reasoning_effort
//	GLM compat          -> thinking: {type: "enabled"}
//	Qwen compat         -> enable_thinking: true
//
// BudgetTokens overrides the Effort mapping for Anthropic only.
type Thinking struct {
	Effort Effort
	// BudgetTokens is an explicit Anthropic thinking budget. When set (and
	// Effort is unset) it implies enabled for all providers.
	BudgetTokens int
}

// Enabled reports whether thinking is on.
func (t Thinking) Enabled() bool {
	return t.Effort != EffortOff || t.BudgetTokens > 0
}

// effectiveEffort resolves the effort used by OpenAI-style providers when
// BudgetTokens is the only signal.
func (t Thinking) effectiveEffort() Effort {
	if t.Effort != EffortOff {
		return t.Effort
	}
	if t.BudgetTokens > 0 {
		return EffortMedium
	}
	return EffortOff
}

// anthropicBudgetTokens returns the Anthropic thinking budget, preferring an
// explicit BudgetTokens over the Effort mapping.
func (t Thinking) anthropicBudgetTokens() int {
	if t.BudgetTokens > 0 {
		return t.BudgetTokens
	}
	return t.Effort.anthropicBudget()
}
