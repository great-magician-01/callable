# callable — Go Agent Loop 库设计方案

> 一个统一的 LLM 调用库：抹平 OpenAI（Chat Completions / Responses）与 Anthropic 两种 API 格式的差异，
> 内置完整的 agent loop（工具调用循环、skill 渐进式披露、思考模式、图片输入、流式输出）。

## 0. 前提与假设

- **"openai completions" 理解为 Chat Completions API**（`/v1/chat/completions`），与 Responses API（`/v1/responses`）并列支持。如果你指的是已废弃的 legacy `/v1/completions`，请指出。
- **单一 Go package**（`callable`），不拆子包——避免接口/实现的循环依赖问题，使用方一个 import 拿到全部能力。
- 已确认的决策：skill 采用**渐进式披露**机制；**允许第三方依赖**；**适配常见国产 OpenAI 兼容端点**（DeepSeek / GLM / Qwen / Doubao 的思考字段）。
- module：`github.com/great-magician-01/callable`，Go 1.26.4（go.mod 已存在）。

## 1. 总体架构

```
┌─────────────────────────────────────────────────────┐
│  Agent / Session（agent loop 层）                    │
│  · 多轮循环：模型 → 工具 → 模型 → … 直到最终回答     │
│  · 工具注册表 + 自动执行 + 审批钩子                  │
│  · Skill 渐进式披露（system prompt 索引 + 内置工具） │
│  · 事件流回调（thinking/text/toolcall 全程可见）     │
├─────────────────────────────────────────────────────┤
│  Client（调用层）                                    │
│  · Create（一次性）/ Stream（流式）                  │
│  · 重试（429/5xx/网络错误，指数退避）                │
│  · 默认参数（model / maxTokens / thinking …）        │
├─────────────────────────────────────────────────────┤
│  Provider 适配层（三种实现，统一接口）               │
│  · OpenAI Chat Completions（含国产端点 compat）      │
│  · OpenAI Responses                                 │
│  · Anthropic Messages                               │
│  职责：统一模型 ⇄ 各家 wire 格式互转 + SSE 解析      │
├─────────────────────────────────────────────────────┤
│  统一消息模型（provider 无关）                       │
│  Message{Role, Parts[]}  Part = Text / Image /       │
│  Thinking / ToolCall / ToolResult                    │
└─────────────────────────────────────────────────────┘
```

核心思路：**内部只有一种消息/请求/响应模型**，各 Provider 负责双向转换。
用户代码只面向统一模型，切换 provider 不改业务代码。

## 2. 包结构（根包为唯一入口，实现在 internal/core）

```
callable/
├── go.mod                    # + github.com/invopop/jsonschema（唯一第三方依赖）
├── callable.go               # 唯一对外入口：包文档 + 全量 API re-export
│                             #   （type alias + const + 函数封装，用户无感）
├── internal/core/            # 全部实现与测试（internal 保证外部无法直接导入）
│   ├── message.go            # Message / Role / 消息构造器（User/System/...）
│   ├── content.go            # Part 家族：Text/Image/Thinking/ToolCall/ToolResult
│   ├── request.go / response.go  # 统一请求/响应/Usage/StopReason
│   ├── stream.go             # 统一流式事件类型
│   ├── thinking.go           # Thinking 配置 + Effort 档位
│   ├── image.go              # 图片加载（路径/URL/字节）、媒体类型识别、base64
│   ├── tool.go               # Tool 接口、NewTool[A]（泛型+反射 schema）、注册表
│   ├── skill.go              # Skill 类型 + read_skill 内置工具 + 读取钩子
│   ├── provider.go           # Provider 接口 + 公共 HTTP/SSE/重试基础设施
│   ├── provider_oai_chat.go  # Chat Completions 适配器（+ 国产 compat）
│   ├── provider_oai_responses.go # Responses 适配器
│   ├── provider_anthropic.go # Anthropic Messages 适配器
│   ├── client.go / agent.go  # Client、Agent loop、Session
│   ├── errors.go             # APIError / MaxTurnsError
│   └── *_test.go             # golden 测试 + SSE fixture 测试 + httptest 集成测试
└── examples/                 # quickstart / tools / vision / thinking / skills
```

## 3. 使用体验（目标 API）

```go
// ── 1. 创建 Client：三种 API 格式任选，切换只改这一行 ──────────────
client := callable.NewClient(
    callable.NewAnthropicProvider(apiKey, "https://api.anthropic.com"),  // 或
    // callable.NewOpenAIProvider(apiKey, "https://api.openai.com/v1"),      // Chat Completions
    // callable.NewOpenAIResponsesProvider(apiKey, "https://api.openai.com/v1"), // Responses
    callable.WithModel("claude-sonnet-5"),
)
// 端点地址是构造函数的必填参数（兼容任意 OpenAI 兼容端点）；其余构造选项：WithHTTPClient、WithHeader

// ── 2. 定义工具：struct 即参数 schema，handler 即执行体 ────────────
type WeatherArgs struct {
    City string `json:"city" jsonschema:"description=城市名称，如：北京"`
    Unit string `json:"unit,omitempty" jsonschema:"description=温度单位,enum=celsius,enum=fahrenheit"`
}
weather := callable.NewTool("get_weather", "查询指定城市的实时天气",
    func(ctx context.Context, args WeatherArgs) (any, error) {
        return fmt.Sprintf("%s 当前 26°C，晴", args.City), nil
    })

// ── 3. Skill：渐进式披露 ──────────────────────────────────────────
skill := callable.NewSkill("pdf-export", "把数据导出为 PDF 文件",
    "# 完整指令\n…（只有模型调用 read_skill 时才会被读取）")

// ── 4. Agent：注册一切，Run 起来就是完整 loop ─────────────────────
agent := callable.NewAgent(client,
    callable.WithSystemPrompt("你是一个务实的助手"),
    callable.WithTools(weather),
    callable.WithSkills(skill),
    callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
    callable.WithMaxTurns(25),
    // 可选审批钩子：每次工具执行前回调，可放行/否决/改参
    callable.WithToolCallHook(func(ctx context.Context, call callable.ToolCall) (callable.ToolDecision, error) {
        return callable.Approve(), nil
    }),
)

// 一次性运行（内部自动执行：模型 → 工具 → 模型 → … → 最终回答）
result, err := agent.Run(ctx, callable.User("北京天气怎么样？"))
// result.FinalText / result.Messages（完整轨迹，含思考与工具记录）
// result.Usage（跨轮累计）/ result.Turns

// 流式运行：思考、正文、工具调用、工具结果全程可见
result, err := agent.RunStream(ctx, func(ev callable.Event) {
    switch e := ev.(type) {
    case callable.ThinkingDeltaEvent: // 思考增量
    case callable.TextDeltaEvent:     fmt.Print(e.Text)
    case callable.ToolResultEvent:    fmt.Println("工具完成:", e.Name, e.Content)
    }
}, callable.User("这张图里是什么？", callable.Image("/tmp/截图.png")))

// ── 5. 多轮会话：历史自动维护 ─────────────────────────────────────
sess := agent.Session()
sess.Ask(ctx, callable.User("上海呢？"))
sess.AskStream(ctx, handler, callable.User("再看看广州", callable.Image("/tmp/gz.png")))
sess.History() // []Message，可 JSON 序列化持久化

// ── 6. 低层直调（不用 agent loop 也可以）──────────────────────────
resp, err := client.Create(ctx, callable.NewRequest(callable.User("你好")))
err  = client.Stream(ctx, callable.NewRequest(...), func(ev callable.Event) error { ... })
```

## 4. 统一消息模型

```go
type Role string // RoleSystem / RoleUser / RoleAssistant / RoleTool

type Message struct {
    Role  Role
    Parts []Part // 一条消息可混合多种内容
}

// Part 为封闭的类型标签接口（sealed interface），按 type 字段做 JSON 序列化：
type TextPart       struct{ Text string }
type ImagePart      struct{ Path string; URL string; Data []byte; MediaType string }
type ThinkingPart   struct{ Text string; Signature string /* Anthropic 回传用 */;
                           ID string /* Responses 回传用 */ }
type ToolCallPart   struct{ ID, Name string; Arguments string /* 原始 JSON */ }
type ToolResultPart struct{ ToolCallID, Name, Content string; IsError bool }

// Message 附带 per-provider 附加数据（如 Responses 的 reasoning item 原文），
// 保证多轮对话回传时不丢信息；Message 支持 MarshalJSON/UnmarshalJSON（可持久化）。
```

关键点：**thinking 块与工具调用会完整保留在历史里**。这是正确性的要求——
Anthropic 开思考+工具时必须回传 thinking 块（含 signature）；Responses 需要回传
reasoning item；DeepSeek/GLM 需要回传 reasoning_content。统一模型都装得下。

## 5. 三种 wire 格式映射表（Provider 适配层的核心工作）

| 统一概念 | Chat Completions | Responses | Anthropic Messages |
|---|---|---|---|
| system | `messages[role=system]`（多条合并） | `instructions` 字段 | 顶层 `system` 字段 |
| user 文本 | `content[].type=text` | `input_text` | `{type:"text"}` block |
| user 图片 | `image_url`（dataURL 或 URL） | `input_image` | `{type:"image"}` base64 source 或 url source |
| assistant 文本 | `content` 字符串 | output_text item | text block |
| assistant 思考 | `reasoning_content`（国产端点） | reasoning item（原文回传） | thinking block（含 signature 回传） |
| 工具调用 | `tool_calls[]`（index 增量累积） | `function_call` item（`call_id`） | `tool_use` block（input 为对象） |
| 工具结果 | `role=tool` 消息（多条） | `function_call_output` item | user 消息内 `tool_result` block |
| 工具定义 | `tools[].function.parameters` | `tools[].function.parameters` | `tools[].input_schema` |
| 停止原因 | `finish_reason`（tool_calls/stop） | `response.completed` 的 status | `stop_reason`（tool_use/end_turn） |

**流式事件解析**（三家差异最大处，逐家适配）：
- Chat Completions：SSE `choices[].delta`；tool_calls 按 index 累积 arguments 增量；`delta.reasoning_content`（国产）为思考增量；`data: [DONE]` 结束。
- Responses：typed events（`response.output_text.delta` / `response.reasoning_summary_text.delta` / `response.function_call_arguments.delta` / `response.output_item.added|done` / `response.completed`），忽略未知事件类型（前向兼容）。
- Anthropic：`event:` + `data:` 行；`content_block_start/delta/stop`；`thinking_delta`；`input_json_delta`（工具参数增量）；`message_delta` 携带最终 usage。

## 6. 思考模式与强度

```go
type Thinking struct {
    Effort       Effort // EffortOff / EffortLow / EffortMedium / EffortHigh
    BudgetTokens int    // Anthropic 专用：显式预算，优先于 Effort 映射
}
```

映射规则：

| Provider | 请求字段 | Effort 映射 |
|---|---|---|
| Anthropic | `thinking:{type:"enabled", budget_tokens:N}` | low≈2048 / medium≈8192 / high≈16384；自动保证 max_tokens > budget |
| OpenAI Responses | `reasoning:{effort:"low|medium|high", summary:"auto"}` | 直接映射 |
| OpenAI Chat Completions | `reasoning_effort` | 直接映射 |
| GLM 兼容端点 | `thinking:{type:"enabled"|"disabled"}` | enabled 恒开，EffortOff → disabled |
| Qwen 兼容端点 | `enable_thinking:true|false` | 同上 |
| DeepSeek | reasoner 模型恒思考，无开关 | 仅解析输出的 `reasoning_content` |

- **国产端点自动嗅探**：根据 BaseURL（bigmodel.cn / deepseek.com / dashscope 等）自动启用对应 compat；`WithCompat(...)` 手动覆盖（避免把非标字段发给官方 OpenAI 报错）。
- **响应侧解析永远宽松**：任何端点返回 `reasoning_content` / `reasoning` 都解析为 ThinkingPart，不做开关。
- 各端思考原文按第 4 节规则回传，保证工具循环中思考不丢。

## 7. 工具系统

```go
type Tool interface {
    Definition() ToolDefinition                       // name/description/parameters(JSON Schema)
    Execute(ctx context.Context, rawArgs string) (ToolResult, error)
}

// 泛型构造器：参数 struct → 自动 JSON Schema（invopop/jsonschema 反射生成）
func NewTool[A any](name, description string,
    fn func(ctx context.Context, args A) (any, error)) *Tool
```

- **Schema 生成**：`invopop/jsonschema` 反射生成，`DoNotReference=true`（内联 $ref）、draft-07（兼容性最好）；字段描述用 `jsonschema:"description=..."` tag，枚举用 `enum=`。
- **返回值**：`string` 原样返回；其他类型 `json.Marshal` 后作为工具结果内容。
- **工具执行错误不中断 loop**：handler 返回 error → 构造 `IsError=true` 的 ToolResult 回传给模型（Anthropic `is_error` / OpenAI 工具消息内错误文本），模型可自行重试或换路；API/网络错误才中断。
- **并行工具调用**：模型一次返回多个 tool_calls 时默认顺序执行（确定性），`WithParallelToolExecution(true)` 可并发。
- **审批钩子** `WithToolCallHook`：每次执行前回调，返回 `Approve() / Deny(reason) / ReplaceArgs(newJSON)`——实现"人工确认高危操作"。
- 请求级 `Extra map[string]any` 透传字段作为逃生舱（合并进请求体顶层，覆盖任何未适配的参数）。

## 8. Skill：渐进式披露

```
system prompt 中注入（只有索引，不含全文）：
  <available_skills>
  以下技能可用。当任务可能涉及某项技能时，先调用 read_skill 工具
  获取其完整指令，然后严格按照指令执行。
  - pdf-export: 把数据导出为 PDF 文件
  - web-search: 检索互联网信息
  </available_skills>
```

- **内置工具 `read_skill(name string) -> string`**：按名称查找 skill，返回 Instructions 全文。
- **读取钩子**（你要求的"暴露钩子来修改"）：

```go
// 在内容返回给模型之前调用，可改写/懒加载/注入运行时上下文/审计；
// 返回 error 则作为工具错误回传给模型。
callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
    return content + "\n\n当前时间: " + time.Now().Format(time.RFC3339), nil
})
```

- 内置工具名/描述可配置（`WithSkillToolName`），也可禁用（`WithSkillToolDisabled`）后注册自定义替代工具。
- `NewSkill(name, description, instructions)` 内存构造。（磁盘 SKILL.md 加载暂不做，后续可加。）

## 9. 图片输入

```go
callable.Image("/tmp/截图.png")        // 本地路径：读文件 + 识别类型 + base64
callable.ImageURL("https://.../a.jpg") // 远程 URL：直接透传（各家原生支持）
callable.ImageBytes(data, "png")       // 原始字节
```

- 构造期只存引用，**发送期按目标 provider 惰性转换**（同一份消息历史发给谁就转谁的格式）。
- 媒体类型：扩展名识别 + `http.DetectContentType` 兜底；不支持的类型返回明确错误。
- 一条消息多图、图文混排均支持。

## 10. Agent Loop

```
Run(input):
  msgs = [System(base prompt + skill 索引)] + history + input
  for turn in 1..MaxTurns:
      resp = provider.Stream(msgs, tools=注册工具+read_skill, thinking)
        ↳ emit: ThinkingDelta / TextDelta / ToolCallDelta / MessageDone
      msgs += resp.Message          // 含 thinking / toolcall parts + provider 附加数据
      if resp 无工具调用: return AgentResult{FinalText, Messages, Usage(累计), Turns}
      for call in resp.ToolCalls:   // 默认顺序，可选并发
          decision = ToolCallHook(call)?    // 审批/改参/否决（可选钩子）
          result  = tool.Execute(call.Args)
          emit ToolResultEvent
          msgs += 工具结果消息      // 格式按第 5 节映射表
  return MaxTurnsError{Turns, Partial}   // 附带已完成的 partial 结果
```

- `Run` = `RunStream(ctx, nil, ...)`，单一实现两条路径。
- **Session** 维护历史（含 thinking/工具轨迹的完整回传数据），`Ask/AskStream` 追加式对话；历史也可 JSON 序列化落盘后恢复。
- 配置层级：**Request 覆盖 Agent 覆盖 Client 默认**。

## 11. 流式事件一览

| 事件 | 层级 | 载荷 |
|---|---|---|
| `TurnStartEvent` | agent | Turn 序号 |
| `MessageStartEvent` | provider | — |
| `ThinkingDeltaEvent` | provider | 思考文本增量 |
| `TextDeltaEvent` | provider | 正文文本增量 |
| `ToolCallDeltaEvent` | provider | 工具名/参数 JSON 增量 |
| `MessageDoneEvent` | provider | 完整 Message + 本轮 Usage |
| `ToolCallEvent` | agent | 完整工具调用（执行前） |
| `ToolResultEvent` | agent | 执行结果 / 错误 |
| `TurnEndEvent` | agent | Turn 序号 |
| `AgentDoneEvent` | agent | AgentResult |

统一 `Usage{InputTokens, OutputTokens, ReasoningTokens, CacheReadTokens, CacheWriteTokens}`，agent 跨轮累计。

## 12. 错误处理与重试

- `APIError{Provider, StatusCode, Type, Message, Body}`：4xx/5xx 结构化错误（含各家 error body 原文）。
- 网络错误 / 429 / 5xx 自动重试：固定等待 3s / 10s / 30s，默认 3 次，`WithRetries(n)` 配置次数、`WithRetries(0)` 关闭；`context.Canceled` 不重试。
- `MaxTurnsError{Turns, Partial *AgentResult}`：循环达上限，附带部分结果。
- 工具错误 → 回传模型（见第 7 节），不算失败。

## 13. 依赖

- `github.com/invopop/jsonschema` —— **唯一第三方依赖**（工具参数 schema 反射生成）。
- SSE 解析、HTTP、重试均为标准库手写（~百行，三家 SSE 格式差异需要完全控制）。

## 14. 测试计划

- **golden 请求测试**：三家 provider × 消息/工具/思考/图片/系统提示组合 → 与固定 wire JSON 比对。
- **SSE fixture 测试**：三家真实格式的流式响应样本（纯文本流、工具参数增量累积、思考增量、多工具并发、[DONE]/message_stop），断言统一事件序列正确。
- **agent loop 集成测试**（httptest 模拟三家端点）：多轮工具循环、max turns、工具报错恢复、审批钩子（否决/改参）、skill 读取流（渐进披露 + 钩子）、Session 多轮、思考块回传正确性（Anthropic signature、Responses reasoning item）。
- **单测**：schema 生成（嵌套 struct/slice/map/enum/omitempty）、图片编码与类型识别、消息 JSON 往返序列化。
- 真实 API 不进 CI（无 key），examples 提供可运行示例。

## 15. 实现顺序

1. 核心类型：message / content / request / response / usage / errors
2. 工具系统 + jsonschema 集成
3. HTTP/SSE 公共设施 + **Chat Completions 适配器**（含国产 compat）
4. **Anthropic 适配器**
5. **Responses 适配器**
6. Client（Create / Stream / 重试）
7. Agent loop + Session + 审批钩子
8. Skill 渐进式披露 + read_skill 内置工具 + 读取钩子
9. 图片输入（三家转换）
10. 测试补全 + examples + README

预计 ~3500 行实现 + ~1500 行测试。

## 16. 范围外（本期不做）

- 图片以外的多模态输入（PDF/音频）
- 服务端工具（Anthropic web search / code execution 等）
- token 计数、prompt caching 显式管理（可经 Extra 透传）
- embeddings / batch API
- legacy `/v1/completions`
