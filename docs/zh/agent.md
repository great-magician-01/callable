# Agent 循环

**中文** | [English](../en/agent.md)

`Agent` 是 callable 的核心抽象：它在 `Client` 之上自动运行完整的工具调用循环——发送消息给模型，模型要求调用工具就执行工具并把结果回传，如此往复，直到模型给出不含工具调用的最终回答（或触达轮数上限）。你只需注册工具并调用一次 `Run`，无需手写任何循环。

## 内部工作流程

每轮（turn）的内部流程如下：

```
Run(input):
  msgs = [System(基础提示词 + skill 索引 + 子代理索引)] + input
  for turn in 1..MaxTurns:
      resp = client.Stream(msgs, tools, thinking)     # Run 时等价于 Create
        ↳ 触发事件: TurnStart / MessageStart / ThinkingDelta /
                    TextDelta / ToolCallDelta / MessageDone
      msgs += resp.Message      # 含 thinking / tool_call 及 provider 附加数据
      if resp 无工具调用:
          return AgentResult{FinalText, Messages, Usage(累计), Turns, AgentCompleted}
      for call in resp.ToolCalls:                     # 默认顺序执行
          decision = ToolCallHook(call)?              # 可选审批钩子
          result = tool.Execute(call.Args)
          ↳ 触发事件: ToolCallEvent / ToolResultEvent
          msgs += 工具结果消息
  return MaxTurnsError{Turns, Partial}                # 附带部分结果
```

关键行为：

- **工具执行错误不中断循环**：工具 handler 返回 error 时，会构造 `IsError=true` 的工具结果回传给模型（模型可自行重试或换路）。只有 API/网络错误、审批钩子自身返回 error、context 取消才会中断 `Run`。
- **模型调用了不存在的工具**（幻觉）同样不中断：回传 `unknown tool "..."` 错误结果。
- **思考块与工具轨迹完整保留**：历史中的 thinking 块（Anthropic signature、Responses reasoning item、国产端点 reasoning_content）会按各 provider 的要求正确回传，见[思考模式](thinking.md)。
- 事件流详见[流式事件](streaming.md)。

## NewAgent

```go
func NewAgent(client *Client, opts ...AgentOption) *Agent
```

`client` 提供 provider、模型与默认参数（见[快速开始](getting-started.md)）；所有行为都通过 `AgentOption` 配置。

## AgentOption 一览

| 选项 | 签名 | 说明 |
|---|---|---|
| `WithSystemPrompt` | `WithSystemPrompt(prompt string)` | 设置 agent 的系统提示词。skill 索引与子代理索引会自动追加在其后 |
| `WithTools` | `WithTools(tools ...Tool)` | 注册模型可调用的工具，可多次调用（追加）。见[工具](tools.md) |
| `WithSkills` | `WithSkills(skills ...Skill)` | 注册 skill（渐进式披露：只注入索引，模型经内置 `read_skill` 工具按需加载全文）。见 [Skill 渐进披露](skills.md) |
| `WithSubAgents` | `WithSubAgents(subs ...SubAgent)` | 注册子代理定义。默认不进工具列表，模型先经内置 `load_agent` 工具加载、动态生成 `call_<name>` 工具后才能委派。见[子代理](subagents.md) |
| `WithThinking` | `WithThinking(t Thinking)` | 开启思考模式，如 `Thinking{Effort: EffortHigh}`。见[思考模式](thinking.md) |
| `WithMaxTurns` | `WithMaxTurns(n int)` | 单次运行允许的最大模型调用轮数。**默认 25**；`n <= 0` 会被忽略（保持默认值） |
| `WithToolCallHook` | `WithToolCallHook(h ToolCallHook)` | 工具执行前的审批钩子：放行 / 否决 / 改写参数。见下文 |
| `WithParallelToolExecution` | `WithParallelToolExecution(enabled bool)` | 允许同一轮内的多个工具调用并发执行。默认 `false`（顺序执行） |

skill 与 subagent 的专项配置项也是 `AgentOption`，细节见对应文档：

- skill：[Skill 渐进披露](skills.md) — `WithSkillReadHook`（读取前改写内容）、`WithSkillToolName`（内置工具改名，默认 `read_skill`）、`WithSkillToolDisabled`（禁用内置工具后自行注册替代）
- subagent：[子代理](subagents.md) — `WithSubAgentToolName`（默认 `load_agent`）、`WithSubAgentToolDisabled`、`WithSubAgentEvents`（子代理事件透传）

## Run 与 RunStream

```go
func (a *Agent) Run(ctx context.Context, messages ...Message) (*AgentResult, error)
func (a *Agent) RunStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)
```

两者是同一实现的两条路径：`Run` 等价于 `RunStream(ctx, nil, ...)`。

- **`Run`**：非流式。内部走一次性请求（`Client.Create`），阻塞到整个循环结束，只返回最终的 `AgentResult`。适合后台任务、脚本、测试。
- **`RunStream`**：流式。每个事件（轮次开始/结束、思考增量、正文增量、工具调用、工具结果）在发生时实时推给 `onEvent`；`onEvent` 传 `nil` 时行为与 `Run` 一致。适合 CLI/服务端实时输出。事件类型清单见[流式事件](streaming.md)。

注意事项：

- 至少需要一条输入消息，否则立即返回错误（`agent run requires at least one input message`）。
- `messages` 可以是多条（例如恢复的历史 + 新问题）；系统提示词由 agent 自动拼在最前面，不要手动重复传入。
- 需要跨多次调用维护历史时使用 [Session](session.md)（`agent.Session()`），不要在每次 `Run` 手动拼接历史。
- 两种调用都受 `ctx` 控制：取消/超时会优雅停止——进行中的上游请求立即断开、不再开启新一轮、未执行的工具调用合成 `IsError` 结果保持配对完整，且返回**非 nil 的部分结果**（见下文 MaxTurnsError 一节同款用法）。

## AgentResult 与停止原因

```go
type AgentResult struct {
    FinalText  string    // 模型的最终回答；未正常完成时为空或部分文本
    Messages   []Message // 本次运行的完整轨迹：输入消息 + 每条 assistant 消息与工具结果
    Usage      Usage     // 跨所有轮次累计的 token 用量
    Turns      int       // 实际执行的模型调用轮数
    StopReason string    // AgentCompleted 或 AgentMaxTurns
}
```

停止原因常量：

| 常量 | 值 | 含义 |
|---|---|---|
| `AgentCompleted` | `"completed"` | 模型给出了不含工具调用的最终回答，循环正常结束 |
| `AgentMaxTurns` | `"max_turns"` | 达到轮数上限仍未得到最终回答（此时返回 `*MaxTurnsError`） |

`Usage` 字段：`InputTokens / OutputTokens / ReasoningTokens / CacheReadTokens / CacheWriteTokens`，为所有轮次之和。

`Messages` 不包含系统提示词，只含输入与本次循环产生的消息，可直接交给 `Session.SetHistory` 或 JSON 序列化持久化。消息结构见[消息模型](messages.md)。

## 审批钩子：ToolCallHook

```go
type ToolCallHook func(ctx context.Context, call ToolCall) (ToolDecision, error)

callable.Approve()                 // 按原样执行
callable.Deny("原因")              // 否决；原因作为 IsError 工具结果回传给模型
callable.ReplaceArgs(`{"k":"v"}`)  // 放行，但用新的 JSON 参数执行
```

- 钩子在**每次工具执行前**调用，拿到完整的 `ToolCall{Name, Arguments}`（`Arguments` 是原始 JSON 字符串）。
- `Deny` 不中断循环：模型收到 `"Tool call denied: <原因>"` 错误结果后通常会调整策略。
- `ReplaceArgs` 可用于强制收敛参数（例如把高危路径改写为沙箱路径）。
- 钩子自身返回 error 会**中断整个运行**——这是审批系统故障时的安全出口。
- 事件顺序：被批准（含改参）的调用先触发 `ToolCallEvent` 再执行；被否决的调用只触发 `ToolResultEvent`。

### 示例：高危操作人工确认

对只读工具直接放行，对写操作要求人工在终端确认：

```go
agent := callable.NewAgent(client,
    callable.WithTools(readFile, writeFile, deleteFile),
    callable.WithToolCallHook(func(ctx context.Context, call callable.ToolCall) (callable.ToolDecision, error) {
        switch call.Name {
        case "read_file":
            return callable.Approve(), nil // 只读操作直接放行
        }

        // 高危操作：展示调用细节，等待人工确认
        fmt.Printf("\n⚠️  模型请求执行 %s(%s)\n允许？[y/n] ", call.Name, call.Arguments)
        var answer string
        if _, err := fmt.Scanln(&answer); err != nil {
            return callable.ToolDecision{}, err // 输入失败：中断运行
        }
        if answer != "y" && answer != "Y" {
            return callable.Deny("用户拒绝了该操作"), nil // 模型会收到拒绝原因
        }
        return callable.Approve(), nil
    }),
)
```

## 并行工具执行

模型一轮可能返回多个工具调用。默认**顺序执行**（确定性、便于审计）；开启并行后同一轮内的调用并发执行：

```go
callable.WithParallelToolExecution(true)
```

行为细节：

- 结果顺序与模型发出的调用顺序一致（与执行快慢无关），回传给模型的消息不受影响。
- 审批钩子同样在并发 goroutine 中运行：若钩子涉及交互式确认或共享状态，需自行加锁/串行化。
- 顺序模式下前一个调用的钩子 error 会阻止后续调用执行；并行模式下所有调用都会启动，第一个 error 在全部结束后上报。
- 仅当你确定工具之间无副作用依赖（例如多个独立的只读查询）时才建议开启。

## MaxTurnsError 与部分结果

触达 `WithMaxTurns` 上限时，`Run`/`RunStream` 返回 `*MaxTurnsError`，其 `Partial` 字段携带截至目前的完整部分结果：

```go
result, err := agent.Run(ctx, callable.User("..."))

var mte *callable.MaxTurnsError
if errors.As(err, &mte) {
    // mte.Turns 是配置的上限；mte.Partial 与直接返回的 result 是同一个对象
    fmt.Println("已达上限，部分轨迹轮数:", mte.Partial.Turns)
    fmt.Println("已消耗 tokens:", mte.Partial.Usage.InputTokens, "/", mte.Partial.Usage.OutputTokens)
    // mte.Partial.Messages 仍是可重放的有效轨迹
}
```

要点：

- `Partial` 非 nil，且 `Partial.StopReason == AgentMaxTurns`；`Partial.FinalText` 为空（没有最终回答）。
- 部分 `Messages` 中每个 tool_call 都有配对的 tool_result，可以直接作为历史继续对话。
- context 取消/超时的处理与之类似：返回的 `result` 非 nil，`FinalText` 为已生成的部分文本，错误满足 `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded`。
- 更多错误类型见[错误处理](errors.md)。

## 配置层级

同一参数可在三层配置，生效优先级为：**Request > Agent > Client**。

| 层级 | 设置方式 | 作用范围 |
|---|---|---|
| Client | `WithModel` / `WithMaxTokens` / `WithTemperature` | 该 client 发出的所有请求 |
| Agent | `WithThinking`（以及工具列表、系统提示词） | 该 agent 的每次运行、每一轮 |
| Request | `NewRequest(...).WithModel(...).WithThinking(...).WithMaxTokens(...)...` | 单次请求 |

agent loop 每轮内部构造一个 `Request` 并套用 agent 级配置（思考模式、工具），再交给 client 补齐模型等默认值；最终未指定的字段由 provider 决定。低层直调 `client.Create/Stream` 时可以在单个 `Request` 上覆盖 agent 与 client 的设置。

## 完整示例

一个带天气工具、审批日志、流式输出的 agent（与 `examples/tools/main.go` 一致）：

```go
package main

import (
    "context"
    "fmt"
    "os"

    callable "github.com/great-magician-01/callable"
)

// WeatherArgs 通过反射生成工具的 JSON Schema
type WeatherArgs struct {
    City string `json:"city" jsonschema:"description=城市名称，例如：北京"`
    Unit string `json:"unit,omitempty" jsonschema:"description=温度单位,enum=celsius,enum=fahrenheit"`
}

func main() {
    ctx := context.Background()
    client := callable.NewClient(
        callable.NewOpenAIProvider(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL),
        callable.WithModel("gpt-5"),
    )

    weather := callable.NewTool("get_weather", "查询指定城市的实时天气",
        func(ctx context.Context, args WeatherArgs) (any, error) {
            // 实际项目中替换为真实 API 调用
            return fmt.Sprintf(`{"city":%q,"temp":26,"unit":"°C","condition":"晴"}`, args.City), nil
        })

    agent := callable.NewAgent(client,
        callable.WithSystemPrompt("你是一个务实的天气助手。"),
        callable.WithTools(weather),
        callable.WithMaxTurns(10),
        // 审批钩子：每次执行前打印日志
        callable.WithToolCallHook(func(ctx context.Context, call callable.ToolCall) (callable.ToolDecision, error) {
            fmt.Printf("\n[tool call] %s(%s)\n", call.Name, call.Arguments)
            return callable.Approve(), nil
        }),
    )

    result, err := agent.RunStream(ctx, func(ev callable.Event) {
        switch e := ev.(type) {
        case callable.ThinkingDeltaEvent:
            fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // 思考内容置灰
        case callable.TextDeltaEvent:
            fmt.Print(e.Delta)
        case callable.ToolResultEvent:
            fmt.Fprintf(os.Stderr, "\n[tool result] %s -> %s\n", e.Call.Name, e.Result.Content)
        }
    }, callable.User("北京和上海现在多少度？用摄氏度。"))
    if err != nil {
        fmt.Fprintln(os.Stderr, "error:", err)
        os.Exit(1)
    }

    fmt.Fprintf(os.Stderr, "\n[turns: %d, stop: %s, tokens: in %d / out %d]\n",
        result.Turns, result.StopReason, result.Usage.InputTokens, result.Usage.OutputTokens)
}
```

## 注意事项

- `WithMaxTurns` 默认 25 已经足够大，但对可能陷入工具循环的任务（模型反复调用同一工具）仍建议显式设置并处理 `MaxTurnsError`。
- `Agent` 本身无状态、可并发复用；跨调用的历史由 [Session](session.md) 维护。
- 正在执行中的工具函数会收到同一个 `ctx`，工具实现应自行响应取消——Go 无法强制杀死无视 ctx 的 goroutine。
- 系统提示词由 agent 自动组装（基础提示词 + skill 索引 + 子代理索引），传入 `Run` 的消息里不要再带 `System(...)`。
