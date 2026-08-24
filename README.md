# callable

统一的 Go LLM 调用库：一套 API 同时支持 **OpenAI（Chat Completions / Responses）** 与 **Anthropic** 格式，内置完整的 **agent loop**（工具调用循环、skill 渐进式披露、思考模式、图片输入、流式输出）。

```
┌─────────────────────────────────────────────┐
│  Agent / Session（自动工具循环 + 会话）      │
├─────────────────────────────────────────────┤
│  Client（Create / Stream / 重试）           │
├─────────────────────────────────────────────┤
│  Provider 适配层（OpenAI / Responses /      │
│  Anthropic，含国产兼容端点思考字段）        │
├─────────────────────────────────────────────┤
│  统一消息模型（text / image / thinking /    │
│  tool_call / tool_result）                  │
└─────────────────────────────────────────────┘
```

## 特性

- **三种 wire 格式**：OpenAI Chat Completions、OpenAI Responses、Anthropic Messages，切换只改一行构造代码，消息历史跨 provider 通用
- **Agent loop**：模型 → 工具执行 → 模型 → … 自动循环直到最终回答；支持审批钩子（放行/否决/改参）、并行工具执行、max turns 保护
- **工具调用**：参数 struct 反射生成 JSON Schema（`invopop/jsonschema`），handler 即执行体；工具错误自动回传给模型而不是中断循环
- **多轮对话**：内置 `Session` 维护历史，thinking 块 / 工具轨迹完整保留并正确回传（Anthropic signature、Responses reasoning item、DeepSeek/GLM reasoning_content）
- **流式**：统一的 `ThinkingDelta / TextDelta / ToolCallDelta / ToolResult / Turn*` 事件流，agent loop 全程可见
- **思考模式**：统一的 `Effort`（low/medium/high）映射到各家原生字段；自动适配 GLM/智谱、火山方舟、Qwen、DeepSeek 等国产端点的非标思考字段（按 BaseURL 自动嗅探）
- **Skill 渐进式披露**：system prompt 只注入 name/description 索引，模型按需通过内置 `read_skill` 工具加载全文；读取钩子可改写内容
- **图片输入**：传本地路径 / URL / 字节，发送时按目标 provider 自动转换格式（base64 source / data URL）
- **零依赖网络层**：HTTP 重试（429/5xx 指数退避）、SSE 解析均为标准库实现；唯一第三方依赖是 `invopop/jsonschema`

## 安装

```bash
go get github.com/great-magician-01/callable
```

## 快速开始

```go
client := callable.NewClient(
    // 第二个参数是端点地址，必须显式传入（本库不内置任何默认端点）
    callable.NewAnthropicProvider(apiKey, "https://api.anthropic.com"), // 或 NewOpenAIProvider / NewOpenAIResponsesProvider
    callable.WithModel("claude-sonnet-5"),
)

// 流式单次调用
err := client.Stream(ctx, callable.NewRequest(callable.User("讲个故事")), func(ev callable.Event) {
    if d, ok := ev.(callable.TextDeltaEvent); ok {
        fmt.Print(d.Delta)
    }
})
```

## Agent：工具调用循环

```go
type WeatherArgs struct {
    City string `json:"city" jsonschema:"description=城市名称，如：北京"`
    Unit string `json:"unit,omitempty" jsonschema:"description=温度单位,enum=celsius,enum=fahrenheit"`
}

weather := callable.NewTool("get_weather", "查询指定城市的实时天气",
    func(ctx context.Context, args WeatherArgs) (any, error) {
        return fmt.Sprintf("%s 当前 26°C，晴", args.City), nil
    })

agent := callable.NewAgent(client,
    callable.WithSystemPrompt("你是一个务实的助手"),
    callable.WithTools(weather),
    callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
    callable.WithMaxTurns(25),
    // 可选：执行前审批钩子
    callable.WithToolCallHook(func(ctx context.Context, call callable.ToolCall) (callable.ToolDecision, error) {
        return callable.Approve(), nil // 或 Deny("原因") / ReplaceArgs(`{"city":"上海"}`)
    }),
)

// Run 内部自动执行：模型 -> 工具 -> 模型 -> ... 直到最终回答
result, err := agent.Run(ctx, callable.User("北京天气怎么样？"))
// result.FinalText / result.Messages（完整轨迹）/ result.Usage（累计）/ result.Turns
```

### 流式运行（全程可见）

```go
result, err := agent.RunStream(ctx, func(ev callable.Event) {
    switch e := ev.(type) {
    case callable.ThinkingDeltaEvent: // 思考增量
    case callable.TextDeltaEvent:     fmt.Print(e.Delta) // 正文增量
    case callable.ToolCallEvent:      // 工具即将执行
    case callable.ToolResultEvent:    // 工具执行完成
    case callable.TurnStartEvent:     // 第 e.Turn 轮开始
    }
}, callable.User("北京天气怎么样？", callable.Image("/tmp/截图.png")))
```

### 多轮会话

```go
sess := agent.Session()
sess.Ask(ctx, callable.User("上海呢？"))            // 历史自动维护
sess.AskStream(ctx, handler, callable.User("广州呢？"))
sess.History()          // []Message，可 JSON 序列化持久化
sess.SetHistory(saved)  // 恢复
```

## Skill：渐进式披露

```go
skill := callable.NewSkill("pdf-export", "把数据导出为 PDF 文件",
    "# 完整指令…（只有模型调用 read_skill 时才会读取）")

agent := callable.NewAgent(client,
    callable.WithSkills(skill),
    // 读取钩子：返回给模型前可改写内容（追加运行时上下文、懒加载子文件、审计）
    callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
        return content + "\n\n当前时间: " + time.Now().Format(time.RFC3339), nil
    }),
)
```

system prompt 中只出现索引：

```
<available_skills>
The following skills are available. When a task may benefit from a skill, first call the
read_skill tool to load its full instructions, then follow them.

- pdf-export: 把数据导出为 PDF 文件
</available_skills>
```

内置工具名默认为 `read_skill`，可用 `WithSkillToolName` 改名、`WithSkillToolDisabled` 禁用后自行注册替代工具。

## 思考模式

```go
callable.WithThinking(callable.Thinking{Effort: callable.EffortHigh})     // low / medium / high
callable.WithThinking(callable.Thinking{BudgetTokens: 16000})             // Anthropic 显式预算
```

| Provider | 请求字段 | 说明 |
|---|---|---|
| Anthropic | `thinking.budget_tokens` | effort 映射 2048/8192/16384；自动保证 max_tokens > budget |
| OpenAI Responses | `reasoning.effort` | 附带 `summary: "auto"` 流式输出思考摘要 |
| OpenAI Chat Completions | `reasoning_effort` | |
| GLM / 火山方舟 | `thinking: {type:"enabled"}` | 按 BaseURL 自动嗅探 |
| Qwen (DashScope) | `enable_thinking: true` | 按 BaseURL 自动嗅探 |
| DeepSeek | （无请求字段） | 解析输出的 `reasoning_content` |

思考原文完整保留在历史中并在下一轮回传（Anthropic 需要 signature，Responses 需要 reasoning item，国产端点需要 reasoning_content），保证工具循环中思考不丢。

## 图片输入

```go
callable.Image("/tmp/截图.png")         // 本地路径：读文件 + 识别类型 + base64
callable.Image("https://cdn/x.jpg")     // URL 直接透传
callable.ImageBytes(data, "png")        // 原始字节
callable.User("这张图里是什么？", callable.Image("/tmp/a.png"))  // 图文混排
```

同一份消息历史发给任何 provider 都会在发送时转换成对应格式（Anthropic base64 source / OpenAI data URL）。

## OpenAI 兼容端点

```go
// GLM（自动启用 thinking:{type:"enabled"} 兼容字段）
client := callable.NewClient(
    callable.NewOpenAIProvider(key, "https://open.bigmodel.cn/api/paas/v4"),
    callable.WithModel("glm-4.7"),
)

// DeepSeek / Qwen / vLLM / Ollama … 同理
// 手动覆盖嗅探结果：
callable.NewOpenAIProvider(key, url, callable.WithCompat(callable.CompatNone))
```

## 事件一览

| 事件 | 层级 | 说明 |
|---|---|---|
| `TurnStartEvent` / `TurnEndEvent` | agent | 轮次开始/结束 |
| `MessageStartEvent` | provider | 一条 assistant 消息开始 |
| `ThinkingDeltaEvent` | provider | 思考文本增量 |
| `TextDeltaEvent` | provider | 正文文本增量 |
| `ToolCallDeltaEvent` | provider | 工具名/参数 JSON 增量 |
| `MessageDoneEvent` | provider | 完整消息 + 本轮 Usage |
| `ToolCallEvent` | agent | 完整工具调用（执行前） |
| `ToolResultEvent` | agent | 工具执行结果 |
| `AgentDoneEvent` | agent | loop 结束（含 AgentResult） |

## 错误处理

- `*callable.APIError`：`Provider / StatusCode / Type / Message / Body`，`IsRetryable()` 判断可重试
- 网络错误 / 429 / 5xx 自动重试（默认 3 次，依次等待 3s / 10s / 30s，`WithRetries(n)` 配置次数，`WithRetries(0)` 关闭）
- 工具执行错误 → `IsError` 工具结果回传给模型（可自行重试换路），不中断 loop
- `*callable.MaxTurnsError`：附带 `Partial *AgentResult`
- 请求级逃生舱：`NewRequest(...).WithExtra("key", value)` 透传任意顶层字段

## 取消与超时（优雅停止）

所有调用都由 `context.Context` 控制。取消（或超时）时行为是**优雅**的：

- **上游同时停止**：进行中的 HTTP/SSE 连接立即关闭，服务端感知断连、停止生成（不再产生 token 费用）
- **不再发起新请求**：agent loop 在每轮开始前检查 context，取消后不会开启新的模型调用
- **部分结果不丢**：流式中途取消时，`Stream` / `RunStream` 返回**非 nil 的部分结果**（已生成的文本、已累积的 Usage），错误满足 `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded`
- **工具配对完整**：未执行的工具调用会合成 `IsError` 结果（"tool execution skipped: ..."），保证每个 tool_call 都有配对的 tool_result，部分轨迹仍是可重放的有效会话
- **Session 不被污染**：中止的运行不会写入会话历史

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := agent.RunStream(ctx, onEvent, callable.User("..."))
if errors.Is(err, context.DeadlineExceeded) {
    // result 非 nil：result.FinalText 是已生成的部分文本
}
```

注意：正在执行中的工具函数会收到同一个 ctx —— 工具实现里应自行响应取消（如数据库查询、子请求），Go 无法强制杀死一个无视 ctx 的 goroutine。

## 更多示例

见 [`examples/`](./examples)：quickstart、tools（agent loop）、thinking（思考+多轮会话）、vision（图片）、skills（渐进披露）。

## 设计说明

- **统一消息模型**：内部只有一种 `Message{Role, Parts}`，Part 为封闭类型族（Text/Image/Thinking/ToolCall/ToolResult）。各 Provider 负责双向转换，业务代码不感知 wire 格式。
- **历史回传保真**：assistant 消息可携带 per-provider 原始数据（`Message.SetProviderExtra`），例如 Responses 的 reasoning item 原文回传；`Message` 支持完整 JSON 序列化往返，可持久化。
- **单一入口**：实现在 `internal/core`，根包 `callable` 的 `callable.go` 是唯一对外入口（全量 re-export），一个 import 拿到全部能力。

## License

[MIT](./LICENSE)
