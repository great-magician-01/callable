# Message Model

[中文](../zh/messages.md) | **English**

callable has exactly one message model internally: `Message{Role, Parts}`. Whether you talk to OpenAI Chat Completions, OpenAI Responses, or Anthropic Messages, your code builds the same messages — conversion to and from each wire format happens entirely inside the Provider adapters. This document covers the model in full.

## Role: who authored a message

`Role` is a string type with exactly four constants:

| Constant | Value | Meaning |
|---|---|---|
| `callable.RoleSystem` | `"system"` | System prompt. Chat Completions maps it to `role=system` messages (merged); Responses maps it to the top-level `instructions` field; Anthropic maps it to the top-level `system` field |
| `callable.RoleUser` | `"user"` | User input (text, images, or interleaved) |
| `callable.RoleAssistant` | `"assistant"` | Model output (text, thinking, tool calls). Normally produced by responses; can also be built by hand to seed history |
| `callable.RoleTool` | `"tool"` | Tool execution results. On the Anthropic wire these actually become `tool_result` blocks inside a user message — the adapter handles this transparently |

## The Message struct

```go
type Message struct {
    Role  Role
    Parts []Part
    Extra map[string]json.RawMessage // unrecognized fields of the response message object (see "Preserving unrecognized data")
    // plus unexported per-provider round-trip data (see "History fidelity")
}
```

A single message may mix several kinds of content (`Parts` is an ordered slice; order is content order). One model turn, for example, might be `[ThinkingPart, ToolCallPart]` or `[ThinkingPart, TextPart]`.

`Message` offers four convenience accessors that filter Parts by type:

```go
func (m Message) Text() string                    // concatenation of all TextPart contents
func (m Message) Thinking() string                // concatenation of all ThinkingPart contents
func (m Message) ToolCalls() []ToolCallPart       // all ToolCallParts, in order
func (m Message) ToolResultsOf() []ToolResultPart // all ToolResultParts, in order
```

## The sealed Part family

`Part` is a sealed interface — it has an unexported method, so **you cannot implement custom Part types**. There are exactly six concrete types, each serialized to JSON with a `"type"` discriminator.

### TextPart

Plain text content.

```go
type TextPart struct {
    Text string `json:"text"`
}
```

JSON form: `{"type":"text","text":"..."}`. Constructor: `callable.Text(text string) TextPart`.

### ImagePart

An image reference. For supported formats, per-provider conversion, and limits see [Image Input](images.md); only the fields are documented here:

```go
type ImagePart struct {
    Path      string `json:"path,omitempty"`       // local file path
    URL       string `json:"url,omitempty"`        // remote URL, passed to the API untouched
    Data      []byte `json:"data,omitempty"`       // raw image bytes
    MediaType string `json:"media_type,omitempty"` // MIME type, e.g. "image/png"; auto-detected when empty
}
```

- **Set exactly one source**: `Path`, `URL`, and `Data` are mutually exclusive image sources; use the constructors below instead of filling fields directly.
- **Lazy resolution**: construction only stores a reference. Reading the file, detecting the media type, and base64 encoding all happen at request time, inside the target provider — so the same message history can be sent to any provider. The trade-off: errors such as a missing file only surface at request time.
- When `MediaType` is empty it is detected from the file extension first (jpg/jpeg/png/gif/webp), then by sniffing the bytes with `http.DetectContentType`; a result that is not `image/*` produces an error.

### ThinkingPart

Model reasoning output ("thinking" / reasoning). See [Thinking Mode](thinking.md) for configuration.

```go
type ThinkingPart struct {
    Text      string `json:"text"`                // reasoning text
    Signature string `json:"signature,omitempty"` // Anthropic thinking block signature (round-trip only)
    ID        string `json:"id,omitempty"`        // OpenAI Responses reasoning item id (round-trip only)
}
```

Keeping thinking in history and replaying it on the next turn is a correctness requirement: Anthropic demands thinking blocks (with signature) be sent back verbatim when thinking + tools are enabled; Responses needs its reasoning items replayed; DeepSeek/GLM-style endpoints need `reasoning_content`. `Signature` and `ID` are filled automatically by the provider when parsing a response — **never construct them by hand**; they are only meaningful to the provider that produced them.

### ToolCallPart

A tool invocation requested by the model.

```go
type ToolCallPart struct {
    ID        string `json:"id"`        // provider-assigned call id ("tool_use" id / "call_id")
    Name      string `json:"name"`      // tool name
    Arguments string `json:"arguments"` // raw JSON arguments produced by the model
}
```

Note that `Arguments` is a **raw JSON string** (e.g. `{"city":"Beijing"}`), not an object — `json.Unmarshal` it yourself; the agent loop passes it around as a string too. When constructing one by hand, make sure it is valid JSON and that `ID` is non-empty — a later `ToolResultPart.ToolCallID` pairs with it.

### ToolResultPart

The outcome of a `ToolCallPart`, produced automatically by the agent loop, or built manually when replaying a conversation.

```go
type ToolResultPart struct {
    ToolCallID string `json:"tool_call_id"`       // matches the corresponding ToolCallPart.ID
    Name       string `json:"name"`               // tool name
    Content    string `json:"content"`            // tool output as text (usually JSON)
    IsError    bool   `json:"is_error,omitempty"` // marks a failed execution so the model can react
}
```

- Every `ToolCallPart` in history must have a matching `ToolResultPart` (in the same message or in the following `RoleTool` message), otherwise some APIs — Anthropic in particular — reject the request outright. The agent loop guarantees pairing, synthesizing `IsError` results for calls skipped due to cancellation.
- `IsError=true` does not break the agent loop — the error is fed back to the model, which can retry or pick another approach. In the Responses format the error output is prefixed with `"Error: "`.

### RawPart

A provider content block the unified model does not understand, preserved as its **original wire JSON**. Typical sources: Anthropic server-tool blocks (`server_tool_use`, `web_search_tool_result`), `redacted_thinking`, custom blocks injected by gateways/relays, and block types added by a provider after this library version (unknown Responses output items likewise).

```go
type RawPart struct {
    Provider  string          `json:"provider,omitempty"` // wire format owner (Provider.Name(), e.g. "anthropic")
    BlockType string          `json:"block_type"`         // the block's own type on the wire (e.g. "server_tool_use")
    Raw       json.RawMessage `json:"raw"`                // the block's complete original JSON
}
```

- Filled automatically when the provider parses a response; never construct it by hand.
- **Replay**: when the message goes back to the same provider (Anthropic / OpenAI Responses), a RawPart replays its `Raw` verbatim, so unknown blocks survive multi-turn conversations; other providers ignore it.
- JSON form: `{"type":"raw","provider":"anthropic","block_type":"server_tool_use","raw":{...}}` — persists with the rest of the message history.

## Message constructors

```go
func System(text string) Message
func User(parts ...any) Message
func Assistant(parts ...any) Message
func ToolResults(results ...ToolResultPart) Message
```

- `System` takes plain text and produces a `RoleSystem` message. You rarely need it by hand — the Agent's `WithSystemPrompt` is more convenient; merging/promotion of system messages is handled per provider (see the table above).
- `User` / `Assistant` take any number of arguments, each of which may be:
  - a `string` → wrapped into a `TextPart`
  - a `Part` → appended as-is (`TextPart`, `ImagePart`, `ThinkingPart`, ...)
  - a `[]Part` → flattened and appended
  - anything else → **panics** (a malformed message is a programming error and fails fast on purpose)
- `Assistant` is mainly for seeding history manually (few-shot examples, replaying a persisted conversation); in normal flow assistant messages come from responses.
- `ToolResults` builds a `RoleTool` message carrying one or more `ToolResultPart`s — used to replay a tool execution when constructing history by hand.

```go
callable.System("You are a pragmatic assistant.")
callable.User("What is in this picture?", callable.Image("/tmp/a.png")) // interleaved text + image
callable.Assistant("Paris is the capital of France.")                   // seeding history
callable.ToolResults(callable.ToolResultPart{
    ToolCallID: "call_1",
    Name:       "get_weather",
    Content:    `{"temp": 26}`,
})
```

## Part constructors

```go
func Text(text string) TextPart
func Image(ref string) ImagePart                       // local path, or a URL starting with http(s)://
func ImageURL(url string) ImagePart                    // remote URL; bytes pass through untouched
func ImageBytes(data []byte, mediaType string) ImagePart // raw bytes + MIME type
```

- `Image` tells paths from URLs by prefix (`http://` / `https://` means URL).
- `ImageBytes` expects a full MIME type such as `"image/png"`; pass an empty string to sniff it from the bytes.
- For supported formats, per-provider wire conversion, and size limits, see [Image Input](images.md).

## History fidelity (ProviderExtra)

Multi-turn tool loops require the previous assistant message to be replayed **faithfully**, but each wire format carries data that does not fit the unified model. To handle this, `Message` holds raw payloads keyed by provider name:

```go
func (m *Message) SetProviderExtra(provider string, raw json.RawMessage)
func (m Message) ProviderExtra(provider string) json.RawMessage
```

Per-provider replay mechanisms:

| Provider | What is replayed | Where it lives |
|---|---|---|
| Anthropic | thinking block `signature` | `ThinkingPart.Signature` |
| OpenAI Responses | raw output items verbatim (reasoning items, etc.) | `providerExtra["openai-responses"]` |
| Chat Completions compatible endpoints (GLM/DeepSeek/Qwen, ...) | `reasoning_content` thinking text | `ThinkingPart.Text` (written back into the assistant message's `reasoning_content` field) |

Behavior notes:

- These fields are filled **automatically** by the provider when parsing a response; the agent loop and Session preserve them as-is. You normally never touch them.
- `providerExtra` is keyed by `Provider.Name()` and only takes effect when the history is sent back to the **same provider**; switching providers ignores the extras (unified fields like `ThinkingPart.Text` are still converted on a best-effort basis).
- Responses reasoning items **cannot be reconstructed** from a `ThinkingPart` — they rely on the verbatim payload in `providerExtra`. If you hand-build assistant history for the Responses API, reasoning continuity is lost (text and tool calls are unaffected).
- Advanced uses (moving history across processes, auditing) can read/write `SetProviderExtra` / `ProviderExtra` directly; the `provider` argument is a `Provider.Name()` value, e.g. `"openai-responses"`.

## Preserving unrecognized data (Extra and RawPart)

Some gateways/relays piggyback custom fields onto responses, and providers add fields or content blocks over time. Response parsing follows a "lose nothing" rule: **every field and block the unified model does not map is preserved as its original JSON**, in four places:

| Location | What is preserved |
|---|---|
| `Response.Extra map[string]json.RawMessage` | unmapped top-level response fields (e.g. `id`, `created`, `model`, gateway traces, future fields) |
| `Message.Extra map[string]json.RawMessage` | unmapped fields of the assistant message object (e.g. Chat Completions' `annotations`, `refusal`); informational only, never sent back |
| `Usage.Extra map[string]json.RawMessage` | unmapped usage accounting fields (e.g. `total_tokens`, `server_tool_use`); on `Usage.Add` the later turn wins per key |
| `RawPart` (previous section) | unrecognized content blocks / output items, kept verbatim |

Behavior notes:

- Values are **raw JSON fragments** (`json.RawMessage`), including any unknown substructure inside them.
- Streaming matches one-shot behavior: Chat Completions streams merge unknown top-level chunk fields (later chunks win); Anthropic streams keep the unknown block's `content_block_start` payload and fold `input_json_delta` increments back into its `input` field.
- `Extra` never affects request construction — use the request-level `WithExtra` escape hatch for that (see [Error Handling](errors.md)).
- Attach `WithResponseHook` to observe the full `Response` (including `Extra`) of every call.

## JSON serialization and persistence

`Message` implements `json.Marshaler` / `json.Unmarshaler` and serializes completely (including provider extras), so it can be persisted and restored losslessly. The `[]Message` returned by `Session.History()` marshals the same way — see [Sessions](session.md).

The wire format:

```json
{
  "role": "assistant",
  "parts": [
    {"type": "thinking", "text": "check the weather first...", "signature": "EqoBCgI..."},
    {"type": "tool_call", "id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"Beijing\"}"}
  ],
  "provider_extra": {
    "openai-responses": [{"type": "reasoning", "id": "rs_123"}]
  }
}
```

Deserialization edge cases:

- Each element of `parts` is restored to its concrete type via the `"type"` field; an **unknown type is an error** (`unknown message part type`).
- An empty `role` defaults to `"user"`.
- A `null` `parts` deserializes to an empty slice; empty Parts serialize as `[]`, not `null`.
- `ImagePart.Data` (`[]byte`) serializes as a base64 string, per Go's `encoding/json` convention.
- Extra unknown top-level fields are ignored (forward compatibility).

To decode a single Part on its own (e.g. reading parts one by one from an external store):

```go
func UnmarshalPart(data []byte) (Part, error)
```

## Complete example

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/great-magician-01/callable"
)

func main() {
	// Build a conversation with a tool call by hand (normally this comes
	// from Session.History()).
	history := []callable.Message{
		callable.System("You are a pragmatic assistant."),
		callable.User("How is the weather in Beijing?", callable.Image("/tmp/screenshot.png")),
		callable.Assistant(
			callable.TextPart{Text: "Let me check."},
			callable.ToolCallPart{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Beijing"}`},
		),
		callable.ToolResults(callable.ToolResultPart{
			ToolCallID: "call_1",
			Name:       "get_weather",
			Content:    `{"temp":26,"cond":"sunny"}`,
		}),
	}

	// Persist: the whole history (including provider round-trip data)
	// survives losslessly.
	data, err := json.Marshal(history)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("history.json", data, 0o644); err != nil {
		panic(err)
	}

	// Restore.
	raw, err := os.ReadFile("history.json")
	if err != nil {
		panic(err)
	}
	var restored []callable.Message
	if err := json.Unmarshal(raw, &restored); err != nil {
		panic(err)
	}

	// Walk a message's parts with a type switch.
	for _, m := range restored {
		for _, p := range m.Parts {
			switch v := p.(type) {
			case callable.TextPart:
				fmt.Println("text:", v.Text)
			case callable.ImagePart:
				fmt.Println("image:", v.Path, v.URL)
			case callable.ThinkingPart:
				fmt.Println("thinking:", v.Text)
			case callable.ToolCallPart:
				fmt.Println("tool call:", v.Name, v.Arguments)
			case callable.ToolResultPart:
				fmt.Println("tool result:", v.Name, v.Content)
			}
		}
	}

	// Restored history can feed the next request directly; wire conversion
	// is the provider's job.
	// resp, err := client.Create(ctx, callable.NewRequest(restored...))
}
```

## Caveats

- **Do not drop thinking/tool parts**: when replaying or trimming history, removing `ThinkingPart`s from assistant messages can make the next Anthropic (thinking enabled) or Responses request fail outright or break reasoning continuity. Trim history in whole turns (assistant + paired tool messages).
- **Every tool_call needs a paired tool_result**: see ToolResultPart above. The agent loop synthesizes pairs on cancellation/timeout; when building history by hand, pairing is your responsibility.
- `Part` is a sealed interface and cannot be extended; `User`/`Assistant` panic on unknown argument types.
- `ImagePart` local files are read at send time — serialized history stores the path reference (`path`), which may be invalid after restoring on another machine. Use `ImageBytes` if history must move across machines (the bytes serialize with the JSON).
- The message model deliberately excludes provider-specific concepts (cache_control, server-side tools, ...). When such data shows up in a response it is preserved verbatim via `RawPart` / `Extra` (see "Preserving unrecognized data"); for the request side use the request-level `WithExtra` escape hatch — see [Error Handling](errors.md).
