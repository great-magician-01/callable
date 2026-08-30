# Getting Started

[中文](../zh/getting-started.md) | **English**

This guide covers installing callable, creating a Client with one of the three Providers, using the built-in endpoint constants and dialect auto-detection, and making your first `Create` / `Stream` call.

callable is a unified Go LLM client library: one API speaks three wire formats — **OpenAI Chat Completions**, **OpenAI Responses**, and **Anthropic Messages** — with a full agent loop built on top. The stack has three layers:

```
Agent / Session (automatic tool loop + sessions)  → see agent.md / session.md
Client (Create / Stream / default filling)        → this page
Provider (wire-format adapters)                   → this page
```

## Installation

Requires Go 1.21 or later:

```bash
go get github.com/great-magician-01/callable
```

Module path and package name:

```go
import callable "github.com/great-magician-01/callable"
```

- The root package `callable` is the single public entry point (`callable.go` re-exports everything from the internal packages under `internal/`); one import gives you the full API.
- The network layer (HTTP, SSE) is implemented with the standard library only; the sole third-party dependency is `invopop/jsonschema` (used to reflect tool argument structs into JSON Schema).

## Creating a Client and Provider

The shortest path is one of the three convenience constructors — "Provider + default model" folded into a single call:

```go
client := callable.NewAnthropicClient(apiKey, callable.AnthropicURL, "claude-sonnet-5")
```

| Convenience constructor | Equivalent expansion |
|---|---|
| `NewOpenAIClient(apiKey, baseURL, model string, opts ...ClientOption) *Client` | `NewClient(NewOpenAIProvider(apiKey, baseURL), WithModel(model), opts...)` |
| `NewOpenAIResponsesClient(apiKey, baseURL, model string, opts ...ClientOption) *Client` | `NewClient(NewOpenAIResponsesProvider(apiKey, baseURL), WithModel(model), opts...)` |
| `NewAnthropicClient(apiKey, baseURL, model string, opts ...ClientOption) *Client` | `NewClient(NewAnthropicProvider(apiKey, baseURL), WithModel(model), opts...)` |

When you need a `ProviderOption` (e.g. `WithRetries`, `WithHTTPClient`, `WithCompat`), fall back to the two-step form. The typical assembly order is: **Provider (pick a wire format and endpoint) → Client (fill in defaults like the model)**:

```go
client := callable.NewClient(
    callable.NewAnthropicProvider(apiKey, callable.AnthropicURL),
    callable.WithModel("claude-sonnet-5"),
)
```

The library **does not read any environment variables** (such as `OPENAI_API_KEY`): the API key and endpoint address must be passed explicitly. Read environment variables yourself if you want them (see `firstNonEmptyEnv` in `examples/quickstart/main.go`).

### The three Provider constructors

| Constructor | Wire format | Request path |
|---|---|---|
| `NewOpenAIProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIProvider` | OpenAI Chat Completions | `POST {baseURL}/chat/completions` |
| `NewOpenAIResponsesProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIResponsesProvider` | OpenAI Responses | `POST {baseURL}/responses` |
| `NewAnthropicProvider(apiKey, baseURL string, opts ...ProviderOption) *AnthropicProvider` | Anthropic Messages | `POST {baseURL}/v1/messages` |

```go
callable.NewOpenAIProvider(apiKey, callable.OpenAIURL)           // Chat Completions
callable.NewOpenAIResponsesProvider(apiKey, callable.OpenAIURL)  // Responses
callable.NewAnthropicProvider(apiKey, callable.AnthropicURL)     // Anthropic
```

Behavior details:

- `baseURL` is the API root including any version prefix; trailing slashes are trimmed so the base and the endpoint path concatenate cleanly.
- `NewAnthropicProvider` tolerates a `baseURL` already ending in `/v1` (the request path becomes `/messages` instead of `/v1/messages`), so you never get `/v1/v1/messages`.
- `NewOpenAIProvider` works with any OpenAI-compatible endpoint (GLM, DeepSeek, Qwen, vLLM, Ollama, ...), not just the official OpenAI API.
- For the official OpenAI endpoint (host `api.openai.com`) the newer parameter names are used automatically (`max_completion_tokens`, `stream_options.include_usage`).

## Built-in endpoint constants

Base URLs of common vendors are available as constants — pass one straight to a constructor. For endpoints without a constant, pass the literal URL string.

| Constant | Value | Applicable constructors |
|---|---|---|
| `callable.OpenAIURL` | `https://api.openai.com/v1` | `NewOpenAIProvider` / `NewOpenAIResponsesProvider` |
| `callable.AnthropicURL` | `https://api.anthropic.com` | `NewAnthropicProvider` |
| `callable.DeepSeekURL` | `https://api.deepseek.com` | `NewOpenAIProvider` |
| `callable.DeepSeekAnthropicURL` | `https://api.deepseek.com/anthropic` | `NewAnthropicProvider` |
| `callable.GLMURL` | `https://open.bigmodel.cn/api/paas/v4` | `NewOpenAIProvider` |
| `callable.GLMAnthropicURL` | `https://open.bigmodel.cn/api/anthropic` | `NewAnthropicProvider` |
| `callable.ZAIURL` | `https://api.z.ai/api/paas/v4` | `NewOpenAIProvider` |
| `callable.ZAIAnthropicURL` | `https://api.z.ai/api/anthropic` | `NewAnthropicProvider` |
| `callable.QwenURL` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `NewOpenAIProvider` |
| `callable.ArkURL` | `https://ark.cn-beijing.volces.com/api/v3` | `NewOpenAIProvider` |
| `callable.KimiURL` | `https://api.moonshot.cn/v1` | `NewOpenAIProvider` |
| `callable.KimiAnthropicURL` | `https://api.moonshot.cn/anthropic` | `NewAnthropicProvider` |

```go
callable.NewAnthropicProvider(key, callable.DeepSeekAnthropicURL) // DeepSeek's Anthropic-compatible endpoint
callable.NewOpenAIProvider(key, callable.QwenURL)                 // Qwen, dialect auto-detected
```

Note: `DeepSeekURL` does not include `/v1` (that is the canonical base URL from DeepSeek's docs; it also accepts a `/v1` suffix, but only as an OpenAI SDK convention). Likewise, GLM's `GLMURL` already contains the `/v4` prefix.

## OpenAI-compatible endpoints and Compat dialects

Third-party OpenAI-compatible endpoints use non-standard fields for capabilities like **thinking controls**. callable models these dialects as a `Compat` bitmask and **auto-detects the right one from the baseURL host**:

| Constant | Detected when the host | Endpoint dialect (thinking fields) |
|---|---|---|
| `callable.CompatNone` | (default, no match) | Standard OpenAI fields (`reasoning_effort`) |
| `callable.CompatGLM` | contains `bigmodel.cn` or `zhipuai`, or is `z.ai` / `*.z.ai` | `thinking:{type:"enabled"}` + `reasoning_effort` (medium mapped to high) |
| `callable.CompatQwen` | contains `dashscope` | `enable_thinking:true` + `thinking_budget` |
| `callable.CompatDeepSeek` | contains `deepseek` | `thinking:{type:"enabled"}` + `reasoning_effort`; parses `reasoning_content` in responses |
| `callable.CompatArk` | contains `volces.com` | `thinking:{type:"enabled"}` + `reasoning_effort` (passed through as-is) |

Detection only affects how thinking-related request fields are written (and how reasoning content in responses is parsed); nothing else about the request changes. The full per-endpoint field mapping is in [Thinking Mode](thinking.md).

### Overriding with WithCompat

Detection works for the built-in constants and for self-hosted gateways on the same domain. If a proxy rewrites the path, or the endpoint behaves differently from the detected dialect, use `WithCompat` to replace the detected value entirely:

```go
// A self-hosted gateway proxies GLM, but the host doesn't match bigmodel.cn:
callable.NewOpenAIProvider(key, "https://llm.example.com/glm",
    callable.WithCompat(callable.CompatGLM))

// Using DashScope's OpenAI-compatible mode without sending enable_thinking:
callable.NewOpenAIProvider(key, callable.QwenURL,
    callable.WithCompat(callable.CompatNone))
```

Note that `WithCompat` is a **full replacement**, not a bitwise addition: it discards the auto-detected dialect and uses exactly the value you pass.

## Provider options

All Provider constructors accept `opts ...ProviderOption`:

| Option | Signature | Description |
|---|---|---|
| `WithHTTPClient` | `WithHTTPClient(client *http.Client) ProviderOption` | Supply a custom `*http.Client` (proxy, TLS, timeouts, ...). Passing `nil` is ignored. |
| `WithHeader` | `WithHeader(key, value string) ProviderOption` | Adds a header to every provider request. Applied **after** authentication, so it can override defaults like `Authorization`; a same-named key is in turn overridden by the request-level `Request.WithHeader`. |
| `WithRetries` | `WithRetries(n int) ProviderOption` | How many times transient failures (network errors, 429, 5xx) are retried. Default 3; pass 0 to disable; negative values are clamped to 0. |
| `WithRetryBackoff` | `WithRetryBackoff(delays ...time.Duration) ProviderOption` | Replaces the default retry wait schedule (3s/10s/30s): `delays[i]` is the wait before retry i+1; retries beyond the schedule reuse the last delay. Combine with `WithRetries`. |
| `WithCompat` | `WithCompat(c Compat) ProviderOption` | Overrides the auto-detected endpoint dialect (see previous section). |

Behavior details:

- **Retry backoff**: a fixed schedule by default — 3s before the first retry, 10s before the second, 30s before the third; retries beyond the schedule reuse the last delay (30s). `WithRetryBackoff` replaces the schedule wholesale. The wait is interruptible by context cancellation.
- **Retryable status codes**: only 429 and 5xx; other 4xx responses (400, 401, 403, ...) are not retried and surface immediately as `*callable.APIError`. See [Error Handling](errors.md).
- **The default HTTP client has no global timeout**: long streams must not be cut off, so cancellation/timeouts are controlled exclusively through `context.Context`. If you want a timeout, either inject a client with `Timeout` via `WithHTTPClient` or (preferably) use `context.WithTimeout`.
- **There is no default baseURL**: the library ships no default endpoint; the `baseURL` argument is required.

## Client options

```go
func NewClient(provider Provider, opts ...ClientOption) *Client
```

| Option | Signature | Description |
|---|---|---|
| `WithModel` | `WithModel(model string) ClientOption` | Default model ID |
| `WithMaxTokens` | `WithMaxTokens(n int) ClientOption` | Default maximum output tokens |
| `WithTemperature` | `WithTemperature(v float64) ClientOption` | Default sampling temperature |
| `WithTopP` | `WithTopP(v float64) ClientOption` | Default nucleus-sampling probability mass, see [Structured Output & Sampling](structured-output.md) |
| `WithStopSequences` | `WithStopSequences(seq ...string) ClientOption` | Default stop sequences (unsupported by OpenAI Responses — ignored there), see above |
| `WithResponseFormat` | `WithResponseFormat(f ResponseFormat) ClientOption` | Default output format constraint (structured output), see above |
| `WithClientHeader` | `WithClientHeader(key, value string) ClientOption` | Adds an HTTP header to every request this client sends (including the Agent loop's internal calls); the request-level `Request.WithHeader` wins on key conflicts |
| `WithRequestHook` | `WithRequestHook(hooks ...RequestHook) ClientOption` | Registers request hooks, invoked in order before every request is sent; see "Request/response hooks" below |
| `WithResponseHook` | `WithResponseHook(hooks ...ResponseHook) ClientOption` | Registers response hooks, invoked in order after every call finishes; see "Request/response hooks" below |

Except for the hooks, these are **defaults**: they only take effect when the `Request` itself leaves the field unset (filled only when `Request.Model == ""`, `MaxTokens == 0`, `Temperature == nil`, `TopP == nil`, `Stop == nil`, `Format == nil`). Request-level `WithModel` / `WithMaxTokens` / `WithTemperature` etc. override the Client defaults:

```go
req := callable.NewRequest(callable.User("...")).
    WithModel("gpt-5-mini").   // overrides the client's default model
    WithTemperature(0.2)
```

When applying defaults the Client **copies** the request instead of mutating it, so the same `*Request` can be reused safely.

### Request/response hooks

Client-level observability hooks, useful for logging, distributed tracing, and token cost accounting:

```go
type RequestHook  func(ctx context.Context, req *Request)
type ResponseHook func(ctx context.Context, req *Request, resp *Response, err error)
```

```go
client := callable.NewAnthropicClient(apiKey, callable.AnthropicURL, "claude-sonnet-5",
    callable.WithRequestHook(func(ctx context.Context, req *callable.Request) {
        log.Printf("→ %s (%d messages)", req.Model, len(req.Messages))
    }),
    callable.WithResponseHook(func(ctx context.Context, req *callable.Request, resp *callable.Response, err error) {
        if err == nil {
            log.Printf("← %d in / %d out tokens", resp.Usage.InputTokens, resp.Usage.OutputTokens)
        }
    }),
)
```

- A `RequestHook` fires right before the request is sent and sees the request with the Client defaults already applied.
- A `ResponseHook` fires after the call finishes; `resp` / `err` are exactly what the provider returned (for `Stream`, resp is the assembled final response).
- Both options accept multiple hooks, run in registration order; hooks must not mutate the request or response.
- Every internal model call of an Agent passes through the hooks as well — an N-turn loop triggers them N times.

### Listing available models (GET /models)

`Client.ListModels` wraps the provider's model-listing endpoint and returns a unified `ModelInfo`:

```go
models, err := client.ListModels(ctx)
if err != nil {
    log.Fatal(err)
}
for _, m := range models {
    fmt.Println(m.ID, m.DisplayName, m.OwnedBy, m.Created)
}
```

- OpenAI-compatible endpoints serve `GET {baseURL}/models`; Anthropic serves `GET {baseURL}/v1/models`, and pagination is followed automatically.
- `DisplayName` is only returned by Anthropic, `OwnedBy` only by OpenAI-compatible endpoints; `Created` is parsed into a `time.Time` when the endpoint provides it.
- All three built-in providers implement the `ModelLister` interface; `ListModels` returns an error for custom providers that do not. This is not a model call, so request/response hooks do not fire.

## Minimal call examples

### Create (non-streaming)

```go
resp, err := client.Create(ctx, callable.NewRequest(callable.User("Explain closures in one sentence")))
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.Text)   // assembled answer text
fmt.Println(resp.Usage)  // token usage
```

### Stream (streaming)

```go
_, err := client.Stream(ctx, callable.NewRequest(callable.User("Tell me a story")), func(ev callable.Event) {
    if d, ok := ev.(callable.TextDeltaEvent); ok {
        fmt.Print(d.Delta)
    }
})
```

`Stream` forwards every streaming event to the callback and returns the fully assembled `*Response` once the stream ends. The complete event list is in [Streaming Events](streaming.md).

### Complete runnable program

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

    // The endpoint must be passed explicitly; the key is read from the
    // environment here, but the library itself reads no env vars.
    client := callable.NewOpenAIClient(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL, "gpt-5",
        callable.WithMaxTokens(2048),
    )

    // Non-streaming: get the full Response
    resp, err := client.Create(ctx, callable.NewRequest(callable.User("Explain recursion in one sentence")))
    if err != nil {
        fmt.Fprintln(os.Stderr, "Create:", err)
        os.Exit(1)
    }
    fmt.Println(resp.Text)

    // Streaming: per-event callback
    _, err = client.Stream(ctx, callable.NewRequest(callable.User("Describe goroutines in three sentences")), func(ev callable.Event) {
        if d, ok := ev.(callable.TextDeltaEvent); ok {
            fmt.Print(d.Delta)
        }
    })
    fmt.Println()
    if err != nil {
        fmt.Fprintln(os.Stderr, "Stream:", err)
        os.Exit(1)
    }
}
```

The Anthropic variant only swaps the convenience constructor:

```go
callable.NewAnthropicClient(os.Getenv("ANTHROPIC_API_KEY"), callable.AnthropicURL, "claude-sonnet-5",
    callable.WithMaxTokens(2048),
)
```

Runnable examples live in `examples/quickstart` (picks a provider based on which API key is set) and `examples/deepseek` (full live-API scenarios across all three wire formats).

## Notes and pitfalls

- **API keys and env vars**: the library reads no environment variables and ships no default endpoint; key and baseURL are always supplied explicitly by the caller.
- **Timeouts**: the default `http.Client` has no timeout, so manage call lifecycles with `context.WithTimeout` / `context.WithCancel`. Cancellation is graceful: the upstream connection closes immediately, and cancelling mid-stream returns a **non-nil partial result** with an error satisfying `errors.Is(err, context.Canceled)`.
- **`Create` / `Stream` are single calls**: they do not run the tool loop. For the automatic "model → tools → model → ..." loop, use the [Agent Loop](agent.md) (`NewAgent` + `Run` / `RunStream`).
- **Conversation history is your job**: `Create` / `Stream` are stateless; multi-turn conversations need the history passed into `NewRequest`, or use [Sessions](session.md) to maintain it automatically.
- **Custom headers (pass-through) come in three levels**: provider-level `WithHeader` (every request from that provider) → client-level `WithClientHeader` (every request from that client, including Agent internal calls) → request-level `Request.WithHeader` (a single call, e.g. passing a per-call tracing id through). On key conflicts the later level wins, and all three are applied after authentication — a same-named key overrides `Authorization` / `x-api-key`. Useful for gateway-specific headers, but be careful not to clobber authentication.

## Next steps

- [Message Model](messages.md) — `Message` / `Part` and the constructor helpers
- [Structured Output & Sampling](structured-output.md) — JSON mode / JSON Schema, `DecodeJSON`, `top_p` and stop sequences
- [Streaming Events](streaming.md) — the complete streaming event list
- [Agent Loop](agent.md) — the automatic tool loop
- [Thinking Mode](thinking.md) — per-endpoint thinking field mapping
- [Error Handling](errors.md) — `APIError`, retries, and cancellation semantics
