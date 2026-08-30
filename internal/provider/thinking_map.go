package provider

import (
	model "github.com/great-magician-01/callable/internal/model"
)

// The thinking helpers below map the unified Thinking/Effort configuration
// (internal/model) onto provider-native controls. Only the provider adapters
// use them.

// anthropicBudget maps a unified effort level to an Anthropic
// thinking.budget_tokens value. Anthropic requires a budget of at least 1024
// tokens and strictly less than max_tokens.
func anthropicBudget(e model.Effort) int {
	switch e {
	case model.EffortLow:
		return 2048
	case model.EffortMedium:
		return 8192
	case model.EffortHigh:
		return 16384
	default:
		return 0
	}
}

// effectiveEffort resolves the effort used by OpenAI-style providers when
// BudgetTokens is the only signal.
func effectiveEffort(t model.Thinking) model.Effort {
	if t.Effort != model.EffortOff {
		return t.Effort
	}
	if t.BudgetTokens > 0 {
		return model.EffortMedium
	}
	return model.EffortOff
}

// glmEffort maps a unified effort onto values every reasoning_effort-capable
// GLM model accepts: GLM-5.3 rejects medium, and GLM-5.2 folds low/medium
// into high server-side anyway, so medium is sent as high.
func glmEffort(e model.Effort) model.Effort {
	if e == model.EffortMedium {
		return model.EffortHigh
	}
	return e
}

// anthropicBudgetTokens returns the Anthropic thinking budget, preferring an
// explicit BudgetTokens over the Effort mapping.
func anthropicBudgetTokens(t model.Thinking) int {
	if t.BudgetTokens > 0 {
		return t.BudgetTokens
	}
	return anthropicBudget(t.Effort)
}
