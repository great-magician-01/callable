# Thinking Mode

[中文](../zh/thinking.md) | **English**

Thinking mode (thinking / reasoning / extended thinking) lets the model do a stretch of internal reasoning before answering. callable describes it with a single unified `Thinking` struct and translates it into each provider's native control fields at request time — the same code runs unchanged against Anthropic, OpenAI (Chat Completions / Responses), GLM, DeepSeek, Qwen and Volcano Ark.

## API overview

```go
type Thinking struct {
    Effort       Effort // EffortOff / EffortLow / EffortMedium / EffortHigh
    BudgetTokens int    // explicit token budget for Anthropic / Qwen
}

type Effort string

const (
    EffortOff    Effort = ""       // thinking disabled (zero value)
    EffortLow    Effort = "low"    // brief reasoning
    EffortMedium Effort = "medium" // balanced (recommended default)
    EffortHigh   Effort = "high"   // deep reasoning
)
```

Three entry points configure it:

| Entry point | Scope |
|---|---|
| `callable.WithThinking(t Thinking)` (Agent option) | every model call in the agent loop |
| `req.WithThinking(t Thinking)` (`*Request` method) | a single request |
| `callable.WithSubAgentThinking(t Thinking)` | inside a sub-agent (see [Sub-Agents](subagents.md)) |

Key semantics:

- `Request.Thinking` is a pointer field; leaving it unset (nil) means "send no thinking control fields at all", which is different from passing an explicit `Thinking{}` (the zero value, i.e. `EffortOff`) — the latter means **explicitly disabled**. The distinction matters for DeepSeek, which thinks by default (see below).
- `Thinking.Enabled()` is true iff `Effort != EffortOff || BudgetTokens > 0`.
- Setting only `BudgetTokens` (no `Effort`) enables thinking for all providers; for effort-based providers it is equivalent to `EffortMedium`.
- When thinking is on, the sampling temperature is never sent: Anthropic requires temperature 1 in thinking mode, and OpenAI-style reasoning models reject custom temperatures.

## Per-provider mapping

The unified `Effort` / `BudgetTokens` are translated into native wire fields:

| Provider | Request field(s) | Effort mapping | Notes |
|---|---|---|---|
| Anthropic Messages | `thinking: {type: "enabled", budget_tokens: N}` | low → 2048, medium → 8192, high → 16384 | floor of 1024; `max_tokens > budget` guaranteed automatically |
| OpenAI Responses | `reasoning: {effort: "low"\|"medium"\|"high", summary: "auto"}` | direct | `summary: "auto"` streams reasoning summaries |
| OpenAI Chat Completions | `reasoning_effort` | direct | official OpenAI endpoints get `max_completion_tokens` |
| GLM / Zhipu (incl. Z.AI) | `thinking: {type: "enabled"\|"disabled"}` + `reasoning_effort` | **medium → high** | GLM-5.3 rejects medium |
| Volcano Ark | `thinking: {type: "enabled"\|"disabled"}` + `reasoning_effort` | pass-through | natively accepts low/medium/high |
| Qwen (DashScope) | `enable_thinking: true\|false` + `thinking_budget` | no effort mapping; `BudgetTokens` → `thinking_budget` | budget-driven, not tier-driven |
| DeepSeek | `thinking: {type: "enabled"\|"disabled"}` + `reasoning_effort` | pass-through | thinking on by default (see below) |

`BudgetTokens` only takes effect for Anthropic (`budget_tokens`) and Qwen (`thinking_budget`); other providers ignore it.

## Endpoint gotchas

### Anthropic

- `budget_tokens` has a minimum of 1024; smaller values are raised to 1024.
- Anthropic requires `max_tokens > budget_tokens`. If the explicit or default `max_tokens` violates this, the library bumps it to `budget + 2048`; when `max_tokens` is unset the default is 4096.
- Thinking mode requires temperature 1, so the `temperature` field is omitted entirely while thinking is on.

### OpenAI Responses

- Sends `reasoning: {effort, summary: "auto"}`; `summary: "auto"` makes reasoning summaries arrive as streaming deltas (surfaced as `ThinkingDeltaEvent`, see [Streaming Events](streaming.md)).
- Reasoning continuity across requests relies on Responses' reasoning items: raw output items are stored on the message (`ProviderExtra`) and replayed verbatim on the next turn. A reasoning item **cannot** be reconstructed from a bare `ThinkingPart` — so if you persist history across processes, Anthropic / Chat Completions endpoints restore thinking context fine, but the Responses endpoint loses the reasoning items (the answer text is unaffected).

### OpenAI Chat Completions (official endpoint)

- Sends only the standard `reasoning_effort` field — no compat dialect fields.
- On the official endpoint, `max_tokens` is automatically switched to `max_completion_tokens` (reasoning models reject the legacy `max_tokens`).

### GLM / Zhipu (incl. Z.AI)

- **medium → high**: GLM-5.3 rejects `reasoning_effort: "medium"` outright, and GLM-5.2 folds low/medium into high server-side anyway, so medium is sent as high — a value every reasoning-capable GLM model accepts.
- **GLM-5.3 forces thinking**: explicitly disabling (`Thinking{}`) sends `thinking: {type: "disabled"}`, which GLM-5.3 answers with a 400 (the error tells you to use low instead). Don't try to turn thinking off for GLM-5.3 — use `EffortLow`.
- Z.AI (`z.ai` domains) uses the same dialect as bigmodel.cn.

### Volcano Ark

- `thinking: {type}` + `reasoning_effort` are passed through as-is; no mapping traps.

### Qwen (DashScope)

- Uses the `enable_thinking: true|false` switch plus `thinking_budget`; `reasoning_effort` is never sent.
- Set `BudgetTokens` to control the budget; without it only the switch is sent and the server decides the budget.

### DeepSeek

- **Thinking is on by default** (server-side default effort is high): sending nothing at all still means thinking mode. Disabling must therefore be explicit — pass `callable.Thinking{}` (zero value) to send `thinking: {type: "disabled"}`.
- `medium` is mapped to high server-side, much like GLM; use `EffortLow` for shallow reasoning.
- DeepSeek also exposes an Anthropic-compatible endpoint (`callable.DeepSeekAnthropicURL`), which follows the Anthropic mapping rules when used through the Anthropic provider.

## Dialect auto-detection from the BaseURL

GLM / Ark / Qwen / DeepSeek thinking fields are not OpenAI standards, and sending them to the official endpoint would error. `NewOpenAIProvider` auto-detects the dialect (a `Compat` bitmask) from the base URL's hostname:

| Hostname pattern | Dialect |
|---|---|
| `bigmodel.cn`, `zhipuai`, `z.ai` (incl. subdomains) | `CompatGLM` |
| `volces.com` | `CompatArk` |
| `dashscope` | `CompatQwen` |
| `deepseek` | `CompatDeepSeek` |

With the built-in endpoint constants (`callable.GLMURL`, `callable.DeepSeekURL`, ...) detection just works. Override it with `callable.WithCompat(...)`:

```go
// A self-hosted gateway proxies GLM but the hostname gives no hint — set the dialect manually
callable.NewOpenAIProvider(key, "https://llm-gateway.internal/v1",
    callable.WithCompat(callable.CompatGLM))

// The reverse: a compatible endpoint rejects every non-standard field — strip the sniffed dialect
callable.NewOpenAIProvider(key, url, callable.WithCompat(callable.CompatNone))
```

`Compat` is a bitmask and can be OR-combined (e.g. `CompatGLM | CompatQwen`), though that's rarely needed.

## Response parsing: always lenient

Regardless of whether thinking is enabled or which endpoint you're talking to, response parsing is **always lenient**: any `reasoning_content` / `reasoning` field, Anthropic thinking block, or Responses reasoning item returned by any endpoint is parsed into a `ThinkingPart` on the assistant message, and into `ThinkingDeltaEvent` while streaming. There is no switch on the parsing side — even if detection misfires or the config is conservative, thinking content is never dropped.

```go
type ThinkingPart struct {
    Text      string `json:"text"`                // the reasoning text
    Signature string `json:"signature,omitempty"` // Anthropic thinking block signature (round-trip only)
    ID        string `json:"id,omitempty"`        // OpenAI Responses reasoning item id (round-trip only)
}
```

Tokens spent on reasoning are reported in `Usage.ReasoningTokens`.

## Faithful round-tripping of thinking blocks in history

Tool loops are multi-turn: assistant messages (including thinking blocks and tool calls) are sent back as context, and every endpoint has hard requirements about how thinking must round-trip. The library preserves and replays it verbatim:

- **Anthropic**: the thinking block is sent back together with its `signature` (the server validates it and errors when it's missing).
- **OpenAI Responses**: the raw reasoning item is replayed verbatim via `ProviderExtra`, preserving reasoning continuity.
- **GLM / Qwen / DeepSeek**: the thinking text goes back as `reasoning_content` on the assistant message.

As long as you use the built-in [Agent](agent.md) loop or a [Session](session.md), all of this happens automatically — `ThinkingPart`s in the history are serialized and replayed with each request, no manual handling needed.

## Complete example

The following (adapted from `examples/thinking/`) enables thinking at `EffortMedium`, asks two questions in one Session, and prints the reasoning stream dimmed to stderr:

```go
package main

import (
	"context"
	"fmt"
	"os"

	callable "github.com/great-magician-01/callable"
)

func main() {
	ctx := context.Background()
	key := os.Getenv("ANTHROPIC_API_KEY")

	client := callable.NewClient(
		callable.NewAnthropicProvider(key, callable.AnthropicURL),
		callable.WithModel("claude-sonnet-5"),
	)

	agent := callable.NewAgent(client,
		// EffortLow / EffortMedium / EffortHigh map to each provider's
		// native thinking controls (budget_tokens, reasoning.effort,
		// reasoning_effort, GLM thinking, Qwen enable_thinking...).
		callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
	)

	// Anthropic also accepts an explicit budget (== thinking.budget_tokens):
	// callable.WithThinking(callable.Thinking{BudgetTokens: 16000})

	session := agent.Session()
	questions := []string{
		"How many three-digit numbers have digits summing to 12, with all digits distinct?",
		"Summarize your reasoning in two sentences.",
	}

	for _, q := range questions {
		fmt.Printf("\n== Q: %s\nA: ", q)
		_, err := session.AskStream(ctx, func(ev callable.Event) {
			switch e := ev.(type) {
			case callable.ThinkingDeltaEvent:
				fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // reasoning stream, dimmed
			case callable.TextDeltaEvent:
				fmt.Print(e.Delta) // answer stream
			}
		}, callable.User(q))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println()
	}
}
```

```bash
ANTHROPIC_API_KEY=... go run ./examples/thinking
```

Switching providers only means changing the two client-construction lines; `WithThinking` stays untouched:

```go
// DeepSeek: thinking is on by default; pass callable.Thinking{} to disable it explicitly
client = callable.NewClient(
	callable.NewOpenAIProvider(key, callable.DeepSeekURL),
	callable.WithModel("deepseek-v4"),
)

// Qwen: BudgetTokens maps to thinking_budget
client = callable.NewClient(
	callable.NewOpenAIProvider(key, callable.QwenURL),
	callable.WithModel("qwen3-max"),
)

// OpenAI Responses: reasoning.effort + summary:"auto"
client = callable.NewClient(
	callable.NewOpenAIResponsesProvider(key, callable.OpenAIURL),
	callable.WithModel("gpt-5"),
)
```

## Quick-reference caveats

- To **disable** thinking on DeepSeek, pass `callable.Thinking{}` explicitly; omitting `WithThinking` still leaves it on.
- For **GLM-5.3**, don't pass `Thinking{}` to disable thinking (`disabled` gets a 400); use `EffortLow` instead.
- `EffortMedium` is sent as `high` on GLM, and folded into `high` server-side on DeepSeek — it's the safe value every endpoint accepts.
- `BudgetTokens` only affects Anthropic / Qwen; when it's the only thing set, other providers treat it as `EffortMedium`.
- Don't rely on `WithTemperature` while thinking: the temperature field is not sent in thinking mode.
- For self-hosted gateways that detection can't recognize, set the dialect with `WithCompat` so no non-standard fields leak to endpoints that don't know them.
