package core

// The thinking helpers below map the unified Thinking/Effort configuration
// (internal/model) onto provider-native controls. They stay in core because
// only the provider adapters use them.

// anthropicBudget maps a unified effort level to an Anthropic
// thinking.budget_tokens value. Anthropic requires a budget of at least 1024
// tokens and strictly less than max_tokens.
func anthropicBudget(e Effort) int {
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

// effectiveEffort resolves the effort used by OpenAI-style providers when
// BudgetTokens is the only signal.
func effectiveEffort(t Thinking) Effort {
	if t.Effort != EffortOff {
		return t.Effort
	}
	if t.BudgetTokens > 0 {
		return EffortMedium
	}
	return EffortOff
}

// glmEffort maps a unified effort onto values every reasoning_effort-capable
// GLM model accepts: GLM-5.3 rejects medium, and GLM-5.2 folds low/medium
// into high server-side anyway, so medium is sent as high.
func glmEffort(e Effort) Effort {
	if e == EffortMedium {
		return EffortHigh
	}
	return e
}

// anthropicBudgetTokens returns the Anthropic thinking budget, preferring an
// explicit BudgetTokens over the Effort mapping.
func anthropicBudgetTokens(t Thinking) int {
	if t.BudgetTokens > 0 {
		return t.BudgetTokens
	}
	return anthropicBudget(t.Effort)
}
