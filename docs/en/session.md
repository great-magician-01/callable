# Sessions

[中文](../zh/session.md) | **English**

A `Session` maintains conversation history across multiple calls: every Ask automatically sends the previous messages (including thinking blocks and tool-call trajectories) to the model, and appends the newly produced messages to the history when done. It is a thin wrapper over the [Agent loop](agent.md) — all agent capabilities (tools, thinking mode, skills, images, streaming events) work unchanged; the Session just manages the context for you.

Sessions are provider-agnostic: the same history can be sent to OpenAI Chat Completions, OpenAI Responses, or Anthropic. Whatever each provider needs echoed back (Anthropic's thinking signature, the Responses reasoning item, DeepSeek/GLM `reasoning_content`) is preserved verbatim and converted at send time.

## API overview

```go
sess := agent.Session()

result, err := sess.Ask(ctx, callable.User("Hello"))
result, err := sess.AskStream(ctx, onEvent, callable.User("Go on"))

history := sess.History()       // a copy of []callable.Message
sess.SetHistory(restored)       // replace history wholesale (e.g. restore persisted state)
sess.Reset()                    // clear history
```

| Method | Signature | Description |
|---|---|---|
| `Agent.Session` | `func (a *Agent) Session(opts ...SessionOption) *Session` | Create a new, empty session on this agent. One agent can host multiple independent sessions; `opts` are covered in "Context window and compaction" below |
| `Session.Ask` | `func (s *Session) Ask(ctx context.Context, messages ...Message) (*AgentResult, error)` | Append messages to the history and run the agent loop (non-streaming). Equivalent to `agent.Run`, with the history prepended automatically |
| `Session.AskStream` | `func (s *Session) AskStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)` | Same as Ask, forwarding all streaming events to `onEvent` (see [Streaming Events](streaming.md)) |
| `Session.History` | `func (s *Session) History() []Message` | Return a **copy** of the conversation history (system prompt excluded); mutating the returned slice has no effect on the session |
| `Session.SetHistory` | `func (s *Session) SetHistory(messages []Message)` | Replace the history wholesale (the argument is copied), e.g. to restore a persisted conversation or seed context manually |
| `Session.Reset` | `func (s *Session) Reset()` | Clear the history and the tracked context usage, returning the session to its freshly-created state |
| `Session.Compact` | `func (s *Session) Compact(ctx context.Context) (string, error)` | Compact the history manually: summarize it with the model and replace it wholesale; returns the summary. No-op on empty history; the history is untouched on error |
| `Session.ContextWindow` | `func (s *Session) ContextWindow() int` | The configured context window size in tokens (default 1,000,000) |
| `Session.ContextUsage` | `func (s *Session) ContextUsage() Usage` | The token usage of the most recent Ask's final turn (`ContextTokens` is the context occupancy at that point) |
| `Session.ContextFillRatio` | `func (s *Session) ContextFillRatio() float64` | Context fill ratio: `ContextTokens / ContextWindow` |

The returned `*AgentResult` is identical to `agent.Run`'s: `FinalText` (the final answer), `Messages` (the full trajectory of this run: input messages plus all assistant/tool messages), `Usage` (accumulated across turns), `LastTurnUsage` (the final turn's usage — its `ContextTokens` is the current context occupancy), `Turns`, and `StopReason` (`AgentCompleted` / `AgentMaxTurns`).

## Basic usage

```go
client := callable.NewClient(
    callable.NewAnthropicProvider(apiKey, callable.AnthropicURL),
    callable.WithModel("claude-sonnet-5"),
)
agent := callable.NewAgent(client,
    callable.WithSystemPrompt("You are a pragmatic assistant."),
    callable.WithTools(weather),
    callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
)

session := agent.Session()
questions := []string{
    "How many three-digit numbers have distinct digits summing to 12?",
    "Summarize your reasoning in two sentences.", // the model sees the full previous turn
}

for _, q := range questions {
    _, err := session.AskStream(ctx, func(ev callable.Event) {
        switch e := ev.(type) {
        case callable.ThinkingDeltaEvent:
            fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // thinking deltas
        case callable.TextDeltaEvent:
            fmt.Print(e.Delta) // answer deltas
        }
    }, callable.User(q))
    if err != nil {
        log.Fatal(err)
    }
}

fmt.Println("history length:", len(session.History()))
```

(A full runnable version lives in `examples/thinking/main.go`.)

## How it works

- On every Ask, the Session concatenates "existing history + this call's input messages" and feeds it to the agent loop. The system prompt is injected by the agent on each request and is **not** part of the history.
- When the run **succeeds** (`err == nil`), `result.Messages` becomes the new history — i.e. old history + this input + every assistant message produced this run (thinking and tool calls included) + every tool-result message. All of it is echoed back on the next Ask.
- A single Ask may span several loop turns (model calls a tool → result goes back → model is asked again); those intermediate messages are preserved in the history as well.

## Persisting history: serialize to JSON and restore

`Message` and all its Part types implement JSON serialization (provider round-trip data included — see the next section), so the result of `History()` can be `json.Marshal`ed directly and restored later with `SetHistory`; the conversation continues seamlessly:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "os"

    callable "github.com/great-magician-01/callable"
)

const historyFile = "session.json"

// saveHistory writes the session history to a file.
func saveHistory(sess *callable.Session) error {
    data, err := json.MarshalIndent(sess.History(), "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(historyFile, data, 0o644)
}

// loadHistory reads a persisted history; a missing file means a fresh session.
func loadHistory() ([]callable.Message, error) {
    data, err := os.ReadFile(historyFile)
    if os.IsNotExist(err) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    var history []callable.Message
    if err := json.Unmarshal(data, &history); err != nil {
        return nil, err
    }
    return history, nil
}

func main() {
    ctx := context.Background()

    client := callable.NewClient(
        callable.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), callable.AnthropicURL),
        callable.WithModel("claude-sonnet-5"),
    )
    agent := callable.NewAgent(client,
        callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
    )
    session := agent.Session()

    // Restore the previous conversation, if any.
    history, err := loadHistory()
    if err != nil {
        log.Fatal(err)
    }
    session.SetHistory(history)

    result, err := session.Ask(ctx, callable.User("Where did we leave off last time?"))
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.FinalText)

    // Persist the updated history for the next run.
    if err := saveHistory(session); err != nil {
        log.Fatal(err)
    }
}
```

A serialized message looks like this:

```json
{
  "role": "assistant",
  "parts": [
    {"type": "thinking", "text": "...", "signature": "EgkBCk…"},
    {"type": "tool_call", "id": "toolu_01…", "name": "get_weather", "arguments": "{\"city\":\"Beijing\"}"},
    {"type": "text", "text": "It is 26°C and sunny in Beijing."}
  ],
  "provider_extra": { "openai-responses": [ /* raw reasoning item */ ] }
}
```

The `type` field discriminates Part kinds (`text` / `image` / `thinking` / `tool_call` / `tool_result`); unmarshaling restores the concrete types automatically. See the [Message Model](messages.md) for details.

## Thinking blocks and tool trajectories are preserved

History fidelity is the whole point of Session; dropping any piece either makes the provider reject the request or makes the model "forget":

- **ThinkingPart is kept verbatim**: the reasoning text travels with Anthropic's `signature` (a cryptographic signature — omitting it causes a 400) and the OpenAI Responses reasoning item id.
- **Raw provider payloads ride along**: data that cannot fit the unified model (e.g. the complete Responses reasoning item) is attached to the message via `Message.SetProviderExtra`, keyed by provider name, serialized into the `provider_extra` field, and survives cross-process persistence.
- **Tool trajectories stay paired**: every `tool_call` in an assistant message has a matching `tool_result` (by ID) in a `tool` message. On cancellation, unexecuted tool calls get a synthesized `IsError` result, so even a partial trajectory remains a valid, replayable conversation.
- **Compatible endpoints included**: DeepSeek/GLM `reasoning_content` and Qwen thinking content are likewise stored in ThinkingPart and echoed back.

Therefore: do not trim or rewrite assistant/tool messages in the history yourself. If you construct history manually via `SetHistory`, make sure every tool call has a paired tool result, or the provider APIs will reject the request.

## Canceled or failed runs never pollute the history

A Session only updates its history when a run **fully succeeds** (`err == nil`). In all of the following cases the history stays untouched, so you can safely retry or simply move on to a different question:

- `ctx` cancellation or timeout (the error matches `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded`)
- Network errors and `APIError` (including after retries are exhausted)
- `MaxTurnsError` (the loop hit the turn limit — note that although it carries a `Partial` result, it is still an error, so the history is not updated)

The rationale: an aborted run may end on an assistant message that issued a tool call whose result was never delivered; writing that into the history would make the provider reject the next request. Partial output from an aborted run is still available through the returned (non-nil) `*AgentResult` — it just never enters the history:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

before := len(sess.History())
result, err := sess.Ask(ctx, callable.User("Write a long essay"))
if errors.Is(err, context.DeadlineExceeded) {
    // result is non-nil: result.FinalText holds the partial text,
    // but the history is unchanged, so this holds:
    fmt.Println(len(sess.History()) == before) // true
    // and you can simply retry:
    result, err = sess.Ask(context.Background(), callable.User("Write a long essay"))
}
```

## Context window and history compaction

History only grows, and long conversations eventually approach the model's context limit. A Session can track context occupancy, compact its history automatically once a threshold is reached, and supports manual compaction at any time.

### Tracking context usage

`agent.Session()` accepts `SessionOption`s:

| Option | Description |
|---|---|
| `WithContextWindow(tokens int)` | Context window size in tokens. Default `DefaultContextWindow` = 1,000,000; non-positive values are ignored |
| `WithAutoCompact(enabled bool)` | Enable automatic compaction. Default off |
| `WithAutoCompactThreshold(ratio float64)` | Context fill ratio that triggers auto-compaction, in (0, 1]. Default `DefaultAutoCompactThreshold` = 0.6; out-of-range values are ignored |

After every successful Ask, the session records the usage of the **final** model turn:

```go
sess := agent.Session(callable.WithContextWindow(200_000))
result, err := sess.Ask(ctx, callable.User("..."))

sess.ContextUsage()      // the final turn's Usage
sess.ContextFillRatio()  // ContextTokens / ContextWindow, e.g. 0.45
result.LastTurnUsage     // the same value, also available on the result
```

The key figure is `Usage.ContextTokens`: the total number of tokens the request occupied in the context window, normalized across providers — OpenAI-style APIs report `prompt_tokens` (which already include cached tokens), Anthropic reports `input_tokens + cache_read + cache_creation`. Unlike the per-run accumulated `Usage`, it answers "how full is the context right now". It is 0 when the provider does not report usage.

### Automatic compaction

When enabled, the session compacts itself at the end of any Ask whose `ContextFillRatio()` reaches the threshold:

```go
sess := agent.Session(
    callable.WithContextWindow(200_000),
    callable.WithAutoCompact(true),
    callable.WithAutoCompactThreshold(0.7), // optional, default 0.6
)
```

- Auto-compaction is **best-effort**: a failed compaction call does not fail the Ask, and the history stays as it was.
- After a successful compaction `ContextUsage()` is reset to zero and re-measured by the next Ask.
- `AskStream` emits a `SessionCompactEvent{Summary, TokensBefore}` when a compaction happens (see [Streaming Events](streaming.md)).
- **Sub-agents are not affected**: delegated sub-agents run in their own agent loop without a session, so this configuration never applies to them.

### Manual compaction

Call `Compact` at any time to compact regardless of the threshold:

```go
summary, err := sess.Compact(ctx) // no-op on empty history; history untouched on error
```

### What compaction does

Compaction renders the current history as a plain-text transcript (thinking blocks and images become placeholders), asks the model for a summary using the agent's client (same model, without tools or thinking config), then replaces the whole history with a single user message:

```
[Conversation compacted] Summary of the earlier conversation:

<summary>
```

Compaction is therefore an irreversible rewrite of the history: thinking signatures, provider round-trip payloads and raw tool trajectories are all replaced by the summary. Back up via `History()` first if you need an audit trail or a way back.

## Caveats

- **Concurrency**: a `Session` is not safe for concurrent use — do not Ask the same session from multiple goroutines; give each goroutine its own session instead.
- **Empty input**: `Ask(ctx)` with no messages fails with `agent run requires at least one input message` when the history is empty; with a non-empty history it effectively asks the model to continue from the existing context.
- **History is a copy**: `History()` returns a copy. To append messages, use `Ask` or `SetHistory`; mutating the returned slice does nothing.
- **Many sessions per agent**: `agent.Session()` can be called repeatedly and the sessions' histories are independent, but they all share the agent's configuration (tools, system prompt, thinking mode).
- **History only grows**: except via `Reset` / `SetHistory` / `Compact`, history accumulates. Long conversations cost tokens — enable `WithAutoCompact` for automatic compaction, or truncate manually with `SetHistory` (keeping tool-call pairing intact, as above).
- **System prompt is not part of the history**: `History()` / `SetHistory()` only manage user / assistant / tool messages. The system prompt comes from the agent configuration (`WithSystemPrompt`, skill indexes, etc.) and is injected per request — it is not persisted.

## See also

- [Agent Loop](agent.md) — the loop machinery behind Ask
- [Message Model](messages.md) — Message/Part structure and serialization format
- [Thinking Mode](thinking.md) — how thinking blocks map to each provider
- [Streaming Events](streaming.md) — event types delivered by AskStream
- [Error Handling](errors.md) — APIError, MaxTurnsError, and cancellation semantics
