# Structured Output & Sampling Parameters

[中文](../zh/structured-output.md) | **English**

This page covers two request-level capabilities: constraining the model's output to a JSON shape with `ResponseFormat` (structured output), and the `top_p` / stop-sequence sampling parameters. Both can be set on a single request or as Client defaults.

## Structured output (ResponseFormat)

`ResponseFormat` uniformly describes the constraint "the model must answer in this JSON shape":

```go
type ResponseFormat struct {
    // Name identifies the schema. Some providers require it; an empty Name
    // defaults to "output".
    Name   string
    // Schema constrains the output JSON. nil requests free-form JSON mode
    // ("give me JSON, any shape").
    Schema map[string]any
    // Strict asks providers supporting it to guarantee schema compliance.
    Strict bool
}
```

Three constructors:

| Constructor | Description |
|---|---|
| `callable.JSONMode() ResponseFormat` | Free-form JSON mode: the output is guaranteed to be valid JSON of any shape |
| `callable.JSONSchema(name string, schema map[string]any, strict bool) ResponseFormat` | Supply a JSON Schema by hand (as a decoded JSON object) |
| `callable.JSONSchemaFor[T any](name string, strict bool) ResponseFormat` | Reflect the schema from a Go struct — same reflection and `jsonschema` struct tags as `NewTool` (see [Tools](tools.md)) |

### Setting and reading

Set it per request with `Request.WithResponseFormat`, or as a Client default with `callable.WithResponseFormat` (request level wins). Read the result with `Response.DecodeJSON(&v)` — it is literally `json.Unmarshal(resp.Text, v)`, but combined with a format constraint it almost never fails:

```go
type Recipe struct {
    Name  string   `json:"name" jsonschema:"description=Dish name"`
    Steps []string `json:"steps" jsonschema:"description=Cooking steps"`
}

resp, err := client.Create(ctx, callable.NewRequest(
    callable.User("Give me a pancake recipe"),
).WithResponseFormat(callable.JSONSchemaFor[Recipe]("recipe", true)))
if err != nil {
    log.Fatal(err)
}

var recipe Recipe
if err := resp.DecodeJSON(&recipe); err != nil {
    log.Fatal(err)
}
```

`DecodeJSON` also works without any format constraint — it accepts any JSON text response; without a constraint the model may simply return non-JSON, in which case parsing fails.

### Per-provider mapping

The same `ResponseFormat` maps onto each provider's native control field:

| Provider | Wire field | Free-form JSON mode | Schema mode |
|---|---|---|---|
| OpenAI Chat Completions | `response_format` | `{"type":"json_object"}` | `{"type":"json_schema","json_schema":{name,schema,strict}}` |
| OpenAI Responses | `text.format` | `{"type":"json_object"}` | `{"type":"json_schema",name,schema,strict}` |
| Anthropic Messages | `output_config.format` | `{"type":"json_schema","schema":{"type":"object"}}` | `{"type":"json_schema","schema":...}` |

Key points:

- **Anthropic has no free-form JSON mode**: `JSONMode()` is mapped to a permissive object schema (`{"type":"object"}`). Strictness is inherent there — the schema is always enforced, so the `Strict` flag has no effect on Anthropic.
- **Anthropic accepts only a subset of JSON Schema**: constraint keywords like `minimum` / `minLength` are rejected with a 400 `*APIError`. When reusing one schema across providers, keep to structural keywords (`type` / `properties` / `required` / `description` / `enum`) and express constraints in the prompt or field descriptions.
- In free-form JSON mode, OpenAI-style endpoints require the prompt to explicitly mention JSON (e.g. "answer in JSON"), or they may error out or return non-JSON.
- **Support varies by endpoint** (from vendor documentation, verified live against DeepSeek): DeepSeek and GLM/Z.AI only accept `json_object`, and the library automatically downgrades schema mode to `json_object` there, spelling the schema out in the prompt (auto-detected from the base URL, no manual handling needed). Qwen (DashScope), Volcano Ark (beta) and Kimi support `json_schema` natively, so it is sent as-is. Third-party Anthropic-compatible gateways vary (e.g. DeepSeek's Anthropic endpoint silently ignores `output_config` — no error, no enforcement either). For endpoints with uncertain behavior, treat `DecodeJSON` errors as the fallback signal.

### Complete example

```go
package main

import (
    "context"
    "fmt"
    "os"

    callable "github.com/great-magician-01/callable"
)

type Answer struct {
    Summary string   `json:"summary" jsonschema:"description=One-sentence summary"`
    Points  []string `json:"points" jsonschema:"description=Bullet points"`
}

func main() {
    ctx := context.Background()
    client := callable.NewOpenAIClient(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL, "gpt-5")

    resp, err := client.Create(ctx, callable.NewRequest(
        callable.User("Summarize Go interfaces. Answer in JSON."),
    ).WithResponseFormat(callable.JSONSchemaFor[Answer]("answer", true)))
    if err != nil {
        fmt.Fprintln(os.Stderr, "Create:", err)
        os.Exit(1)
    }

    var answer Answer
    if err := resp.DecodeJSON(&answer); err != nil {
        fmt.Fprintln(os.Stderr, "DecodeJSON:", err)
        os.Exit(1)
    }
    fmt.Println(answer.Summary)
    for _, p := range answer.Points {
        fmt.Println("-", p)
    }
}
```

## Sampling parameters: top_p and stop sequences

| Layer | API |
|---|---|
| Request level | `req.WithTopP(v float64)` / `req.WithStopSequences(seq ...string)` |
| Client defaults | `callable.WithTopP(v)` / `callable.WithStopSequences(seq ...)` |

As with `WithTemperature`, the request level overrides the Client default. Per-provider mapping:

| Provider | top_p | Stop sequences |
|---|---|---|
| OpenAI Chat Completions | `top_p` | `stop` |
| OpenAI Responses | `top_p` | Unsupported (the API has no stop parameter; the value is ignored) |
| Anthropic Messages | `top_p` | `stop_sequences` |

**Sampling parameters are omitted in thinking mode**: with thinking enabled (`WithThinking`, see [Thinking Mode](thinking.md)), neither `temperature` nor `top_p` is sent — reasoning models typically reject custom sampling parameters (400). Stop sequences are unaffected and still sent.

## Related docs

- [Getting Started](getting-started.md) — Client defaults and request/response hooks
- [Tools](tools.md) — the full `jsonschema` struct tag syntax
- [Thinking Mode](thinking.md) — thinking configuration and per-endpoint mapping
- [Error Handling](errors.md) — `APIError` and retry semantics
