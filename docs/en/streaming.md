# Streaming Events

[中文](../zh/streaming.md) | **English**

All streaming in callable is built around one unified event model: whether the wire format is OpenAI Chat Completions, OpenAI Responses or Anthropic Messages, the event types, fields and ordering are identical. Events live on two levels:

- **provider level**: events inside a single model call (message start/done, text deltas, tool-call deltas), produced by `client.Stream`
- **agent level**: per-turn events of the agent loop (turn start/end, tool execution, loop completion), produced by `agent.RunStream` in addition to the provider-level events

Both entry points share the same callback signature `func(callable.Event)`, so a single type switch handles both levels.

## API signatures

```go
// Single streaming model call: provider-level events only, returns the
// fully assembled response.
func (c *Client) Stream(ctx context.Context, req *Request, onEvent func(Event)) (*Response, error)

// Streaming agent loop: provider-level + agent-level events, returns the
// accumulated result.
func (a *Agent) RunStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)

// Streaming session call: RunStream with conversation history maintained.
func (s *Session) AskStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)
```

- Passing `nil` for `onEvent` degrades to non-streaming (`agent.Run` is literally `RunStream(ctx, nil, ...)`); the agent then issues non-streaming requests and no events are produced.
- The callback is **synchronous**: events fire one by one on the caller's goroutine, and the stream pauses until your callback returns. Do not block inside it (slow IO etc.) — forward to a channel if you need async processing.
- Exception: with `WithParallelToolExecution(true)`, the `ToolCallEvent` / `ToolResultEvent` of multiple tools in one turn fire **from concurrent goroutines** — the callback must be concurrency-safe in that case (mutex or channel).
- The callback returns no error. To abort a stream midway, cancel the `context.Context` you passed in (see [Error Handling](errors.md)).

## Complete event reference

`Event` is a sealed interface; the full set of implementations is listed below. Events are delivered by value (not pointer).

### Provider-level events (produced by both `client.Stream` and `agent.RunStream`)

| Event | Payload fields | When it fires |
|---|---|---|
| `MessageStartEvent` | (none) | An assistant message starts generating; the first event of every model call |
| `ThinkingDeltaEvent` | `Delta string` | An increment of reasoning text; only when thinking mode is enabled and the model actually reasons, see [Thinking Mode](thinking.md) |
| `TextDeltaEvent` | `Delta string` | An increment of answer text, token by token (or chunk by chunk) |
| `ToolCallDeltaEvent` | `Index int` `ID string` `Name string` `ArgsDelta string` | An increment of a streamed tool call. `ID`/`Name` are set only on the first increment of a given call; `ArgsDelta` carries argument JSON fragments that concatenate into the full arguments. `Index` identifies the call within the turn (0-based), distinguishing multiple tool calls requested in one turn |
| `MessageDoneEvent` | `Message` `Usage` `StopReason` | The model call finished; carries the fully assembled message (including `ThinkingPart` / `ToolCallPart`), this turn's token usage and the stop reason |

`MessageDoneEvent.StopReason` is one of `StopReasonEndTurn` / `StopReasonToolCalls` / `StopReasonMaxTokens` / `StopReasonOther`. `StopReasonToolCalls` means the model requested tools — inside the agent loop this is followed by `ToolCallEvent` / `ToolResultEvent` and another turn.

### Agent-level events (produced only by `agent.RunStream` / `Session.AskStream`)

| Event | Payload fields | When it fires |
|---|---|---|
| `TurnStartEvent` | `Turn int` | Turn `Turn` begins (1-based). A turn is one model call plus every tool execution it requested |
| `TurnEndEvent` | `Turn int` | Turn `Turn` ends (after all its tools finished, or after the model produced the final answer) |
| `ToolCallEvent` | `Call ToolCall` | A tool is about to execute: fires **after the ToolCallHook approved it, before execution**. `Call` is the complete call (`ID` / `Name` / `Arguments` raw JSON). Calls denied by the hook do not fire this event |
| `ToolResultEvent` | `Call ToolCall` `Result ToolResult` | A tool finished executing. `Result.Content` is what goes back to the model; `Result.IsError` marks failure (errors are fed back to the model as tool results, the loop continues). Also fires for hook-denied or cancellation-skipped calls (`IsError=true`) |
| `AgentDoneEvent` | `Result *AgentResult` | The agent loop finished normally (model produced a final answer); `Result` is the same object as the return value. **Not fired on max turns, errors or cancellation** — check the return value / error for those |
| `SubAgentEvent` | `SubAgent string` `Event Event` | Wraps an event from inside a delegated sub-agent's loop; `SubAgent` is the sub-agent's name. Only produced when the parent agent enables `WithSubAgentEvents(true)`, see [Sub-Agents](subagents.md) |

## Typical event sequences

### Single turn, plain text (thinking enabled)

The provider-level portion is identical for `client.Stream` and a one-turn `agent.RunStream`:

```
[agent]    TurnStartEvent{Turn: 1}
[provider] MessageStartEvent{}
[provider] ThinkingDeltaEvent{Delta: "the user asks"} × N
[provider] TextDeltaEvent{Delta: "Beijing"} × N
[provider] MessageDoneEvent{Message: ..., Usage: {Input: 12, Output: 30}, StopReason: end_turn}
[agent]    TurnEndEvent{Turn: 1}
[agent]    AgentDoneEvent{Result: ...}
```

### Multi-turn with tool calls

The model requests a tool in turn 1 and gives the final answer in turn 2:

```
TurnStartEvent{Turn: 1}
MessageStartEvent{}
ThinkingDeltaEvent × N                     // optional
TextDeltaEvent × N                         // optional: lead-in text before the tool call
ToolCallDeltaEvent{Index: 0, ID: "call_1", Name: "get_weather", ArgsDelta: `{"city":`}
ToolCallDeltaEvent{Index: 0, ArgsDelta: `"Beijing"}`}
MessageDoneEvent{..., StopReason: tool_calls}
ToolCallEvent{Call: {ID: "call_1", Name: "get_weather", Arguments: `{"city":"Beijing"}`}}
ToolResultEvent{Call: ..., Result: {Content: "Beijing: 26°C, sunny"}}
TurnEndEvent{Turn: 1}
TurnStartEvent{Turn: 2}
MessageStartEvent{}
TextDeltaEvent × N                         // final answer
MessageDoneEvent{..., StopReason: end_turn}
TurnEndEvent{Turn: 2}
AgentDoneEvent{Result: ...}
```

Note that `ToolCallDeltaEvent` carries `ID` and `Name` only on the first increment; the concatenated argument deltas equal the later `ToolCallEvent.Call.Arguments`. In most cases you can ignore `ToolCallDeltaEvent` entirely and just consume the complete call from `ToolCallEvent`.

### Sub-agent events

With `WithSubAgentEvents(true)` enabled, every event inside a delegated sub-agent's run is wrapped and forwarded:

```
ToolCallEvent{Call: {Name: "call_translator", ...}}
SubAgentEvent{SubAgent: "translator", Event: TurnStartEvent{Turn: 1}}
SubAgentEvent{SubAgent: "translator", Event: MessageStartEvent{}}
SubAgentEvent{SubAgent: "translator", Event: TextDeltaEvent{Delta: "..."}}
...
SubAgentEvent{SubAgent: "translator", Event: AgentDoneEvent{...}}
ToolResultEvent{Call: ..., Result: ...}
```

A nested type switch lets the consumer tell main-agent and sub-agent output apart — e.g. prefix sub-agent text or route it to a separate UI pane.

## Handling events with a type switch

Events arrive by value, so case on value types. A handler covering every event type:

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/great-magician-01/callable"
)

func main() {
	client := callable.NewClient(
		callable.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), callable.AnthropicURL),
		callable.WithModel("claude-sonnet-5"),
	)

	type WeatherArgs struct {
		City string `json:"city" jsonschema:"description=City name, e.g. Beijing"`
	}
	weather := callable.NewTool("get_weather", "Get the current weather for a city",
		func(ctx context.Context, args WeatherArgs) (any, error) {
			return fmt.Sprintf("%s: 26°C, sunny", args.City), nil
		})

	agent := callable.NewAgent(client,
		callable.WithTools(weather),
		callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
	)

	onEvent := func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.TurnStartEvent:
			fmt.Printf("\n── turn %d ──\n", e.Turn)
		case callable.MessageStartEvent:
			// an assistant message begins; usually nothing to do
		case callable.ThinkingDeltaEvent:
			fmt.Printf("\x1b[90m%s\x1b[0m", e.Delta) // reasoning: render dimmed
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta) // answer increment, print as-is
		case callable.ToolCallDeltaEvent:
			// streamed argument fragments; ignore if you only need the
			// complete call — wait for ToolCallEvent instead
		case callable.MessageDoneEvent:
			fmt.Printf("\n[turn usage: in %d / out %d / reasoning %d]\n",
				e.Usage.InputTokens, e.Usage.OutputTokens, e.Usage.ReasoningTokens)
		case callable.ToolCallEvent:
			fmt.Printf("\n>>> calling %s(%s)\n", e.Call.Name, e.Call.Arguments)
		case callable.ToolResultEvent:
			if e.Result.IsError {
				fmt.Printf("<<< tool failed: %s\n", e.Result.Content)
			} else {
				fmt.Printf("<<< tool returned: %s\n", e.Result.Content)
			}
		case callable.TurnEndEvent:
			// turn finished
		case callable.SubAgentEvent:
			fmt.Printf("[sub-agent %s] ", e.SubAgent) // dispatch on e.Event for details
		case callable.AgentDoneEvent:
			fmt.Printf("\n[done: %d turns, %d input tokens total]\n",
				e.Result.Turns, e.Result.Usage.InputTokens)
		}
	}

	result, err := agent.RunStream(context.Background(), onEvent, callable.User("How's the weather in Beijing?"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	_ = result // same object as AgentDoneEvent.Result
}
```

The low-level path (no agent loop) only needs provider-level events:

```go
resp, err := client.Stream(ctx, callable.NewRequest(callable.User("Tell me a story")),
	func(ev callable.Event) {
		if d, ok := ev.(callable.TextDeltaEvent); ok {
			fmt.Print(d.Delta)
		}
	})
// resp is the assembled response: resp.Text / resp.Message / resp.Usage / resp.StopReason
```

How the returned `Response` relates to the stream: concatenating every `TextDeltaEvent.Delta` yields `resp.Text`, and `MessageDoneEvent.Message` equals `resp.Message`. So you can render incrementally and still get the complete structured result at the end.

## Usage fields

```go
type Usage struct {
	InputTokens      int // input (prompt) tokens
	OutputTokens     int // output tokens
	ReasoningTokens  int // of which were reasoning tokens (reported by OpenAI; typically 0 for Anthropic)
	CacheReadTokens  int // input tokens served from prompt cache (Anthropic cache_read_input_tokens)
	CacheWriteTokens int // input tokens written to prompt cache (Anthropic cache_creation_input_tokens)
}
```

- Providers report different subsets: unreported fields stay 0. Anthropic reports the cache fields; OpenAI reports `ReasoningTokens`.
- `MessageDoneEvent.Usage` and `Response.Usage` are **per-turn** figures.
- After every turn the agent loop adds that turn's `Usage` into `AgentResult.Usage` (i.e. `AgentDoneEvent.Result.Usage`), which is therefore a cross-turn cumulative total. In multi-turn tool scenarios the cumulative input tokens far exceed a single turn — the full conversation history is re-sent every turn.
- `ReasoningTokens` is usually a subset of `OutputTokens`; do not add them together as a total.

## Caveats and edge cases

- **No `MessageDoneEvent` on cancellation**: canceling mid-stream makes the provider return the partially assembled `*Response` (non-nil) plus an error matching `errors.Is(err, context.Canceled)`, but `MessageDoneEvent` / `TurnEndEvent` / `AgentDoneEvent` do not fire. At the agent level the partial text and usage land in the returned `AgentResult`; the incomplete message is **not** appended to `Messages`, keeping the trajectory replayable.
- **No `AgentDoneEvent` on errors / max turns**: hitting the turn limit returns `*MaxTurnsError` (`Partial` carries the partial result); provider failures return `*APIError`. Detect completion via the return value and error, not by waiting for `AgentDoneEvent`. See [Error Handling](errors.md).
- **Synchronous, ordered callbacks** (with default sequential tools): events fire strictly in order of occurrence. With `WithParallelToolExecution(true)`, tool events fire concurrently — the callback must be concurrency-safe, and the relative order of `ToolCallEvent`/`ToolResultEvent` within a turn is no longer deterministic (use `Call.ID` for pairing).
- **Hook-denied tools**: no `ToolCallEvent`, but a `ToolResultEvent` fires (`IsError=true`, content is the denial reason). Do not assume the two always come in pairs.
- **`ThinkingDeltaEvent` and `TextDeltaEvent` interleave in one message**: with thinking enabled, reasoning deltas precede text deltas; both end up in `MessageDoneEvent.Message.Parts` as `ThinkingPart` and `TextPart`.
- **`onEvent: nil`**: no events at all, and the agent internally switches to non-streaming requests (less parsing overhead).

## Related docs

- [Agent Loop](agent.md) — the full tool-calling loop around RunStream
- [Sessions](session.md) — Session.AskStream
- [Tools](tools.md) — ToolCall / ToolResult structures and the approval hook
- [Thinking Mode](thinking.md) — where ThinkingDeltaEvent comes from
- [Sub-Agents](subagents.md) — SubAgentEvent and event forwarding
- [Error Handling](errors.md) — cancellation, retries and partial results
