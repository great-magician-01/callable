# Web Search

[中文](../zh/web-search.md) | **English**

callable agents can search the web out of the box. The resolution order is: **provider built-in (server-side) search > Tavily fallback tool > nothing exposed**:

1. When the endpoint has built-in search, it wins — the search runs server-side with no extra round trip in the agent loop;
2. otherwise, when a [Tavily](https://tavily.com) API key is configured, a plain `web_search` function tool is registered as the fallback;
3. when neither is available, the model never sees a search tool and behavior is identical to having the feature off.

Built-in capability is auto-sniffed from the provider's base URL — zero configuration. The whole mechanism lives at the [Agent](agent.md) level and never leaks into the unified message model.

## Quick start

The default is "auto": no option needed when the endpoint has built-in search.

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

	client := callable.NewClient(
		callable.NewOpenAIProvider(os.Getenv("GLM_API_KEY"), callable.GLMURL),
		callable.WithModel("glm-4.7"),
	)

	// No WithWebSearch: the GLM endpoint has built-in search, so it is on.
	agent := callable.NewAgent(client)

	result, err := agent.Run(ctx, callable.User("What's new in Go this week?"))
	if err != nil {
		panic(err)
	}
	fmt.Println(result.FinalText)
}
```

## Toggle and priority

```go
agent := callable.NewAgent(client)                                            // auto: on iff built-in search or a Tavily key exists
agent = callable.NewAgent(client, callable.WithWebSearch(false))              // fully off
agent = callable.NewAgent(client, callable.WithTavilyAPIKey(os.Getenv("TAVILY_API_KEY"))) // configure the fallback
```

- `WithWebSearch(enabled bool)`: the explicit switch. The default (option not given) is auto — enabled when the endpoint has built-in search or a Tavily key is configured, with nothing exposed otherwise. With an explicit `WithWebSearch(true)` and neither built-in support nor a Tavily key, **no tool is exposed either** — enabling does not conjure up a capability.
- `WithTavilyAPIKey(key string)`: configures the Tavily fallback key. It is only used when the endpoint has no built-in search; a provider's built-in search always wins over the fallback.

## Built-in search support matrix

Built-in search is auto-detected from the provider base URL. The injected wire fields differ per endpoint but are fully transparent to the caller:

| Endpoint | Detection | Injected wire field |
|---|---|---|
| Kimi / Moonshot (`KimiURL`, any host containing `moonshot`), Chat Completions | base URL | declares `{"type":"builtin_function","function":{"name":"$web_search"}}` |
| GLM / Zhipu bigmodel.cn and Z.AI (`GLMURL` / `ZAIURL`), Chat Completions | Compat dialect sniffing | appends `{"type":"web_search","web_search":{"enable":true,"search_result":true}}` to tools |
| Qwen / DashScope compatible mode (`QwenURL`), Chat Completions | Compat dialect sniffing | top-level `"enable_search": true` (not a tool entry) |
| Official Anthropic `api.anthropic.com` | base URL | appends the server tool `{"type":"web_search_20250305","name":"web_search"}` to tools |
| Official OpenAI Responses `api.openai.com` | base URL | appends `{"type":"web_search"}` to tools |
| Everything else (OpenAI Chat Completions, DeepSeek, Volcano Ark, custom endpoints, Anthropic-compatible third-party endpoints) | — | no built-in → Tavily fallback with a key, otherwise nothing is exposed |

Notes:

- **Kimi's echo protocol**: when the model calls `$web_search`, the agent echoes the call arguments back verbatim as the tool result, and the server performs the actual search on the next request. In the event stream this looks like an ordinary `ToolCallEvent` / `ToolResultEvent` pair.
- **Anthropic's server-side search**: the `server_tool_use` / `web_search_tool_result` response blocks are not mapped into the unified message — only the final text is. OpenAI Responses' `web_search_call` items instead round-trip into the history via the raw output replay.
- GLM / Qwen built-in search rides on the Compat dialect: endpoints detected as `CompatGLM` / `CompatQwen` (or given those dialect bits via `WithCompat`) get the corresponding wire fields injected — self-hosted gateways can opt in the same way.

## The Tavily fallback tool

On endpoints without built-in search, the fallback is registered as a normal function tool — the model sees and calls it exactly like one of your own tools:

- The tool is named `web_search` (the `callable.DefaultWebSearchToolName` constant).
- Arguments: `query` (required, the search query) and `max_results` (optional, 1–10, default 5).
- The result is formatted as plain text for the model: Tavily's short answer first (`Answer: ...`), then a numbered list of results with titles, URLs and content snippets.
- Each search has a 30-second timeout and honors the agent run's context cancellation. Failures (network errors, 4xx/5xx) follow the tool-error rules — they are fed back to the model as `IsError` results without breaking the agent loop (see [Tools](tools.md)).

## Direct Client usage: `Request.WebSearch`

Without an Agent, flip the request-level field instead:

```go
req := callable.NewRequest(callable.User("What's new in Go this week?"))
req.WebSearch = true
resp, err := client.Create(ctx, req)
```

`Request.WebSearch` only takes effect on endpoints with built-in search; providers without it ignore the field (and no Tavily tool is injected — the fallback is an Agent-level feature). Note that Kimi's echo protocol is handled by the Agent automatically; with a direct Client you would have to register `$web_search` yourself via `NewRawTool` and echo the arguments back as the tool result.

## Caveats

- **Billing**: built-in search is billed by the provider per search call (the Tavily fallback consumes your Tavily quota instead); see each vendor's pricing.
- **Sub-agents**: every delegation builds a fresh Agent over the sub-agent's client, and web search is resolved there again — a sub-agent that inherits the parent client gets the same built-in search; one configured with `WithSubAgentClient` follows that client's endpoint. The Tavily key is not passed down: a sub-agent whose endpoint lacks built-in search gets no `web_search` tool — inject a search tool yourself with `WithSubAgentTools` if needed.
- **Kimi + thinking**: combining Kimi's thinking mode with web search is not covered (`reasoning_content` history replay is a separate, existing behavior).
- **Name conflicts**: the fallback tool follows the normal duplicate-name rule — a user tool named `web_search` registered first shadows the built-in fallback (first registration wins, see [Tools](tools.md)).

## See also

- [Agent Loop](agent.md) — the tool loop, approval hooks, config layering
- [Tools](tools.md) — custom tools, error feedback, name conflicts
- [Sub-Agents](subagents.md) — the two-step load_agent delegation flow, client inheritance
- [Getting Started](getting-started.md) — endpoint constants and Compat dialect sniffing
