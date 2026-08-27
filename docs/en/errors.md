# Error Handling, Retries and Cancellation

[中文](../zh/errors.md) | **English**

callable splits failures into three categories, each handled differently:

- **API / transport errors**: `*callable.APIError`, subject to automatic retries (below)
- **Agent loop hitting the turn limit**: `*callable.MaxTurnsError`, carrying the partial result
- **Tool execution errors**: not treated as failures — the error is fed back to the model and the loop continues

Every call is governed by a `context.Context`, and cancellation is **graceful** (see "Cancellation and timeouts").

## APIError: structured provider errors

When a provider returns a non-2xx response, or a transport-level (network) failure occurs, the error is a `*callable.APIError`:

```go
type APIError struct {
    // Name of the provider that produced the error:
    // "openai" / "openai-responses" / "anthropic"
    Provider   string
    // HTTP status code; 0 for transport-level failures
    // (connection refused, DNS failure, ...)
    StatusCode int
    // Provider-specific error type/code, best-effort extracted
    // from the error payload (may be empty)
    Type       string
    // Human-readable error message
    Message    string
    // Raw response body, kept for diagnostics
    Body       string
}
```

Methods:

```go
func (e *APIError) Error() string      // "callable: openai API error (status 429, type \"rate_limit_error\"): ..."
func (e *APIError) IsRetryable() bool  // whether the error is transient and worth retrying
```

`IsRetryable()` uses exactly the same rules as the built-in retry policy:

- `StatusCode == 0` (transport failure) → retryable
- `StatusCode == 429` (rate limit) → retryable
- `StatusCode >= 500` (server error) → retryable
- everything else (400/401/403/404, ...) → not retryable

`Type` / `Message` extraction covers the common error payload shapes of all three providers: the OpenAI-style `{"error": {"type", "code", "message"}}`, and the top-level `{"type", "code", "message", "param"}` used by OpenAI Responses. If parsing fails, `Message` falls back to the trimmed raw body, then to `"unexpected status code %d"`, so `Message` is guaranteed non-empty. Read `Body` when you need the full payload.

Branch with `errors.As`:

```go
resp, err := client.Create(ctx, req)
if err != nil {
    var apiErr *callable.APIError
    if errors.As(err, &apiErr) {
        switch {
        case apiErr.StatusCode == 401:
            // invalid API key; retrying is pointless
        case apiErr.StatusCode == 429:
            // still rate limited after automatic retries: back off or fall back
        case apiErr.IsRetryable():
            // network failure / 5xx, retries already exhausted
        }
        log.Printf("provider=%s status=%d type=%s body=%s",
            apiErr.Provider, apiErr.StatusCode, apiErr.Type, apiErr.Body)
    }
}
```

## Automatic retries

The client layer retries transient failures automatically, with a fixed, predictable policy:

| Setting | Behavior |
|---|---|
| Trigger | network errors (connection failures, ...), HTTP 429, HTTP 5xx |
| Default count | 3 retries (up to 4 requests total) |
| Wait schedule | 3s before retry 1, 10s before retry 2, 30s before retry 3 |
| `WithRetries(n)` | sets the retry count; negative values are clamped to 0 |
| `WithRetries(0)` | disables retries; every error is returned immediately |
| Beyond the schedule | retries 4 and later each wait 30s |

```go
client := callable.NewClient(
    callable.NewOpenAIProvider(apiKey, callable.OpenAIURL,
        callable.WithRetries(5), // 5 retries, waiting 3s/10s/30s/30s/30s
    ),
    callable.WithModel("gpt-5"),
)
```

Boundary details worth knowing:

- **Backoff is context-aware**: canceling the ctx during a wait returns the ctx error immediately instead of sleeping through the delay.
- **`context.Canceled` / `context.DeadlineExceeded` are never retried**: a deliberate cancel or deadline is not a transient failure; it propagates as-is.
- **Non-retryable status codes are never retried**: 400/401/403/404 and friends surface as `*APIError` on the first attempt.
- **Exhausted retries still report the error**: after the last retry you get the final failure's `*APIError` (for 429/5xx on retried attempts, `Body` may be empty because the retry path discards the failed response body without reading it).
- Retries live at the HTTP layer, so `Create`, `Stream`, and everything built on them (`agent.Run` / `RunStream`, `Session.Ask*`) all benefit.

## MaxTurnsError: hitting the turn limit

When the [agent loop](agent.md) reaches `WithMaxTurns(n)` (default 25) without a final answer, `Run` / `RunStream` return a `*callable.MaxTurnsError`:

```go
type MaxTurnsError struct {
    Turns   int          // the configured turn limit
    Partial *AgentResult // everything produced so far; never nil
}
```

`Partial` carries the full trajectory up to the interruption (`Messages`), the text generated so far (`FinalText`), accumulated token usage (`Usage`), and the number of turns executed (`Turns`); its `StopReason` is `callable.AgentMaxTurns`. You can extract the interim conclusions, or continue from `Partial.Messages` (e.g. by handing them to a [Session](session.md)):

```go
result, err := agent.Run(ctx, callable.User("Summarize this 500-page report"))
var mtErr *callable.MaxTurnsError
if errors.As(err, &mtErr) {
    // intermediate work is not lost
    fmt.Println("ran", mtErr.Partial.Turns, "turns, current text:", mtErr.Partial.FinalText)
    fmt.Println("accumulated tokens:", mtErr.Partial.Usage)
}
```

Note what the limit means: the model kept requesting tools (or never produced a terminating answer). That usually indicates a task-decomposition problem, tool results that don't satisfy the model, or a max-turns value that is simply too small.

## Tool execution errors: fed back to the model, loop keeps running

An error returned by a [tool](tools.md) handler does **not** fail the agent run. It is wrapped in a tool result with `IsError = true` and fed back to the model, which can then retry, adjust its arguments, or take another path:

```go
search := callable.NewTool("web_search", "Search the internet",
    func(ctx context.Context, args SearchArgs) (any, error) {
        hits, err := doSearch(ctx, args.Query)
        if err != nil {
            return nil, err // becomes an IsError result for the model; the loop continues
        }
        return hits, nil
    })
```

Other situations that also become `IsError` results without breaking the loop:

- the model's argument JSON fails to parse into the tool's argument struct (the error message includes the expected schema so the model can self-correct)
- the model calls a non-existent tool name (`unknown tool`, listing the available tools)
- the approval hook vetoes the call with `Deny(reason)` (see [Agent loop](agent.md))

Only two things actually **abort** a run: the `WithToolCallHook` itself returning an error (treated as a program bug and propagated), and upstream API errors / context cancellation.

## Cancellation and timeouts: five guarantees of a graceful stop

Every entry point (`client.Create` / `client.Stream` / `agent.Run` / `agent.RunStream` / `sess.Ask` / `sess.AskStream`) takes a `context.Context` as its first argument. On cancellation or deadline, the behavior satisfies five guarantees:

1. **The upstream stops too**. The in-flight HTTP/SSE connection is closed immediately; the server notices the disconnect and stops generating — no further tokens are billed.
2. **No new requests are started**. The agent loop checks `ctx.Err()` before every turn, so a canceled loop never issues another model call; retry backoff is interrupted immediately as well.
3. **Partial results are not lost**. When a stream is canceled mid-flight, `Stream` / `RunStream` return a **non-nil partial result**: generated text lands in `FinalText`, accumulated usage in `Usage`; the half-assembled assistant message is kept out of `Messages`.
4. **Tool pairing stays intact**. Tool calls that were requested but not yet executed get a synthesized `IsError` result (`"tool execution skipped: ..."`), so every tool call still has a matching tool result — the partial trajectory remains a valid conversation that every provider's API would accept on replay.
5. **Sessions are not polluted**. A [Session](session.md) only appends to its history when a run **succeeds**; a canceled or failed run leaves no messages behind, so the next `Ask` is not corrupted by a dangling assistant tool-call message (every provider rejects such unpaired history).

Typical usage:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := agent.RunStream(ctx, onEvent, callable.User("Write a detailed market analysis"))
switch {
case err == nil:
    fmt.Println(result.FinalText)
case errors.Is(err, context.DeadlineExceeded):
    // result is non-nil: partial text and usage are intact
    log.Printf("timed out, got %d characters of partial output", len(result.FinalText))
case errors.Is(err, context.Canceled):
    // canceled deliberately
default:
    // APIError or other errors
}
```

## Tool functions must honor cancellation themselves

A running tool handler receives that same ctx, but Go **cannot forcibly kill a goroutine that ignores its context**. The library can only observe cancellation once the tool returns; any slow work inside the tool — database queries, sub-requests, file I/O — must respond to cancellation on its own:

```go
query := callable.NewTool("db_query", "Query the database",
    func(ctx context.Context, args QueryArgs) (any, error) {
        // good: pass ctx into an operation that supports cancellation
        rows, err := db.QueryContext(ctx, args.SQL)
        if err != nil {
            return nil, err
        }
        defer rows.Close()
        // ...
        return nil, nil
    })
```

If a tool goroutine never returns, a cancel request waits until it does; the same applies with `WithParallelToolExecution(true)`, where the loop waits for all parallel tools to wind down. Avoid ctx-free blocking calls in tool implementations (bare `http.Get`, channel receives without a timeout, and so on).

## Request-level escape hatch: WithExtra

For provider parameters the library does not model natively (e.g. OpenAI's `parallel_tool_calls`, Anthropic's `top_k`), `WithExtra` merges arbitrary fields into the top level of the request body:

```go
resp, err := client.Create(ctx,
    callable.NewRequest(callable.User("Hello")).
        WithExtra("top_k", 40).
        WithExtra("metadata", map[string]any{"user_id": "u-123"}))
```

After the request is serialized into the provider's wire format, `WithExtra(key, value)` overlays the key/value onto the top-level JSON object — which means it **can overwrite fields the library sets itself** (e.g. `max_tokens`). That is an intentional escape-hatch design: powerful, but a wrong key name or type produces no compile-time error — it goes straight to the server (usually surfacing as a 400 `*APIError`, whose `Body` carries the provider's complaint). Use it only when no first-class API exists.

## Complete example

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "time"

    "github.com/great-magician-01/callable"
)

func main() {
    client := callable.NewClient(
        callable.NewAnthropicProvider(apiKey, callable.AnthropicURL,
            callable.WithRetries(3), // retry network errors / 429 / 5xx up to 3 times
        ),
        callable.WithModel("claude-sonnet-5"),
    )

    agent := callable.NewAgent(client, callable.WithMaxTurns(25))

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    result, err := agent.Run(ctx, callable.User("..."))
    if err == nil {
        fmt.Println(result.FinalText)
        return
    }

    switch {
    case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
        // canceled/timed out: partial result is intact
        log.Printf("interrupted, %d characters generated", len(result.FinalText))

    case func() bool { var e *callable.MaxTurnsError; return errors.As(err, &e) }():
        var mtErr *callable.MaxTurnsError
        errors.As(err, &mtErr)
        log.Printf("hit the %d-turn limit, partial result available", mtErr.Turns)

    default:
        var apiErr *callable.APIError
        if errors.As(err, &apiErr) {
            log.Printf("API error provider=%s status=%d type=%s: %s",
                apiErr.Provider, apiErr.StatusCode, apiErr.Type, apiErr.Message)
        } else {
            log.Printf("other error: %v", err)
        }
    }
}
```

## See also

- [Agent loop](agent.md): loop structure, approval hooks, max turns
- [Tools](tools.md): tool definitions and the wire format of error feedback
- [Sessions](session.md): when history is written, and persistence
- [Streaming events](streaming.md): the event stream during a mid-stream cancel
- [Getting started](getting-started.md): Client and Provider construction options
