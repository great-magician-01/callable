# Agent Loop

[中文](../zh/agent.md) | **English**

`Agent` is callable's core abstraction: on top of a `Client` it runs the full tool-calling loop automatically — send messages to the model, execute any requested tools, feed the results back, and repeat until the model produces a final answer with no tool calls (or the turn limit is hit). Register your tools, call `Run` once, and you never write the loop yourself.

## How the loop works internally

Each turn proceeds as follows:

```
Run(input):
  msgs = [System(base prompt + skill index + sub-agent index)] + input
  for turn in 1..MaxTurns:
      resp = client.Stream(msgs, tools, thinking)     # Run uses Create instead
        ↳ emits: TurnStart / MessageStart / ThinkingDelta /
                 TextDelta / ToolCallDelta / MessageDone
      msgs += resp.Message      # thinking / tool_call parts + provider extras
      if resp has no tool calls:
          return AgentResult{FinalText, Messages, Usage(accumulated), Turns, AgentCompleted}
      for call in resp.ToolCalls:                     # sequential by default
          decision = ToolCallHook(call)?              # optional approval hook
          result = tool.Execute(call.Args)
          ↳ emits: ToolCallEvent / ToolResultEvent
          msgs += tool result message
  return MaxTurnsError{Turns, Partial}                # carries the partial result
```

Key behaviors:

- **Tool execution errors do not abort the loop**: when a tool handler returns an error, an `IsError=true` tool result is fed back to the model (which may retry or take another route). Only API/network errors, an error returned by the approval hook itself, or context cancellation abort `Run`.
- **Unknown tools** (model hallucinating a tool name) do not abort either: an `unknown tool "..."` error result is fed back.
- **Thinking blocks and tool trajectories are fully preserved** in history and replayed per provider requirements (Anthropic signatures, Responses reasoning items, `reasoning_content` on compatible endpoints) — see [Thinking Mode](thinking.md).
- For the event stream see [Streaming Events](streaming.md).

## NewAgent

```go
func NewAgent(client *Client, opts ...AgentOption) *Agent
```

The `client` supplies the provider, model and default parameters (see [Getting Started](getting-started.md)); all agent behavior is configured via `AgentOption`s.

## AgentOption reference

| Option | Signature | Description |
|---|---|---|
| `WithSystemPrompt` | `WithSystemPrompt(prompt string)` | Sets the agent's base system prompt. Skill and sub-agent indexes are appended after it automatically |
| `WithTools` | `WithTools(tools ...Tool)` | Registers tools the model may call; may be passed multiple times (appends). See [Tools](tools.md) |
| `WithSkills` | `WithSkills(skills ...Skill)` | Registers skills (progressive disclosure: only the index goes into the prompt; the model loads full instructions via the built-in `read_skill` tool). See [Skills](skills.md) |
| `WithSubAgents` | `WithSubAgents(subs ...SubAgent)` | Registers sub-agent definitions. Not exposed as tools by default: the model first loads one via the built-in `load_agent` tool, which dynamically registers a `call_<name>` tool for delegation. See [Sub-Agents](subagents.md) |
| `WithThinking` | `WithThinking(t Thinking)` | Enables thinking mode, e.g. `Thinking{Effort: EffortHigh}`. See [Thinking Mode](thinking.md) |
| `WithMaxTurns` | `WithMaxTurns(n int)` | Caps the number of model calls per run. **Default 25**; `n <= 0` is ignored (keeps the default) |
| `WithToolCallHook` | `WithToolCallHook(h ToolCallHook)` | Pre-execution approval hook: approve / deny / rewrite arguments. See below |
| `WithParallelToolExecution` | `WithParallelToolExecution(enabled bool)` | Allows concurrent execution of multiple tool calls within one turn. Default `false` (sequential) |

Skill- and sub-agent-specific options are also `AgentOption`s; details live in their own docs:

- Skills: [Skills](skills.md) — `WithSkillReadHook` (rewrite content before it reaches the model), `WithSkillToolName` (rename the built-in tool, default `read_skill`), `WithSkillToolDisabled` (remove it and register your own replacement)
- Sub-agents: [Sub-Agents](subagents.md) — `WithSubAgentToolName` (default `load_agent`), `WithSubAgentToolDisabled`, `WithSubAgentEvents` (forward sub-agent events)

## Run vs RunStream

```go
func (a *Agent) Run(ctx context.Context, messages ...Message) (*AgentResult, error)
func (a *Agent) RunStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)
```

Both are the same implementation: `Run` is equivalent to `RunStream(ctx, nil, ...)`.

- **`Run`**: non-streaming. Uses one-shot requests (`Client.Create`) internally, blocks until the loop finishes, and returns only the final `AgentResult`. Best for background jobs, scripts, and tests.
- **`RunStream`**: streaming. Every event (turn start/end, thinking deltas, text deltas, tool calls, tool results) is pushed to `onEvent` as it happens; passing `nil` behaves like `Run`. Best for CLIs and servers streaming to clients. For the event catalog see [Streaming Events](streaming.md).

Notes:

- At least one input message is required, otherwise an error is returned immediately (`agent run requires at least one input message`).
- `messages` may contain multiple entries (e.g. restored history plus a new question); the system prompt is assembled by the agent and prepended automatically — do not pass it yourself.
- To maintain history across calls, use a [Session](session.md) (`agent.Session()`) instead of stitching history into every `Run` manually.
- Both entry points honor `ctx`: cancellation/timeout stops gracefully — the in-flight upstream request is aborted, no new turn is started, unexecuted tool calls get synthesized `IsError` results so every call stays paired, and a **non-nil partial result** is returned (same usage pattern as `MaxTurnsError` below).

## AgentResult and stop reasons

```go
type AgentResult struct {
    FinalText  string    // the model's final answer; empty or partial if the run did not complete
    Messages   []Message // full trajectory: input messages + every assistant message and tool result
    Usage      Usage     // token usage accumulated across all turns
    Turns      int       // number of model calls actually performed
    StopReason string    // AgentCompleted or AgentMaxTurns
}
```

Stop reason constants:

| Constant | Value | Meaning |
|---|---|---|
| `AgentCompleted` | `"completed"` | The model produced a final answer with no tool calls; the loop finished normally |
| `AgentMaxTurns` | `"max_turns"` | The turn limit was hit without a final answer (a `*MaxTurnsError` is returned) |

`Usage` fields: `InputTokens / OutputTokens / ReasoningTokens / CacheReadTokens / CacheWriteTokens`, summed over all turns.

`Messages` excludes the system prompt and contains only the input plus everything the loop produced — it can be handed to `Session.SetHistory` or persisted as JSON. See [Message Model](messages.md).

## Approval hook: ToolCallHook

```go
type ToolCallHook func(ctx context.Context, call ToolCall) (ToolDecision, error)

callable.Approve()                 // execute as requested
callable.Deny("reason")            // block; the reason goes back to the model as an IsError result
callable.ReplaceArgs(`{"k":"v"}`)  // approve with rewritten JSON arguments
```

- The hook runs **before every tool execution** and receives the full `ToolCall{Name, Arguments}` (`Arguments` is the raw JSON string).
- `Deny` does not abort the loop: the model receives `"Tool call denied: <reason>"` and typically adjusts its plan.
- `ReplaceArgs` can force parameters into safe bounds (e.g. rewrite a dangerous path to a sandbox path).
- If the hook itself returns an error, the **entire run aborts** — a safe escape hatch when your approval system fails.
- Event ordering: an approved (or rewritten) call emits `ToolCallEvent` before executing; a denied call emits only `ToolResultEvent`.

### Example: human confirmation for dangerous operations

Approve read-only tools directly; require a human to confirm writes at the terminal:

```go
agent := callable.NewAgent(client,
    callable.WithTools(readFile, writeFile, deleteFile),
    callable.WithToolCallHook(func(ctx context.Context, call callable.ToolCall) (callable.ToolDecision, error) {
        switch call.Name {
        case "read_file":
            return callable.Approve(), nil // read-only: no confirmation needed
        }

        // Dangerous operation: show the call and wait for human confirmation.
        fmt.Printf("\n⚠️  The model requests %s(%s)\nAllow? [y/n] ", call.Name, call.Arguments)
        var answer string
        if _, err := fmt.Scanln(&answer); err != nil {
            return callable.ToolDecision{}, err // input failure: abort the run
        }
        if answer != "y" && answer != "Y" {
            return callable.Deny("the user rejected this operation"), nil
        }
        return callable.Approve(), nil
    }),
)
```

## Parallel tool execution

A model may request several tool calls in one turn. By default they run **sequentially** (deterministic, easier to audit); enabling parallel execution runs them concurrently:

```go
callable.WithParallelToolExecution(true)
```

Behavior details:

- Results keep the order in which the model issued the calls (regardless of execution speed), so the messages fed back are unaffected.
- The approval hook runs inside the concurrent goroutines too: if it involves interactive confirmation or shared state, serialize it yourself (e.g. with a mutex).
- In sequential mode, a hook error on an earlier call prevents later calls from running; in parallel mode all calls start, and the first error is reported after all of them finish.
- Only enable this when the tools are free of side-effect dependencies (e.g. several independent read-only queries).

## MaxTurnsError and partial results

When the `WithMaxTurns` limit is reached, `Run`/`RunStream` return a `*MaxTurnsError` whose `Partial` field carries everything that happened up to that point:

```go
result, err := agent.Run(ctx, callable.User("..."))

var mte *callable.MaxTurnsError
if errors.As(err, &mte) {
    // mte.Turns is the configured limit; mte.Partial is the same object as result
    fmt.Println("limit reached, turns so far:", mte.Partial.Turns)
    fmt.Println("tokens consumed:", mte.Partial.Usage.InputTokens, "/", mte.Partial.Usage.OutputTokens)
    // mte.Partial.Messages is still a valid, replayable trajectory
}
```

Key points:

- `Partial` is non-nil, `Partial.StopReason == AgentMaxTurns`, and `Partial.FinalText` is empty (there is no final answer).
- In the partial `Messages`, every tool call is paired with a tool result, so the trajectory can be continued directly.
- Context cancellation/timeout behaves similarly: the returned `result` is non-nil, `FinalText` holds the partially generated text, and the error satisfies `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded`.
- See [Error Handling](errors.md) for the other error types.

## Configuration hierarchy

The same parameter can be set at three layers, with precedence **Request > Agent > Client**:

| Layer | Set via | Scope |
|---|---|---|
| Client | `WithModel` / `WithMaxTokens` / `WithTemperature` | Every request the client sends |
| Agent | `WithThinking` (plus tool list and system prompt) | Every run, every turn of the agent |
| Request | `NewRequest(...).WithModel(...).WithThinking(...).WithMaxTokens(...)...` | A single request |

Each loop turn builds a `Request` internally, applies the agent-level configuration (thinking, tools), and hands it to the client, which fills in model defaults; anything still unset is up to the provider. When calling `client.Create/Stream` directly, you can override agent and client settings on an individual `Request`.

## Complete example

An agent with a weather tool, an approval log, and streaming output (mirrors `examples/tools/main.go`):

```go
package main

import (
    "context"
    "fmt"
    "os"

    callable "github.com/great-magician-01/callable"
)

// WeatherArgs is reflected into the tool's JSON Schema.
type WeatherArgs struct {
    City string `json:"city" jsonschema:"description=City name, e.g. Beijing"`
    Unit string `json:"unit,omitempty" jsonschema:"description=Temperature unit,enum=celsius,enum=fahrenheit"`
}

func main() {
    ctx := context.Background()
    client := callable.NewClient(
        callable.NewOpenAIProvider(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL),
        callable.WithModel("gpt-5"),
    )

    weather := callable.NewTool("get_weather", "Get the current weather of a city",
        func(ctx context.Context, args WeatherArgs) (any, error) {
            // Replace with a real API call in production.
            return fmt.Sprintf(`{"city":%q,"temp":26,"unit":"°C","condition":"sunny"}`, args.City), nil
        })

    agent := callable.NewAgent(client,
        callable.WithSystemPrompt("You are a pragmatic weather assistant."),
        callable.WithTools(weather),
        callable.WithMaxTurns(10),
        // Approval hook: log every call before it runs.
        callable.WithToolCallHook(func(ctx context.Context, call callable.ToolCall) (callable.ToolDecision, error) {
            fmt.Printf("\n[tool call] %s(%s)\n", call.Name, call.Arguments)
            return callable.Approve(), nil
        }),
    )

    result, err := agent.RunStream(ctx, func(ev callable.Event) {
        switch e := ev.(type) {
        case callable.ThinkingDeltaEvent:
            fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // dim thinking output
        case callable.TextDeltaEvent:
            fmt.Print(e.Delta)
        case callable.ToolResultEvent:
            fmt.Fprintf(os.Stderr, "\n[tool result] %s -> %s\n", e.Call.Name, e.Result.Content)
        }
    }, callable.User("What is the temperature in Beijing and Shanghai right now? In Celsius."))
    if err != nil {
        fmt.Fprintln(os.Stderr, "error:", err)
        os.Exit(1)
    }

    fmt.Fprintf(os.Stderr, "\n[turns: %d, stop: %s, tokens: in %d / out %d]\n",
        result.Turns, result.StopReason, result.Usage.InputTokens, result.Usage.OutputTokens)
}
```

## Gotchas

- `WithMaxTurns` defaults to 25, which is generous, but for tasks that can fall into tool loops (the model calling the same tool repeatedly), set it explicitly and handle `MaxTurnsError`.
- `Agent` itself is stateless and safe to reuse concurrently; cross-call history is managed by a [Session](session.md).
- Running tool functions receive the same `ctx`; tool implementations should respond to cancellation themselves — Go cannot forcibly kill a goroutine that ignores `ctx`.
- The system prompt is assembled by the agent (base prompt + skill index + sub-agent index); do not include `System(...)` in the messages you pass to `Run`.
