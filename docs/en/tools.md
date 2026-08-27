# Tools

[中文](../zh/tools.md) | **English**

Tools bridge your Go functions and the model. callable offers three ways to define one:

- `NewTool[A]` (recommended): a generic constructor that reflects the argument struct into a JSON Schema;
- `NewRawTool`: a hand-written JSON Schema, with the handler receiving the raw JSON arguments;
- implementing the `Tool` interface: full control, for advanced use cases.

Once tools are registered with an [Agent](agent.md), the agent loop runs the whole "model requests a tool → execute → feed the result back" cycle automatically. A failing tool does **not** abort the loop — the error is fed back to the model so it can adjust.

## Quick example

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

	weather := callable.NewTool("get_weather", "Get the current weather for a city",
		func(ctx context.Context, args WeatherArgs) (any, error) {
			unit := "°C"
			if args.Unit == "fahrenheit" {
				unit = "°F"
			}
			// Returning a string passes it through verbatim as the tool result.
			return fmt.Sprintf(`{"city":%q,"temp":26,"unit":%q,"condition":"sunny"}`, args.City, unit), nil
		})

	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("You are a pragmatic weather assistant."),
		callable.WithTools(weather),
	)

	result, err := agent.Run(ctx, callable.User("How warm is Beijing right now? Celsius, please."))
	if err != nil {
		panic(err)
	}
	fmt.Println(result.FinalText)
}
```

## NewTool[A]: the generic constructor

```go
func NewTool[A any](name, description string,
    fn func(ctx context.Context, args A) (any, error)) Tool
```

- `name` / `description`: what the model sees when choosing a tool. Names must be unique within an agent; on duplicate registration the later tool is ignored (user tools are registered before built-ins like `read_skill`, so they win).
- `fn`: the execution body. For each call, the model-produced JSON (`call.Arguments`) is unmarshaled into `A` before `fn` runs.
- The parameter schema is reflected once at construction time by [invopop/jsonschema](https://github.com/invopop/jsonschema) (callable's only third-party dependency) with `DoNotReference: true` — all `$ref`s are inlined because providers dislike external references — producing a draft-07-compatible schema. The schema root is always `{"type":"object"}` (a hard provider requirement); a fieldless `A` degrades to an empty object.

### jsonschema struct tags

Field names come from the `json` tag; fields tagged `json:",omitempty"` are **optional**, everything else lands in the schema's `required` list. The `jsonschema` tag adds descriptions and constraints:

```go
type SearchArgs struct {
	// Required string with a description
	Query string `json:"query" jsonschema:"description=Search keywords"`
	// Optional enum: repeat enum= to list all valid values
	Sort string `json:"sort,omitempty" jsonschema:"description=Sort order,enum=time,enum=relevance"`
	// Optional number with a default and range constraints
	Limit int `json:"limit,omitempty" jsonschema:"description=Result count,default=10,minimum=1,maximum=100"`
}
```

The schema advertised to the model looks roughly like:

```json
{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "Search keywords"},
    "sort":  {"type": "string", "description": "Sort order", "enum": ["time", "relevance"]},
    "limit": {"type": "integer", "description": "Result count", "default": 10, "minimum": 1, "maximum": 100}
  },
  "required": ["query"]
}
```

### Nested structs, slices, maps

Complex argument shapes reflect directly, no extra work needed:

```go
type Range struct {
	Start int `json:"start" jsonschema:"description=First page"`
	End   int `json:"end" jsonschema:"description=Last page"`
}

type ExportArgs struct {
	Format string            `json:"format" jsonschema:"description=Export format,enum=csv,enum=pdf"`
	Range  Range             `json:"range" jsonschema:"description=Page range"`          // nested struct
	Tags   []string          `json:"tags,omitempty" jsonschema:"description=Tag filter"` // slice
	Meta   map[string]string `json:"meta,omitempty" jsonschema:"description=Extra metadata"` // map
}
```

Nested structs appear as inlined objects (with their own `properties` / `required`) — no `$ref` is ever emitted.

> The supported tag keys go well beyond the above — `required`, `oneof`, `title`, `pattern`, `minLength` and anything else invopop/jsonschema understands work too; see that library's docs.

## NewRawTool: hand-written JSON Schema

```go
func NewRawTool(name, description, parametersJSON string,
    fn func(ctx context.Context, rawArgs string) (any, error)) Tool
```

Use this when a schema can't be expressed as a struct (e.g. `oneOf`, dynamic shapes) or you want to parse arguments yourself:

```go
sqlQuery := callable.NewRawTool("run_sql", "Run a read-only SQL query", `{
	"type": "object",
	"properties": {
		"sql": {"type": "string", "description": "A SELECT statement"}
	},
	"required": ["sql"],
	"additionalProperties": false
}`, func(ctx context.Context, rawArgs string) (any, error) {
	// rawArgs is the raw JSON string produced by the model; parse it yourself
	var args struct {
		SQL string `json:"sql"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return nil, fmt.Errorf("bad arguments: %w", err) // becomes an IsError result for the model
	}
	return runReadOnlyQuery(ctx, args.SQL)
})
```

Notes:

- `parametersJSON` is parsed once at construction; **invalid JSON panics** (it's a programming error, surfaced early).
- An empty string means "no parameters": `Parameters` stays `nil` and degrades to `{"type":"object"}` when sent to a provider.
- Unlike `NewTool`, the handler receives the raw string and the library performs no parsing or validation — handle malformed arguments yourself and return an error.

## Handler return-value rules

Both constructors use handlers returning `(any, error)`, normalized (`coerceToolOutput`) as follows:

| Return value | Tool result |
|---|---|
| `nil` | Empty result (`ToolResult{}`, empty content) |
| `string` | Used verbatim (good for plain text or hand-built JSON strings) |
| `callable.ToolResult` | Used as-is (full control over `IsError`) |
| Any other type | `json.Marshal`ed into a JSON string (return structs/maps/slices directly) |
| `error != nil` | `ErrorResult(err)`: `Content = err.Error()`, `IsError = true` |
| `json.Marshal` fails | Also becomes an `IsError` result; the loop is not interrupted |

So returning a plain struct yields well-formed JSON automatically:

```go
callable.NewTool("get_user", "Look up a user by ID",
	func(ctx context.Context, args struct {
		ID int `json:"id" jsonschema:"description=User ID"`
	}) (any, error) {
		u, err := db.FindUser(ctx, args.ID)
		if err != nil {
			return nil, err // automatically fed back to the model
		}
		return u, nil // json.Marshal'ed as the result content
	})
```

## Error handling: tool failures don't break the loop

This is a core callable design: **every tool-level failure goes back to the model; only API/network errors abort the agent run** (see [Error Handling](errors.md)).

Cases that become an `IsError=true` result for the model:

- the handler returns an error (the common business failure — the model can retry or try another path);
- the model's argument JSON doesn't unmarshal into `A` (the feedback includes the expected schema so the model can self-correct);
- `json.Marshal` fails on the return value;
- the model calls a nonexistent tool (fed back as `unknown tool "xxx" (available tools: ...)`);
- the call is rejected by an [approval hook](agent.md) via `Deny(reason)` (fed back as `Tool call denied: ...`);
- on context cancellation, tool calls not yet executed get a synthesized `"tool execution skipped: ..."` result, so every tool call stays paired with a result and the history remains a valid, replayable conversation.

How `IsError` maps to the wire:

- Anthropic: `tool_result` block with `is_error: true` (the content gets an `Error: ` prefix unless it already starts with `Error`);
- OpenAI Chat Completions: a `role=tool` message carrying the error text;
- OpenAI Responses: a `function_call_output` item carrying the error text.

What still makes `Run` return an error: provider 4xx/5xx (after retries are exhausted), network failures, and an error returned by the `ToolCallHook` itself.

## ToolResult / TextResult / ErrorResult

```go
type ToolResult struct {
	Content string // output shown to the model, usually JSON or plain text
	IsError bool   // tells the model the execution failed
}

func TextResult(content string) ToolResult // ToolResult{Content: content}
func ErrorResult(err error) ToolResult     // ToolResult{Content: err.Error(), IsError: true}
```

Return a `ToolResult` from the handler to bypass normalization when you need exact control — e.g. "a business failure that isn't a Go error":

```go
if resp.StatusCode == 404 {
	return callable.ErrorResult(fmt.Errorf("user %d not found", args.ID)), nil
}
```

> Note the shape: return `ErrorResult` with a nil error. Returning `(nil, err)` gets wrapped into the same `ErrorResult(err)` by the library — both spellings are equivalent.

## The Tool interface: full control

```go
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"` // nil means "no parameters"
}

type Tool interface {
	Definition() ToolDefinition // the schema advertised to the model
	Execute(ctx context.Context, rawArgs string) ToolResult
}
```

Implement the interface for maximum freedom (dynamic schemas, custom argument parsing, non-JSON output, ...). `Execute` returns only a `ToolResult`, never an error — to surface a failure to the model, return a result with `IsError=true`, mirroring the built-in constructors:

```go
type timeTool struct{}

func (timeTool) Definition() callable.ToolDefinition {
	return callable.ToolDefinition{
		Name:        "now",
		Description: "Return the current server time",
		// nil Parameters: a parameterless tool
	}
}

func (timeTool) Execute(ctx context.Context, rawArgs string) callable.ToolResult {
	return callable.TextResult(time.Now().Format(time.RFC3339))
}

agent := callable.NewAgent(client, callable.WithTools(timeTool{}))
```

## Caveats

- **Validation is advisory**: schema constraints like `enum`/`minimum` are hints to the model; `NewTool` only guarantees the JSON unmarshals into `A`. Re-validate critical arguments in the handler and return an error when they're invalid.
- **Parallel execution**: when the model emits multiple tool calls in one turn they run sequentially by default (deterministic). With `WithParallelToolExecution(true)`, guard any state shared between tools yourself.
- **Respect the ctx**: the handler receives the same ctx as the agent run, which is canceled when the run stops. Slow operations inside a tool (HTTP, DB) should propagate and honor it — Go can't kill a goroutine that ignores ctx.
- **Name conflicts**: within one agent, a duplicate tool name is ignored — first registration wins. User tools are registered before built-ins, so you can shadow `read_skill` / `load_agent` with a same-named tool (or disable the built-in first with `WithSkillToolDisabled` and friends, then register your own).
- **Schema size**: the schema is generated once but sent with every request; deeply nested structures cost tokens. Split tools or trim descriptions when needed.
- **Observing execution**: listen for `ToolCallEvent` / `ToolResultEvent` via `RunStream` to watch every call and result live — see [Streaming Events](streaming.md); for pre-execution approval/rewriting see `WithToolCallHook` in [Agent Loop](agent.md).

## See also

- [Getting Started](getting-started.md) — your first runnable agent
- [Agent Loop](agent.md) — the tool loop, approval hooks, parallel execution, max turns
- [Streaming Events](streaming.md) — `ToolCallEvent` / `ToolResultEvent` and friends
- [Error Handling](errors.md) — API errors, retries and cancellation
- [Skills](skills.md) / [Sub-Agents](subagents.md) — the built-in read_skill / load_agent tools
