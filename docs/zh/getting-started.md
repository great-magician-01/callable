# 快速开始

**中文** | [English](../en/getting-started.md)

本文介绍如何安装 callable、创建 Client 与三种 Provider、使用内置端点常量与方言自动嗅探，并完成第一次 `Create` / `Stream` 调用。

callable 是一个统一的 Go LLM 调用库：一套 API 同时支持 **OpenAI Chat Completions**、**OpenAI Responses**、**Anthropic Messages** 三种 wire 格式，并在其上内置了完整的 agent loop。整个库分三层：

```
Agent / Session（自动工具循环 + 会话）   → 见 agent.md / session.md
Client（Create / Stream / 默认值填充）    → 本文
Provider（wire 格式适配层）              → 本文
```

## 安装

要求 Go 1.21 或更高版本：

```bash
go get github.com/great-magician-01/callable
```

模块路径与包名：

```go
import callable "github.com/great-magician-01/callable"
```

- 根包 `callable` 是唯一的对外入口（`callable.go` 全量 re-export `internal/` 下的各内部包），一次 import 拿到全部能力。
- 网络层（HTTP、SSE）全部用标准库实现；唯一第三方依赖是 `invopop/jsonschema`（用于工具参数的 JSON Schema 反射）。

## 创建 Client 与 Provider

最简路径是三个便捷构造函数——把「Provider + 默认模型」折叠成一行：

```go
client := callable.NewAnthropicClient(apiKey, callable.AnthropicURL, "claude-sonnet-5")
```

| 便捷构造 | 等价展开 |
|---|---|
| `NewOpenAIClient(apiKey, baseURL, model string, opts ...ClientOption) *Client` | `NewClient(NewOpenAIProvider(apiKey, baseURL), WithModel(model), opts...)` |
| `NewOpenAIResponsesClient(apiKey, baseURL, model string, opts ...ClientOption) *Client` | `NewClient(NewOpenAIResponsesProvider(apiKey, baseURL), WithModel(model), opts...)` |
| `NewAnthropicClient(apiKey, baseURL, model string, opts ...ClientOption) *Client` | `NewClient(NewAnthropicProvider(apiKey, baseURL), WithModel(model), opts...)` |

需要 `ProviderOption`（如 `WithRetries`、`WithHTTPClient`、`WithCompat`）时回到两步构造，典型装配顺序是：**Provider（选 wire 格式和端点）→ Client（填默认模型等参数）**：

```go
client := callable.NewClient(
    callable.NewAnthropicProvider(apiKey, callable.AnthropicURL),
    callable.WithModel("claude-sonnet-5"),
)
```

库**不读取任何环境变量**（如 `OPENAI_API_KEY`）：API key 和端点地址都必须显式传入。需要环境变量时自行读取（可参考 `examples/quickstart/main.go` 的 `firstNonEmptyEnv`）。

### 三种 Provider 构造函数

| 构造函数 | wire 格式 | 请求路径 |
|---|---|---|
| `NewOpenAIProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIProvider` | OpenAI Chat Completions | `POST {baseURL}/chat/completions` |
| `NewOpenAIResponsesProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIResponsesProvider` | OpenAI Responses | `POST {baseURL}/responses` |
| `NewAnthropicProvider(apiKey, baseURL string, opts ...ProviderOption) *AnthropicProvider` | Anthropic Messages | `POST {baseURL}/v1/messages` |

```go
callable.NewOpenAIProvider(apiKey, callable.OpenAIURL)           // Chat Completions
callable.NewOpenAIResponsesProvider(apiKey, callable.OpenAIURL)  // Responses
callable.NewAnthropicProvider(apiKey, callable.AnthropicURL)     // Anthropic
```

行为细节：

- `baseURL` 是含版本前缀的 API 根地址；末尾的 `/` 会被自动去掉，保证与端点路径正确拼接。
- `NewAnthropicProvider` 容忍 `baseURL` 已以 `/v1` 结尾（此时请求路径为 `/messages` 而不是 `/v1/messages`），不会拼出 `/v1/v1/messages`。
- `NewOpenAIProvider` 对任何 OpenAI 兼容端点都可用（GLM、DeepSeek、Qwen、vLLM、Ollama……），不限于官方 OpenAI。
- 官方 OpenAI 端点（host 为 `api.openai.com`）会自动使用新版参数名（`max_completion_tokens`、`stream_options.include_usage`）。

## 内置端点常量

常见厂商的 baseURL 已内置为常量，直接传给构造函数即可；未内置的端点照旧传实际 URL 字符串。

| 常量 | 值 | 适用构造函数 |
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
callable.NewAnthropicProvider(key, callable.DeepSeekAnthropicURL) // DeepSeek 的 Anthropic 兼容端点
callable.NewOpenAIProvider(key, callable.QwenURL)                 // Qwen，方言自动嗅探
```

注意：`DeepSeekURL` 不含 `/v1`（DeepSeek 官方文档的规范 base URL；虽然它也接受 `/v1` 后缀，但那是 OpenAI SDK 的约定）。同理 GLM 的 `GLMURL` 已含 `/v4` 前缀。

## OpenAI 兼容端点与 Compat 方言

第三方 OpenAI 兼容端点对**思考控制**等能力使用了非标准字段。callable 用 `Compat` 位掩码表示这些方言，并**按 baseURL 的 host 自动嗅探**：

| 常量 | 触发嗅探的 host | 端点方言（思考字段） |
|---|---|---|
| `callable.CompatNone` | （默认，无匹配） | 标准 OpenAI 字段（`reasoning_effort`） |
| `callable.CompatGLM` | 含 `bigmodel.cn`、`zhipuai`，或为 `z.ai` / `*.z.ai` | `thinking:{type:"enabled"}` + `reasoning_effort`（medium 映射为 high） |
| `callable.CompatQwen` | 含 `dashscope` | `enable_thinking:true` + `thinking_budget` |
| `callable.CompatDeepSeek` | 含 `deepseek` | `thinking:{type:"enabled"}` + `reasoning_effort`；输出解析 `reasoning_content` |
| `callable.CompatArk` | 含 `volces.com` | `thinking:{type:"enabled"}` + `reasoning_effort`（原样透传） |

嗅探只影响请求里思考相关字段的写法（以及响应中思考内容的解析），不影响其它请求结构。思考模式各字段的完整映射见[思考模式](thinking.md)。

### 用 WithCompat 手动覆盖

嗅探对内置常量和同域名的自建网关都有效；如果代理改写路径、或者端点行为与嗅探结果不符，用 `WithCompat` 完全替换嗅探结果：

```go
// 自建网关代理 GLM，但 host 不匹配 bigmodel.cn：
callable.NewOpenAIProvider(key, "https://llm.example.com/glm",
    callable.WithCompat(callable.CompatGLM))

// 用 DashScope 的 OpenAI 兼容模式，但不想发 enable_thinking 字段：
callable.NewOpenAIProvider(key, callable.QwenURL,
    callable.WithCompat(callable.CompatNone))
```

注意 `WithCompat` 是**整体覆盖**而非追加位：它会丢弃自动嗅探结果，把方言设为你传入的值。

## Provider 选项

所有 Provider 构造函数都接受 `opts ...ProviderOption`：

| 选项 | 签名 | 说明 |
|---|---|---|
| `WithHTTPClient` | `WithHTTPClient(client *http.Client) ProviderOption` | 注入自定义 `*http.Client`（代理、TLS、超时等）。传 `nil` 则忽略。 |
| `WithHeader` | `WithHeader(key, value string) ProviderOption` | 给每个 provider 请求附加 header。在认证头**之后**应用，因此可以用它覆盖 `Authorization` 等默认头；同名 key 会被请求级 `Request.WithHeader` 覆盖。 |
| `WithRetries` | `WithRetries(n int) ProviderOption` | 瞬时失败（网络错误、429、5xx）的重试次数。默认 3；传 0 关闭；负数按 0 处理。 |
| `WithRetryBackoff` | `WithRetryBackoff(delays ...time.Duration) ProviderOption` | 替换默认重试等待表（3s/10s/30s）：`delays[i]` 是第 i+1 次重试前的等待，超出次数复用最后一个。与 `WithRetries` 配合。 |
| `WithCompat` | `WithCompat(c Compat) ProviderOption` | 覆盖自动嗅探的端点方言（见上节）。 |

行为细节：

- **重试退避**：默认固定时间表——第一次重试前等 3s，第二次前等 10s，第三次前等 30s；超过时间表的重试沿用最后一个间隔（30s）。可用 `WithRetryBackoff` 整体替换该表。重试等待可被 context 取消打断。
- **可重试状态码**：仅 429 与 5xx；4xx（如 400、401、403）不重试，直接返回 `*callable.APIError`。详见[错误处理](errors.md)。
- **默认 HTTP 客户端无全局超时**：长流式响应不能被掐断，取消/超时一律用 `context.Context` 控制。需要超时就通过 `WithHTTPClient` 注入带 `Timeout` 的 client，或（更推荐）用 `context.WithTimeout`。
- **默认 baseURL 不存在**：库不带任何默认端点，`baseURL` 参数必填。

## Client 选项

```go
func NewClient(provider Provider, opts ...ClientOption) *Client
```

| 选项 | 签名 | 说明 |
|---|---|---|
| `WithModel` | `WithModel(model string) ClientOption` | 默认模型 ID |
| `WithMaxTokens` | `WithMaxTokens(n int) ClientOption` | 默认最大输出 token 数 |
| `WithTemperature` | `WithTemperature(v float64) ClientOption` | 默认采样温度 |
| `WithTopP` | `WithTopP(v float64) ClientOption` | 默认核采样（nucleus sampling）概率质量，见[结构化输出与采样参数](structured-output.md) |
| `WithStopSequences` | `WithStopSequences(seq ...string) ClientOption` | 默认停止序列（OpenAI Responses 不支持，会被忽略），同上 |
| `WithResponseFormat` | `WithResponseFormat(f ResponseFormat) ClientOption` | 默认输出格式约束（结构化输出），同上 |
| `WithClientHeader` | `WithClientHeader(key, value string) ClientOption` | 给该 client 的每个请求附加 HTTP 头（含 Agent loop 的内部调用）；同名 key 请求级 `Request.WithHeader` 优先 |
| `WithRequestHook` | `WithRequestHook(hooks ...RequestHook) ClientOption` | 注册请求钩子，每次请求发送前按序调用，见下文「请求/响应钩子」 |
| `WithResponseHook` | `WithResponseHook(hooks ...ResponseHook) ClientOption` | 注册响应钩子，每次调用结束后按序调用，见下文「请求/响应钩子」 |

除钩子外这些都是**默认值**：只有当 `Request` 自身没有设置对应字段时才生效（`Request.Model == ""`、`MaxTokens == 0`、`Temperature == nil`、`TopP == nil`、`Stop == nil`、`Format == nil` 时才填充）。请求级别的 `WithModel` / `WithMaxTokens` / `WithTemperature` 等会覆盖 Client 默认值：

```go
req := callable.NewRequest(callable.User("...")).
    WithModel("gpt-5-mini").   // 覆盖 client 的默认模型
    WithTemperature(0.2)
```

填充默认值时 Client 会**复制**请求而不修改原对象，同一个 `*Request` 可以安全地复用。

### 请求/响应钩子

Client 级的观测钩子，适合日志、分布式 trace 与 token 成本统计：

```go
type RequestHook  func(ctx context.Context, req *Request)
type ResponseHook func(ctx context.Context, req *Request, resp *Response, err error)
```

```go
client := callable.NewAnthropicClient(apiKey, callable.AnthropicURL, "claude-sonnet-5",
    callable.WithRequestHook(func(ctx context.Context, req *callable.Request) {
        log.Printf("→ %s (%d 条消息)", req.Model, len(req.Messages))
    }),
    callable.WithResponseHook(func(ctx context.Context, req *callable.Request, resp *callable.Response, err error) {
        if err == nil {
            log.Printf("← 输入 %d / 输出 %d tokens", resp.Usage.InputTokens, resp.Usage.OutputTokens)
        }
    }),
)
```

- `RequestHook` 在请求发送前触发，看到的是应用了 Client 默认值之后的请求。
- `ResponseHook` 在调用结束后触发，`resp` / `err` 与 provider 返回的完全一致（`Stream` 是聚合后的最终响应）。
- 两个选项都可传入多个钩子，按注册顺序执行；钩子不应修改请求或响应。
- Agent 的每次内部模型调用同样经过钩子——loop 跑了 N 轮就触发 N 次。

### 列出可用模型（GET /models）

`Client.ListModels` 封装了 provider 的模型列表接口，返回统一的 `ModelInfo`：

```go
models, err := client.ListModels(ctx)
if err != nil {
    log.Fatal(err)
}
for _, m := range models {
    fmt.Println(m.ID, m.DisplayName, m.OwnedBy, m.Created)
}
```

- OpenAI 兼容端点请求 `GET {baseURL}/models`；Anthropic 请求 `GET {baseURL}/v1/models`，分页自动取全。
- `DisplayName` 仅 Anthropic 返回，`OwnedBy` 仅 OpenAI 兼容端点返回，`Created` 在端点提供时解析为 `time.Time`。
- 三种内置 provider 都实现了 `ModelLister` 接口；自定义 provider 未实现时 `ListModels` 返回错误。这不是模型调用，不触发请求/响应钩子。

## 最小调用示例

### Create（非流式）

```go
resp, err := client.Create(ctx, callable.NewRequest(callable.User("用一句话解释什么是闭包")))
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.Text)   // 拼接后的正文文本
fmt.Println(resp.Usage)  // token 用量
```

### Stream（流式）

```go
_, err := client.Stream(ctx, callable.NewRequest(callable.User("讲个故事")), func(ev callable.Event) {
    if d, ok := ev.(callable.TextDeltaEvent); ok {
        fmt.Print(d.Delta)
    }
})
```

`Stream` 把每个流式事件交给回调，并在流结束后返回组装好的完整 `*Response`。完整事件清单见[流式事件](streaming.md)。

### 完整可运行程序

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

    // 端点必须显式传入；这里从环境变量读 key，库本身不读环境变量
    client := callable.NewOpenAIClient(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL, "gpt-5",
        callable.WithMaxTokens(2048),
    )

    // 非流式：拿到完整 Response
    resp, err := client.Create(ctx, callable.NewRequest(callable.User("用一句话解释什么是递归")))
    if err != nil {
        fmt.Fprintln(os.Stderr, "Create:", err)
        os.Exit(1)
    }
    fmt.Println(resp.Text)

    // 流式：逐事件回调
    _, err = client.Stream(ctx, callable.NewRequest(callable.User("用三句话介绍 goroutine")), func(ev callable.Event) {
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

Anthropic 版只需换一个便捷构造函数：

```go
callable.NewAnthropicClient(os.Getenv("ANTHROPIC_API_KEY"), callable.AnthropicURL, "claude-sonnet-5",
    callable.WithMaxTokens(2048),
)
```

可运行示例见仓库 `examples/quickstart`（按环境变量自动选 provider）与 `examples/deepseek`（三种 wire 格式的全场景真机测试）。

## 注意事项与坑

- **API key 与环境变量**：库不读环境变量，也不内置默认端点；key 和 baseURL 都由调用方显式提供。
- **超时**：默认 `http.Client` 没有超时，务必用 `context.WithTimeout` / `context.WithCancel` 管理调用生命周期。取消是优雅的：上游连接立即关闭，流式中途取消会返回**非 nil 的部分结果**且错误满足 `errors.Is(err, context.Canceled)`。
- **`Create` / `Stream` 是单次调用**：不会执行工具循环。要让模型自动跑「模型 → 工具 → 模型 → …」循环，用 [Agent 循环](agent.md)（`NewAgent` + `Run` / `RunStream`）。
- **多轮历史要自己传**：`Create` / `Stream` 无状态，多轮对话需要把历史消息一起放进 `NewRequest`，或用 [Session](session.md) 自动维护。
- **自定义请求头（穿透）分三级**：Provider 级 `WithHeader`（该 provider 的每个请求）→ Client 级 `WithClientHeader`（该 client 的每个请求，含 Agent 内部调用）→ 请求级 `Request.WithHeader`（单次调用，如按调用透传 tracing id）。三者同名 key 后者优先，且都在认证头之后应用——同名 key 会覆盖 `Authorization` / `x-api-key`，通常用于网关特殊头，注意不要误伤认证。

## 下一步

- [消息模型](messages.md) — `Message` / `Part` 与构造辅助函数
- [结构化输出与采样参数](structured-output.md) — JSON 模式 / JSON Schema、`DecodeJSON`、`top_p` 与停止序列
- [流式事件](streaming.md) — 完整的流式事件清单
- [Agent 循环](agent.md) — 自动工具循环
- [思考模式](thinking.md) — 各端点思考字段映射
- [错误处理](errors.md) — `APIError`、重试与取消语义
