# 联网搜索

**中文** | [English](../en/web-search.md)

callable 的 Agent 可以开箱即用地联网搜索。解析顺序是：**provider 内置（服务端）搜索 > Tavily 回退工具 > 不暴露任何搜索能力**：

1. 端点支持内置搜索时优先使用——搜索在服务端执行，agent loop 无需额外往返；
2. 端点没有内置搜索、但配置了 [Tavily](https://tavily.com) API key 时，注册一个普通的 `web_search` 函数工具作为回退；
3. 两者都没有时，模型根本看不到搜索工具，行为与未开启时完全一致。

内置能力按 provider 的 BaseURL 自动嗅探，零配置；整套机制只作用于 [Agent](agent.md)，不侵入统一消息模型。

## 快速开始

默认即「自动」：端点有内置搜索就启用，无需任何选项。

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

	// 不传 WithWebSearch：GLM 端点有内置搜索，自动启用
	agent := callable.NewAgent(client)

	result, err := agent.Run(ctx, callable.User("本周 Go 语言有什么新闻？"))
	if err != nil {
		panic(err)
	}
	fmt.Println(result.FinalText)
}
```

## 开关与优先级

```go
agent := callable.NewAgent(client)                                            // auto：有内置或 Tavily key 即启用
agent = callable.NewAgent(client, callable.WithWebSearch(false))              // 彻底关闭
agent = callable.NewAgent(client, callable.WithTavilyAPIKey(os.Getenv("TAVILY_API_KEY"))) // 配置 Tavily 回退
```

- `WithWebSearch(enabled bool)`：显式开关。默认（不传）为 auto——端点有内置搜索、或配置了 Tavily key 时启用，否则不暴露任何搜索工具。显式 `WithWebSearch(true)` 时，若端点既无内置搜索也未配置 Tavily key，**同样不会暴露工具**——开启不等于凭空获得能力。
- `WithTavilyAPIKey(key string)`：配置 Tavily 回退密钥，仅当端点没有内置搜索时使用；有内置搜索时该 key 被忽略（内置优先）。

## 内置搜索支持矩阵

内置搜索按 provider 的 BaseURL 自动嗅探，各端点注入的 wire 字段不同，但对上层完全透明：

| 端点 | 嗅探方式 | 注入的 wire 字段 |
|---|---|---|
| Kimi / Moonshot（`KimiURL` 等 host 含 `moonshot` 的端点），Chat Completions | 按 BaseURL | 声明 `{"type":"builtin_function","function":{"name":"$web_search"}}` |
| GLM / 智谱 bigmodel.cn 与 Z.AI（`GLMURL` / `ZAIURL`），Chat Completions | Compat 方言嗅探 | tools 数组追加 `{"type":"web_search","web_search":{"enable":true,"search_result":true}}` |
| Qwen / DashScope 兼容模式（`QwenURL`），Chat Completions | Compat 方言嗅探 | 顶层字段 `"enable_search": true`（不是工具条目） |
| Anthropic 官方 `api.anthropic.com` | 按 BaseURL | tools 追加 server tool `{"type":"web_search_20250305","name":"web_search"}` |
| OpenAI Responses 官方 `api.openai.com` | 按 BaseURL | tools 追加 `{"type":"web_search"}` |
| 其他端点（OpenAI Chat Completions、DeepSeek、火山方舟、自定义端点、Anthropic 兼容第三方端点） | — | 无内置 → 有 Tavily key 则回退，否则不暴露 |

补充说明：

- **Kimi 的回显协议**：模型调用 `$web_search` 时，agent 把调用参数原样回显为工具结果，服务端在下一个请求里真正执行搜索。表现在事件流上是一次普通的 `ToolCallEvent` / `ToolResultEvent`。
- **Anthropic 的服务端搜索**：响应中的 `server_tool_use` / `web_search_tool_result` 块不映射进统一消息，只有最终文本；OpenAI Responses 的 `web_search_call` item 则随原始输出回放进历史。
- GLM / Qwen 的内置搜索依附于 Compat 方言：嗅探到（或经 `WithCompat` 叠加了）`CompatGLM` / `CompatQwen` 方言的端点会注入对应的内置搜索字段——自建网关也可以借此获得该能力。

## Tavily 回退工具

端点没有内置搜索时，回退工具以普通函数工具的形式注册，模型看到的与调用方式都和自定义工具一致：

- 工具名为 `web_search`（常量 `callable.DefaultWebSearchToolName`）。
- 参数：`query`（必填，搜索关键词）、`max_results`（可选，1–10，默认 5）。
- 结果格式化为纯文本回传给模型：先是 Tavily 的简短回答（`Answer: ...`），随后是带标题 / URL / 摘要的编号结果列表。
- 单次搜索 30 秒超时，并响应 agent run 的 context 取消；搜索失败（网络错误、4xx/5xx）按工具错误处理——以 `IsError` 结果回传模型，不中断 agent loop（见[工具](tools.md)的错误回传规则）。

## 直接使用 Client：`Request.WebSearch`

不走 Agent 时可以用请求级字段直接开启：

```go
req := callable.NewRequest(callable.User("本周 Go 语言有什么新闻？"))
req.WebSearch = true
resp, err := client.Create(ctx, req)
```

`Request.WebSearch` 只在端点有内置搜索时生效，没有内置搜索的 provider 会忽略它（也不会注入 Tavily 工具——回退是 Agent 层的能力）。注意 Kimi 的回显协议由 Agent 自动处理；直连 Client 时需要自行用 `NewRawTool` 注册 `$web_search` 并把参数回显为工具结果。

## 注意事项

- **计费**：内置搜索由 provider 按搜索调用次数计费（Tavily 回退则消耗 Tavily 额度），具体费率以各厂商为准。
- **子代理**：每次委派都会基于子代理的 client 构建一个全新的 Agent，联网搜索在新 Agent 上按端点重新解析——继承父 client 的子代理获得同样的内置搜索；用 `WithSubAgentClient` 更换 client 的，以新 client 的端点为准。Tavily key 不会传给子代理：端点无内置搜索时子代理不会获得 `web_search` 工具，需要的话用 `WithSubAgentTools` 自行注入一个搜索工具。
- **Kimi 思考模式**：Kimi 的思考模型与联网搜索的组合未覆盖验证（`reasoning_content` 的历史回传是独立的既有行为）。
- **工具名冲突**：回退工具遵循普通的重名规则——用户先注册同名 `web_search` 工具时，内置回退被忽略（先注册者生效，见[工具](tools.md)）。

## 相关文档

- [Agent 循环](agent.md) — 工具循环、审批钩子、配置层级
- [工具](tools.md) — 自定义工具、错误回传、工具名冲突
- [子代理](subagents.md) — load_agent 两步委派、子代理的 client 继承
- [快速开始](getting-started.md) — 端点常量与 Compat 方言嗅探
