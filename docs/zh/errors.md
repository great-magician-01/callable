# 错误处理、重试与取消

**中文** | [English](../en/errors.md)

callable 把错误分成三类，处理方式各不相同：

- **API / 传输错误**：`*callable.APIError`，可被自动重试（见下文）
- **Agent 循环达到轮次上限**：`*callable.MaxTurnsError`，附带部分结果
- **工具执行错误**：不视为失败，错误内容回传给模型继续循环

所有调用都由 `context.Context` 控制取消与超时，取消行为是**优雅**的（见「取消与超时」一节）。

## APIError：结构化的 Provider 错误

Provider 返回非 2xx 响应，或发生传输层（网络）故障时，错误类型为 `*callable.APIError`：

```go
type APIError struct {
    // 产生错误的 provider 名称："openai" / "openai-responses" / "anthropic"
    Provider   string
    // HTTP 状态码；传输层故障（连接被拒、DNS 失败等）为 0
    StatusCode int
    // 各家的错误类型/代码，尽力从错误响应体中提取（可能为空）
    Type       string
    // 人类可读的错误消息
    Message    string
    // 原始响应体，保留用于诊断
    Body       string
}
```

配套方法：

```go
func (e *APIError) Error() string      // "callable: openai API error (status 429, type \"rate_limit_error\"): ..."
func (e *APIError) IsRetryable() bool  // 是否属于瞬时错误，值得重试
```

`IsRetryable()` 的判定规则与内置重试策略完全一致：

- `StatusCode == 0`（传输层故障）→ 可重试
- `StatusCode == 429`（限流）→ 可重试
- `StatusCode >= 500`（服务端错误）→ 可重试
- 其余（400/401/403/404 等）→ 不可重试

`Type` / `Message` 的提取覆盖三家常见的错误载荷形态：OpenAI 风格的 `{"error": {"type", "code", "message"}}`，以及 OpenAI Responses 在顶层返回的 `{"type", "code", "message", "param"}`。解析失败时 `Message` 回退为响应体原文（截掉首尾空白），再兜底为 `"unexpected status code %d"`，因此 `Message` 保证非空。需要完整细节时读 `Body`。

用 `errors.As` 分支处理：

```go
resp, err := client.Create(ctx, req)
if err != nil {
    var apiErr *callable.APIError
    if errors.As(err, &apiErr) {
        switch {
        case apiErr.StatusCode == 401:
            // 密钥无效，重试无意义
        case apiErr.StatusCode == 429:
            // 已被自动重试过仍然限流：降级模型或稍后再试
        case apiErr.IsRetryable():
            // 网络故障 / 5xx，且自动重试已耗尽
        }
        log.Printf("provider=%s status=%d type=%s body=%s",
            apiErr.Provider, apiErr.StatusCode, apiErr.Type, apiErr.Body)
    }
}
```

## 自动重试

Client 层对瞬时故障自动重试，策略固定且可预测：

| 配置项 | 行为 |
|---|---|
| 触发条件 | 网络错误（连接失败等）、HTTP 429、HTTP 5xx |
| 默认次数 | 3 次重试（共最多 4 次请求） |
| 等待节奏 | 第 1 次重试前等 3s，第 2 次前等 10s，第 3 次前等 30s；可用 `WithRetryBackoff` 整体替换 |
| `WithRetries(n)` | 配置重试次数；负数按 0 处理 |
| `WithRetries(0)` | 关闭重试，任何错误立即返回 |
| 超出节奏表 | 第 4 次及以后的重试都等 30s |

```go
client := callable.NewClient(
    callable.NewOpenAIProvider(apiKey, callable.OpenAIURL,
        callable.WithRetries(5), // 重试 5 次，等待 3s/10s/30s/30s/30s
    ),
    callable.WithModel("gpt-5"),
)
```

`WithRetryBackoff` 用自定义时间表整体替换默认的 3s/10s/30s：`delays[i]` 是第 i+1 次重试前的等待，重试次数超出表长时复用最后一个值：

```go
callable.NewOpenAIProvider(apiKey, callable.OpenAIURL,
    callable.WithRetries(4),
    callable.WithRetryBackoff(500*time.Millisecond, 2*time.Second, 5*time.Second),
    // 4 次重试的等待依次为 0.5s / 2s / 5s / 5s
)
```

几个边界细节：

- **退避可被 context 打断**：等待期间取消 ctx，立即返回 ctx 错误，不会睡满整个退避时间。
- **`context.Canceled` / `context.DeadlineExceeded` 永不重试**：用户主动取消或超时不是瞬时故障，直接透传。
- **不可重试的状态码不重试**：400/401/403/404 等第一次失败就以 `*APIError` 返回。
- **重试的瞬时错误最终也报错**：重试次数耗尽后，返回最后一次失败的 `*APIError`（429/5xx 的场景下 `Body` 可能为空，因为重试路径不读取失败响应体）。
- 重试在 HTTP 层生效，`Create` 与 `Stream`（以及 Agent 的 `Run` / `RunStream` / Session 的 `Ask*`）都受益。

## MaxTurnsError：轮次上限

[Agent 循环](agent.md)在 `WithMaxTurns(n)`（默认 25）轮内没有产生最终回答时，`Run` / `RunStream` 返回 `*callable.MaxTurnsError`：

```go
type MaxTurnsError struct {
    Turns   int          // 实际配置的轮次上限
    Partial *AgentResult // 已产生的部分结果，保证非 nil
}
```

`Partial` 包含截至中断时的完整轨迹（`Messages`）、已生成文本（`FinalText`）、累计 token 用量（`Usage`）和已执行的轮数（`Turns`），且 `StopReason` 为 `callable.AgentMaxTurns`。可以从中提取阶段性结论，或者把 `Partial.Messages` 作为历史继续跑（例如交给 [Session](session.md) 续聊）：

```go
result, err := agent.Run(ctx, callable.User("把这份 500 页报告整理成摘要"))
var mtErr *callable.MaxTurnsError
if errors.As(err, &mtErr) {
    // 已生成的中间内容不丢
    fmt.Println("已跑", mtErr.Partial.Turns, "轮，当前文本：", mtErr.Partial.FinalText)
    fmt.Println("累计 token：", mtErr.Partial.Usage)
}
```

注意区分：循环上限意味着模型一直在要工具（或一直没给出终止回答），通常说明任务拆分有问题、工具结果不满足模型预期，或者 max turns 确实设小了。

## 工具执行错误：回传模型，不中断循环

[工具](tools.md) handler 返回的 error **不会**导致 agent 运行失败。它会被包装成 `IsError = true` 的工具结果回传给模型，模型可以看到失败原因并自行决定重试、换参数或换路：

```go
search := callable.NewTool("web_search", "搜索互联网",
    func(ctx context.Context, args SearchArgs) (any, error) {
        hits, err := doSearch(ctx, args.Query)
        if err != nil {
            return nil, err // 变成 IsError 结果回传给模型，loop 继续
        }
        return hits, nil
    })
```

同样回传为 `IsError` 结果、不中断循环的情况还有：

- 模型传入的参数 JSON 无法解析为工具的参数 struct（错误信息里附带期望的 schema，便于模型自我纠正）
- 模型调用了不存在的工具名（`unknown tool`，附带可用工具列表）
- 审批钩子 `Deny(reason)` 否决（见 [Agent 循环](agent.md)）

只有两种情况会让运行**真正中断**：审批钩子 `WithToolCallHook` 本身返回 error（视为程序 bug，原样上抛）；以及上游 API 错误 / context 取消。

## 取消与超时：优雅停止的五条保证

所有入口（`client.Create` / `client.Stream` / `agent.Run` / `agent.RunStream` / `sess.Ask` / `sess.AskStream`）都接受 `context.Context` 作为第一个参数。取消或超时触发时，行为满足五条保证：

1. **上游同时停止**。进行中的 HTTP/SSE 连接被立即关闭，服务端感知断连后停止生成——之后的 token 不再计费。
2. **不再发起新请求**。agent loop 在每轮开始前检查 `ctx.Err()`，取消后不会开启新的模型调用；重试退避同样会立即中断。
3. **部分结果不丢**。流式中途取消时，`Stream` / `RunStream` 返回**非 nil 的部分结果**：已生成的文本进 `FinalText`、已累积的用量进 `Usage`；未组装完成的 assistant 消息不会混进 `Messages`。
4. **工具配对完整**。取消时已经发出但尚未执行的工具调用，会合成一条 `IsError` 结果（内容为 `"tool execution skipped: ..."`），保证每个 tool_call 都有配对的 tool_result——部分轨迹仍然是各家 API 可接受的有效会话。
5. **Session 不被污染**。[Session](session.md) 只在运行**成功**时写入历史；被取消/失败的运行不留下任何消息，下一次 `Ask` 不会被半截的 assistant 工具调用消息破坏（各家 API 会拒绝这种不配对的历史）。

典型用法：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := agent.RunStream(ctx, onEvent, callable.User("写一份详细的市场分析"))
switch {
case err == nil:
    fmt.Println(result.FinalText)
case errors.Is(err, context.DeadlineExceeded):
    // result 非 nil：部分文本与用量都在
    log.Printf("超时，已获得 %d 字符的部分结果", len(result.FinalText))
case errors.Is(err, context.Canceled):
    // 用户主动取消
default:
    // APIError 等其他错误
}
```

## 工具函数需自行响应取消

执行中的工具 handler 收到的就是同一个 ctx，但 Go **无法强制杀死一个无视 ctx 的 goroutine**。库只能保证在工具返回后感知取消；工具内部的耗时操作——数据库查询、子 HTTP 请求、文件 IO——必须自己响应取消：

```go
query := callable.NewTool("db_query", "查询数据库",
    func(ctx context.Context, args QueryArgs) (any, error) {
        // 好：把 ctx 传进支持取消的操作
        rows, err := db.QueryContext(ctx, args.SQL)
        if err != nil {
            return nil, err
        }
        defer rows.Close()
        // ...
        return nil, nil
    })
```

如果一个工具 goroutine 永远不返回，取消请求会等到它返回为止；开启 `WithParallelToolExecution(true)` 时同理，loop 会等待所有并行工具收尾。因此工具实现里避免无 ctx 的阻塞调用（裸 `http.Get`、无超时的 channel 接收等）。

## 请求级逃生舱：WithExtra

库尚未原生支持的 provider 参数（如 OpenAI 的 `parallel_tool_calls`、Anthropic 的 `top_k`），可以用 `WithExtra` 直接合并进请求体顶层：

```go
resp, err := client.Create(ctx,
    callable.NewRequest(callable.User("你好")).
        WithExtra("top_k", 40).
        WithExtra("metadata", map[string]any{"user_id": "u-123"}))
```

`WithExtra(key, value)` 在请求被序列化为各家 wire 格式之后，把键值对覆盖合并到 JSON 顶层——也就是说它**能覆盖库自己设置的字段**（比如 `max_tokens`）。这是有意的逃生舱设计：能力强大，但传错键名或类型不会在编译期报错，而是直达服务端（通常表现为 400 `*APIError`，其 `Body` 里有 provider 的具体抱怨）。只在没有对应的一等 API 时使用。

某个中转站/网关要求**每个请求**都带方言参数时，用 Client 级的 `WithExtra` 设默认值，请求级同名字段优先：

```go
client := callable.NewClient(
    callable.NewOpenAIProvider(apiKey, gatewayURL),
    callable.WithModel("..."),
    callable.WithExtra("gateway_flag", true), // 每个请求都带上
)
```

网关要求的自定义 HTTP 头也分三级：Provider 级 `WithHeader` 与该 client 级的 `WithClientHeader` 适合「每个请求都带」的静态头（见「创建 Client 与 Provider」）；需要按调用穿透的头（比如每次调用不同的 tracing id、租户标签）用请求级 `Request.WithHeader`：

```go
resp, err := client.Create(ctx,
    callable.NewRequest(callable.User("你好")).
        WithHeader("X-Request-Id", requestID)) // 仅本次调用带上
```

三级头同名 key 请求级优先，且都在认证头之后应用（同名 key 会覆盖 `Authorization` / `x-api-key`），注意不要误伤认证。

## 完整示例

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
            callable.WithRetries(3), // 网络错误/429/5xx 最多重试 3 次
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
        // 取消/超时：部分结果不丢
        log.Printf("中断，已生成 %d 字符", len(result.FinalText))

    case func() bool { var e *callable.MaxTurnsError; return errors.As(err, &e) }():
        var mtErr *callable.MaxTurnsError
        errors.As(err, &mtErr)
        log.Printf("达到 %d 轮上限，部分结果可用", mtErr.Turns)

    default:
        var apiErr *callable.APIError
        if errors.As(err, &apiErr) {
            log.Printf("API 错误 provider=%s status=%d type=%s: %s",
                apiErr.Provider, apiErr.StatusCode, apiErr.Type, apiErr.Message)
        } else {
            log.Printf("其他错误: %v", err)
        }
    }
}
```

## 相关文档

- [Agent 循环](agent.md)：loop 结构、审批钩子、max turns
- [工具](tools.md)：工具定义、错误回传的 wire 格式
- [多轮会话](session.md)：历史写入时机与持久化
- [流式事件](streaming.md)：取消中途的事件流形态
- [快速开始](getting-started.md)：Client 与 Provider 的构造选项
