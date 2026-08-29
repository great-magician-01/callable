# callable

**中文** | [English](README_EN.md)

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
- **联网搜索**：优先使用 provider 内置的服务端搜索（Kimi、GLM/Z.AI、Qwen、Anthropic、OpenAI Responses，按 BaseURL 自动嗅探），无内置时可用 Tavily 回退工具（`WithTavilyAPIKey`）；`WithWebSearch` 显式开关
- **多轮对话**：内置 `Session` 维护历史，thinking 块 / 工具轨迹完整保留并正确回传（Anthropic signature、Responses reasoning item、DeepSeek/GLM reasoning_content）
- **上下文管理**：Session 跟踪上下文窗口占用（`Usage.ContextTokens` 按 provider 归一化），支持阈值触发自动压缩（`WithAutoCompact`）与手动 `Compact` 压缩历史
- **会话 ID 与持久化**：每个会话/运行自动生成 ID（`sess-` / `run-` 前缀），出现在所有流式事件与 `AgentResult` 上；`Session.Snapshot` / `Restore` 一行完成会话持久化与恢复；Session 并发安全
- **结构化输出**：`JSONMode` / `JSONSchema` / `JSONSchemaFor[T]`（从 struct 反射）统一约束 JSON 输出，自动映射三家原生控制字段；`resp.DecodeJSON(&v)` 直接解码到 Go struct
- **采样参数**：`WithTopP` / `WithStopSequences` 支持请求级与 Client 默认值，按 provider 映射（OpenAI Responses 无 stop）；思考模式下自动省略 temperature/top_p
- **请求/响应钩子**：`WithRequestHook` / `WithResponseHook` 观测每次模型调用（含 agent loop 内部），用于日志、trace 与成本统计
- **流式**：统一的 `ThinkingDelta / TextDelta / ToolCallDelta / ToolResult / Turn*` 事件流，agent loop 全程可见
- **思考模式**：统一的 `Effort`（low/medium/high）映射到各家原生字段；自动适配 GLM/智谱、火山方舟、Qwen、DeepSeek 等国产端点的非标思考字段（按 BaseURL 自动嗅探）
- **Skill 渐进式披露**：system prompt 只注入 name/description 索引，模型按需通过内置 `read_skill` 工具加载全文；读取钩子可改写内容
- **SubAgent 委派**：注册命名子代理（可指定模型、提示词、工具、skill），默认**不进工具列表**——模型先经内置 `load_agent` 工具手动加载，动态生成 `call_<name>` 工具后才能委派子任务
- **图片输入**：传本地路径 / URL / 字节，发送时按目标 provider 自动转换格式（base64 source / data URL）
- **零依赖网络层**：HTTP 重试（429/5xx 指数退避）、SSE 解析均为标准库实现；唯一第三方依赖是 `invopop/jsonschema`

## 安装

```bash
go get github.com/great-magician-01/callable
```

## 快速开始

```go
// 第二个参数是端点地址：内置了常见厂商常量（见下），其它端点传实际 URL 即可
client := callable.NewAnthropicClient(apiKey, callable.AnthropicURL, "claude-sonnet-5")
// 或 NewOpenAIClient / NewOpenAIResponsesClient；需要 ProviderOption（如 WithRetries）时
// 用两步构造：callable.NewClient(callable.NewAnthropicProvider(apiKey, callable.AnthropicURL), callable.WithModel(...))

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
sess := agent.Session(
    callable.WithContextWindow(200_000),   // 上下文窗口，默认 1M
    callable.WithAutoCompact(true),        // 达到阈值自动压缩历史，默认关闭
)
sess.Ask(ctx, callable.User("上海呢？"))            // 历史自动维护
sess.AskStream(ctx, handler, callable.User("广州呢？"))
sess.ContextFillRatio() // 当前上下文占用比例
sess.Compact(ctx)       // 手动压缩历史
sess.ID()               // 会话 ID（sess- 前缀），出现在所有事件与 AgentResult 上
data, _ := sess.Snapshot() // 持久化整个会话（id + 历史 + 上下文用量）
sess.Restore(data)      // 从快照恢复
sess.History()          // []Message，也可自行 JSON 序列化
sess.SetHistory(saved)  // 手工恢复历史
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

## SubAgent：子代理委派

```go
translator := callable.NewSubAgent("translator", "把中文翻译成地道的英文",
    callable.WithSubAgentModel("gpt-5-mini"),        // 指定模型（沿用父 client 的端点与密钥）
    callable.WithSubAgentPrompt("你是一名专业译者……"), // 指定提示词
)

researcher := callable.NewSubAgent("researcher", "深度调研一个主题",
    callable.WithSubAgentClient(otherClient),   // 或完全换成另一个 client/provider
    callable.WithSubAgentTools(searchTool),     // 指定工具（对父 agent 不可见）
    callable.WithSubAgentSkills(citingSkill),   // 指定 skill（子 agent 自带 read_skill）
    callable.WithSubAgentThinking(callable.Thinking{Effort: callable.EffortHigh}),
    callable.WithSubAgentMaxTurns(10),
)

agent := callable.NewAgent(client,
    callable.WithSystemPrompt("翻译交给 translator，调研交给 researcher"),
    callable.WithSubAgents(translator, researcher),
)
```

与 skill 一样走渐进式披露：**子 agent 默认不会加载进工具列表**，system prompt 只注入 name/description 索引：

```
<available_agents>
The following sub-agents are available for delegating subtasks. They are NOT
loaded as tools yet. When a subtask matches one of them, first call the
load_agent tool with the sub-agent's name to load it; this registers a
call_<name> tool, which you then call with a self-contained task description.

- translator: 把中文翻译成地道的英文
- researcher: 深度调研一个主题
</available_agents>
```

委派流程分两步，均由模型驱动：

1. **手动加载**：模型调用内置 `load_agent` 工具 → 返回该子 agent 的完整卡片（描述、提示词、能力清单），同时**动态注册** `call_<name>` 工具，下一轮请求起对模型可见
2. **调用**：模型调用 `call_<name>({"task": "..."})` → 子 agent 以全新会话独立跑自己的 agent loop（用自己的模型/工具/skill），最终回答作为工具结果返回给父 agent

行为细节：

- 加载幂等：load 一次后 `call_<name>` 持续可用；若与用户自定义工具重名，加载会报错而不是覆盖
- 未加载时直接调用 `call_<name>` 会得到 unknown tool 错误（工具列表里确实没有它）
- 内置工具名默认 `load_agent`，可用 `WithSubAgentToolName` 改名、`WithSubAgentToolDisabled` 禁用后自行注册替代
- 子 agent 不继承父 agent 的子代理列表（不嵌套）；每次调用都是全新会话，互相不共享历史
- 子 agent 达到 max turns 时，已产生的部分回答会带回给父 agent 并附提示，而不是直接失败
- 事件透传：默认子代理内部对父 agent 的事件回调不可见；`WithSubAgentEvents(true)` 后子代理改为流式执行，其每个事件包装为 `SubAgentEvent`（含子代理名和原始事件）转发到父 agent 的事件回调，消费端可据此区分主/子输出

## 思考模式

```go
callable.WithThinking(callable.Thinking{Effort: callable.EffortHigh})     // low / medium / high
callable.WithThinking(callable.Thinking{BudgetTokens: 16000})             // Anthropic / Qwen 显式预算
```

| Provider | 请求字段 | 说明 |
|---|---|---|
| Anthropic | `thinking.budget_tokens` | effort 映射 2048/8192/16384；自动保证 max_tokens > budget |
| OpenAI Responses | `reasoning.effort` | 附带 `summary: "auto"` 流式输出思考摘要 |
| OpenAI Chat Completions | `reasoning_effort` | |
| GLM / 智谱（含 Z.AI） | `thinking:{type:"enabled"}` + `reasoning_effort` | 按 BaseURL 自动嗅探；medium→high（GLM-5.3 拒收 medium）；⚠️ GLM-5.3 强制思考，传 disabled 会 400 |
| 火山方舟 | `thinking:{type:"enabled"}` + `reasoning_effort` | 按 BaseURL 自动嗅探；effort 原样透传 |
| Qwen (DashScope) | `enable_thinking:true` + `thinking_budget` | 按 BaseURL 自动嗅探；`BudgetTokens` 映射为 thinking_budget |
| DeepSeek | `thinking:{type:"enabled"}` + `reasoning_effort` | 按 BaseURL 自动嗅探；默认开思考（effort high），传 `Thinking{}` 显式关闭；medium 服务端映射为 high |

思考原文完整保留在历史中并在下一轮回传（Anthropic 需要 signature，Responses 需要 reasoning item，国产端点需要 reasoning_content），保证工具循环中思考不丢。

## 图片输入

```go
callable.Image("/tmp/截图.png")         // 本地路径：读文件 + 识别类型 + base64
callable.Image("https://cdn/x.jpg")     // URL 直接透传
callable.ImageBytes(data, "image/png")  // 原始字节（mediaType 需为完整 MIME 类型）
callable.User("这张图里是什么？", callable.Image("/tmp/a.png"))  // 图文混排
```

同一份消息历史发给任何 provider 都会在发送时转换成对应格式（Anthropic base64 source / OpenAI data URL）。

## OpenAI 兼容端点

```go
// GLM（自动启用 thinking:{type:"enabled"} 兼容字段）
client := callable.NewClient(
    callable.NewOpenAIProvider(key, callable.GLMURL),
    callable.WithModel("glm-4.7"),
)

// DeepSeek / Qwen / vLLM / Ollama … 同理
// 手动覆盖嗅探结果：
callable.NewOpenAIProvider(key, url, callable.WithCompat(callable.CompatNone))
```

### 内置端点常量

常见厂商的 baseURL 已内置为常量，直接传给构造函数即可；未内置的端点照旧传实际 URL：

| 常量 | 值 | 适用构造函数 |
|---|---|---|
| `OpenAIURL` | `https://api.openai.com/v1` | OpenAI / Responses |
| `AnthropicURL` | `https://api.anthropic.com` | Anthropic |
| `DeepSeekURL` | `https://api.deepseek.com` | OpenAI |
| `DeepSeekAnthropicURL` | `https://api.deepseek.com/anthropic` | Anthropic |
| `GLMURL` | `https://open.bigmodel.cn/api/paas/v4` | OpenAI |
| `GLMAnthropicURL` | `https://open.bigmodel.cn/api/anthropic` | Anthropic |
| `ZAIURL` | `https://api.z.ai/api/paas/v4` | OpenAI |
| `ZAIAnthropicURL` | `https://api.z.ai/api/anthropic` | Anthropic |
| `QwenURL` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | OpenAI |
| `ArkURL` | `https://ark.cn-beijing.volces.com/api/v3` | OpenAI |
| `KimiURL` | `https://api.moonshot.cn/v1` | OpenAI |
| `KimiAnthropicURL` | `https://api.moonshot.cn/anthropic` | Anthropic |

```go
callable.NewAnthropicProvider(key, callable.DeepSeekAnthropicURL) // DeepSeek 的 Anthropic 兼容端点
callable.NewOpenAIProvider(key, callable.QwenURL)                 // Qwen，方言自动嗅探
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
- 网络错误 / 429 / 5xx 自动重试（默认 3 次，依次等待 3s / 10s / 30s，`WithRetries(n)` 配置次数，`WithRetries(0)` 关闭，`WithRetryBackoff(d...)` 自定义等待节奏）
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

见 [`examples/`](./examples)：quickstart、tools（agent loop）、thinking（思考+多轮会话）、vision（图片）、skills（渐进披露）、subagents（子代理委派）、compact（上下文压缩）、websearch（联网搜索）、deepseek（真实 API 全场景）。

## 文档

按功能拆分的详细使用文档，英文版见 [`docs/en/`](./docs/en)（每篇文档页内也有语言切换链接）：

- [快速开始](./docs/zh/getting-started.md)：安装、Client 与 Provider、端点常量、Compat 方言
- [消息模型](./docs/zh/messages.md)：Message/Part、构造器、历史回传保真、持久化
- [Agent 循环](./docs/zh/agent.md)：Run/RunStream、审批钩子、并行工具、max turns
- [多轮会话](./docs/zh/session.md)：Session、历史持久化与恢复
- [工具](./docs/zh/tools.md)：NewTool/NewRawTool、JSON Schema 生成、错误回传
- [联网搜索](./docs/zh/web-search.md)：内置搜索自动嗅探、Kimi 回显协议、Tavily 回退
- [流式事件](./docs/zh/streaming.md)：事件类型一览、事件序列、Usage
- [思考模式](./docs/zh/thinking.md)：Effort 映射、各家端点的坑
- [结构化输出与采样参数](./docs/zh/structured-output.md)：JSONMode/JSONSchema/JSONSchemaFor、DecodeJSON、top_p 与停止序列
- [Skill 渐进披露](./docs/zh/skills.md)：read_skill、读取钩子
- [子代理](./docs/zh/subagents.md)：load_agent 两步委派、事件透传
- [图片输入](./docs/zh/images.md)：本地/URL/字节、跨 provider 转换
- [错误处理](./docs/zh/errors.md)：重试、取消与超时、WithExtra 逃生舱

## 设计说明

- **统一消息模型**：内部只有一种 `Message{Role, Parts}`，Part 为封闭类型族（Text/Image/Thinking/ToolCall/ToolResult）。各 Provider 负责双向转换，业务代码不感知 wire 格式。
- **历史回传保真**：assistant 消息可携带 per-provider 原始数据（`Message.SetProviderExtra`），例如 Responses 的 reasoning item 原文回传；`Message` 支持完整 JSON 序列化往返，可持久化。
- **单一入口**：实现在 `internal/core`，根包 `callable` 的 `callable.go` 是唯一对外入口（全量 re-export），一个 import 拿到全部能力。

## License

[MIT](./LICENSE)
