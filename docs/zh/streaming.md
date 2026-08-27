# 流式输出与事件

**中文** | [English](../en/streaming.md)

callable 的所有流式能力都围绕一个统一的事件模型：无论底层是 OpenAI Chat Completions、OpenAI Responses 还是 Anthropic Messages，事件类型、字段、顺序完全一致。事件分两个层级：

- **provider 层**：一次模型调用内部的事件（消息开始/结束、文本增量、工具调用增量），由 `client.Stream` 产生
- **agent 层**：agent loop 每轮的事件（轮次开始/结束、工具执行、循环结束），由 `agent.RunStream` 在 provider 层事件之外额外产生

两个入口共用同一个回调签名 `func(callable.Event)`，同一个 type switch 可以同时处理两个层级的事件。

## API 签名

```go
// 单次流式模型调用：只产生 provider 层事件，返回拼装好的完整响应
func (c *Client) Stream(ctx context.Context, req *Request, onEvent func(Event)) (*Response, error)

// 流式运行 agent loop：provider 层 + agent 层全部事件，返回累计结果
func (a *Agent) RunStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)

// Session 的流式版本，等价于带历史记录的 RunStream
func (s *Session) AskStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)
```

- `onEvent` 传 `nil` 即退化为非流式（`agent.Run` 内部就是 `RunStream(ctx, nil, ...)`），此时 agent 走非流式请求，不产生任何事件。
- 回调是**同步**的：事件在调用方的 goroutine（即调用 `Stream` / `RunStream` 的 goroutine）中逐个触发，回调返回后才继续处理后续数据。不要在回调里做阻塞操作（如慢速 IO），否则会拖慢整个流；需要异步处理时请自行转发到 channel。
- 例外：开启 `WithParallelToolExecution(true)` 后，同一轮内多个工具的 `ToolCallEvent` / `ToolResultEvent` 会**从并发 goroutine 触发**，此时回调必须自行保证并发安全（加锁或用 channel 收集）。
- 回调不返回错误。想中途停止流式，请取消传入的 `context.Context`（见[错误处理](errors.md)）。

## 事件类型完整表

`Event` 是封闭接口（sealed interface），全部实现如下。事件按值传递（非指针）。

### provider 层事件（`client.Stream` 与 `agent.RunStream` 都会产生）

| 事件 | 载荷字段 | 触发时机 |
|---|---|---|
| `MessageStartEvent` | （无字段） | 一条 assistant 消息开始生成，每次模型调用的第一个事件 |
| `ThinkingDeltaEvent` | `Delta string` | 思考/推理文本增量；仅在开启思考模式且模型实际产出推理时触发，见[思考模式](thinking.md) |
| `TextDeltaEvent` | `Delta string` | 正文文本增量，逐 token（或逐片段）触发 |
| `ToolCallDeltaEvent` | `Index int` `ID string` `Name string` `ArgsDelta string` | 流式工具调用的增量。`ID`/`Name` 只在该工具调用的首个增量上设置；`ArgsDelta` 携带参数 JSON 片段，按顺序拼接即为完整参数。`Index` 标识本轮内第几个工具调用（0 起），模型一次请求多个工具时用它区分 |
| `MessageDoneEvent` | `Message` `Usage` `StopReason` | 一次模型调用结束，携带拼装好的完整消息（含 `ThinkingPart` / `ToolCallPart`）、本轮 token 用量和停止原因 |

`MessageDoneEvent.StopReason` 取值为 `StopReasonEndTurn` / `StopReasonToolCalls` / `StopReasonMaxTokens` / `StopReasonOther`。`StopReasonToolCalls` 表示模型要求执行工具——在 agent loop 中这会触发后续的 `ToolCallEvent` / `ToolResultEvent` 和新一轮。

### agent 层事件（仅 `agent.RunStream` / `Session.AskStream` 产生）

| 事件 | 载荷字段 | 触发时机 |
|---|---|---|
| `TurnStartEvent` | `Turn int` | 第 `Turn` 轮开始（1 起）。一轮 = 一次模型调用 + 它请求的全部工具执行 |
| `TurnEndEvent` | `Turn int` | 第 `Turn` 轮结束（工具全部执行完毕之后，或模型给出最终回答之后） |
| `ToolCallEvent` | `Call ToolCall` | 工具即将执行：**ToolCallHook 批准之后、实际执行之前**触发。`Call` 是完整的工具调用（`ID` / `Name` / `Arguments` 原始 JSON）。被 hook 否决的调用不会触发此事件 |
| `ToolResultEvent` | `Call ToolCall` `Result ToolResult` | 工具执行完毕后触发。`Result.Content` 是回传给模型的内容，`Result.IsError` 表示执行失败（错误会作为工具结果回传模型，不中断循环）。被 hook 否决或因取消被跳过的调用也会触发此事件（`IsError=true`） |
| `AgentDoneEvent` | `Result *AgentResult` | agent loop 正常结束（模型给出最终回答）时触发，`Result` 与返回值是同一个对象。**达到 max turns 或出错/取消时不触发**，通过返回值/错误判断 |
| `SubAgentEvent` | `SubAgent string` `Event Event` | 包装子代理 loop 内部产生的事件，`SubAgent` 是子代理名。仅在父 agent 开启 `WithSubAgentEvents(true)` 时产生，见[子代理](subagents.md) |

## 典型事件序列

### 纯文本一轮（开启思考）

`client.Stream` 与 `agent.RunStream`（一轮即结束）的 provider 层部分相同：

```
[agent]    TurnStartEvent{Turn: 1}
[provider] MessageStartEvent{}
[provider] ThinkingDeltaEvent{Delta: "用户问的是"} × N
[provider] TextDeltaEvent{Delta: "北京"} × N
[provider] MessageDoneEvent{Message: ..., Usage: {Input: 12, Output: 30}, StopReason: end_turn}
[agent]    TurnEndEvent{Turn: 1}
[agent]    AgentDoneEvent{Result: ...}
```

### 含工具调用的多轮

模型第一轮请求工具，第二轮给出最终回答：

```
TurnStartEvent{Turn: 1}
MessageStartEvent{}
ThinkingDeltaEvent × N                     // 可选
TextDeltaEvent × N                         // 可选：工具调用前的引导文本
ToolCallDeltaEvent{Index: 0, ID: "call_1", Name: "get_weather", ArgsDelta: `{"city":`}
ToolCallDeltaEvent{Index: 0, ArgsDelta: `"北京"}`}
MessageDoneEvent{..., StopReason: tool_calls}
ToolCallEvent{Call: {ID: "call_1", Name: "get_weather", Arguments: `{"city":"北京"}`}}
ToolResultEvent{Call: ..., Result: {Content: "北京 当前 26°C，晴"}}
TurnEndEvent{Turn: 1}
TurnStartEvent{Turn: 2}
MessageStartEvent{}
TextDeltaEvent × N                         // 最终回答
MessageDoneEvent{..., StopReason: end_turn}
TurnEndEvent{Turn: 2}
AgentDoneEvent{Result: ...}
```

注意 `ToolCallDeltaEvent` 只在首个增量携带 `ID` 和 `Name`；增量参数拼起来等于后面 `ToolCallEvent.Call.Arguments`。多数场景下可以忽略 `ToolCallDeltaEvent`，直接消费 `ToolCallEvent` 的完整调用。

### 子代理事件

开启 `WithSubAgentEvents(true)` 且模型委派子代理时，子代理 loop 的每个事件被包装后转发：

```
ToolCallEvent{Call: {Name: "call_translator", ...}}
SubAgentEvent{SubAgent: "translator", Event: TurnStartEvent{Turn: 1}}
SubAgentEvent{SubAgent: "translator", Event: MessageStartEvent{}}
SubAgentEvent{SubAgent: "translator", Event: TextDeltaEvent{Delta: "..."}}
...
SubAgentEvent{SubAgent: "translator", Event: AgentDoneEvent{...}}
ToolResultEvent{Call: ..., Result: ...}
```

消费端可以用嵌套 type switch 区分主/子输出，例如给子代理文本加前缀或路由到不同的 UI 区域。

## type switch 处理模式

事件按值传递，case 写值类型。一个覆盖全部事件类型的处理器：

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/great-magician-01/callable"
)

func main() {
	client := callable.NewClient(
		callable.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), callable.AnthropicURL),
		callable.WithModel("claude-sonnet-5"),
	)

	type WeatherArgs struct {
		City string `json:"city" jsonschema:"description=城市名称，如：北京"`
	}
	weather := callable.NewTool("get_weather", "查询指定城市的实时天气",
		func(ctx context.Context, args WeatherArgs) (any, error) {
			return fmt.Sprintf("%s 当前 26°C，晴", args.City), nil
		})

	agent := callable.NewAgent(client,
		callable.WithTools(weather),
		callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
	)

	onEvent := func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.TurnStartEvent:
			fmt.Printf("\n── 第 %d 轮 ──\n", e.Turn)
		case callable.MessageStartEvent:
			// 一条 assistant 消息开始，一般无需处理
		case callable.ThinkingDeltaEvent:
			fmt.Printf("\x1b[90m%s\x1b[0m", e.Delta) // 思考：灰色显示
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta) // 正文增量，直接输出
		case callable.ToolCallDeltaEvent:
			// 工具参数流式增量；只关心完整调用可忽略，等 ToolCallEvent
		case callable.MessageDoneEvent:
			fmt.Printf("\n[本轮用量: 输入 %d / 输出 %d / 思考 %d]\n",
				e.Usage.InputTokens, e.Usage.OutputTokens, e.Usage.ReasoningTokens)
		case callable.ToolCallEvent:
			fmt.Printf("\n>>> 调用工具 %s(%s)\n", e.Call.Name, e.Call.Arguments)
		case callable.ToolResultEvent:
			if e.Result.IsError {
				fmt.Printf("<<< 工具失败: %s\n", e.Result.Content)
			} else {
				fmt.Printf("<<< 工具返回: %s\n", e.Result.Content)
			}
		case callable.TurnEndEvent:
			// 本轮结束
		case callable.SubAgentEvent:
			fmt.Printf("[子代理 %s] ", e.SubAgent) // 再按 e.Event 内层分发
		case callable.AgentDoneEvent:
			fmt.Printf("\n[完成，共 %d 轮，累计 %d 输入 token]\n",
				e.Result.Turns, e.Result.Usage.InputTokens)
		}
	}

	result, err := agent.RunStream(context.Background(), onEvent, callable.User("北京天气怎么样？"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	_ = result // 与 AgentDoneEvent.Result 是同一个对象
}
```

低层用法（不用 agent loop）只需要处理 provider 层事件：

```go
resp, err := client.Stream(ctx, callable.NewRequest(callable.User("讲个故事")),
	func(ev callable.Event) {
		if d, ok := ev.(callable.TextDeltaEvent); ok {
			fmt.Print(d.Delta)
		}
	})
// resp 是拼装好的完整响应：resp.Text / resp.Message / resp.Usage / resp.StopReason
```

`Stream` 返回的 `Response` 与流中事件的关系：所有 `TextDeltaEvent.Delta` 拼接等于 `resp.Text`，`MessageDoneEvent.Message` 等于 `resp.Message`。所以可以边流式展示边在结束后拿到完整结构化结果。

## Usage 字段说明

```go
type Usage struct {
	InputTokens      int // 输入 token（prompt 部分）
	OutputTokens     int // 输出 token
	ReasoningTokens  int // 其中用于思考/推理的 token（OpenAI 系列报告；Anthropic 通常为 0）
	CacheReadTokens  int // 命中 prompt 缓存的输入 token（Anthropic cache_read_input_tokens）
	CacheWriteTokens int // 写入 prompt 缓存的输入 token（Anthropic cache_creation_input_tokens）
}
```

- 不同 provider 报告的口径不同：不报告的字段保持 0。例如 Anthropic 报告 cache 字段，OpenAI 报告 `ReasoningTokens`。
- `MessageDoneEvent.Usage` 与 `Response.Usage` 是**单轮**用量。
- agent loop 每轮结束后把该轮 `Usage` 累加进 `AgentResult.Usage`（`AgentDoneEvent.Result.Usage`），即跨轮累计值。多轮工具调用场景下，累计输入 token 会远大于单轮——每轮都要把完整对话历史重新发给模型。
- `ReasoningTokens` 通常是 `OutputTokens` 的子集，不要把两者相加当总输出。

## 注意事项与边界情况

- **取消时没有 `MessageDoneEvent`**：流式中途取消 context，provider 返回部分拼装好的 `*Response`（非 nil）和匹配 `errors.Is(err, context.Canceled)` 的错误，但不触发 `MessageDoneEvent` / `TurnEndEvent` / `AgentDoneEvent`。agent 层会把已生成的部分文本和用量放进返回的 `AgentResult`（该轮不完整的消息**不会**写入 `Messages`，保证轨迹可重放）。
- **出错/超轮不触发 `AgentDoneEvent`**：达到 max turns 返回 `*MaxTurnsError`（`Partial` 携带部分结果），provider 错误返回 `*APIError`。判断结束要用返回值和错误，而不是只等 `AgentDoneEvent`。详见[错误处理](errors.md)。
- **回调是同步且有序的**（默认顺序执行工具时）：事件严格按发生顺序触发。开启 `WithParallelToolExecution(true)` 后工具事件并发触发，回调需并发安全，且同一轮内 `ToolCallEvent`/`ToolResultEvent` 的相对顺序不再确定（`Call.ID` 可用于配对）。
- **被 hook 否决的工具**：不触发 `ToolCallEvent`，但会触发 `ToolResultEvent`（`IsError=true`，内容为否决原因）。因此消费端不应假设两者一一成对出现。
- **`ThinkingDeltaEvent` 与 `TextDeltaEvent` 交错**：开启思考后，思考增量先于正文增量出现，但两者都属于同一条消息；`MessageDoneEvent.Message.Parts` 中对应为 `ThinkingPart` 和 `TextPart`。
- **`onEvent` 传 nil**：不产生任何事件，agent 内部改用非流式请求（更省解析开销）。

## 相关文档

- [Agent 循环](agent.md) — RunStream 所在的完整工具循环
- [多轮会话](session.md) — Session.AskStream
- [工具](tools.md) — ToolCall / ToolResult 结构与审批钩子
- [思考模式](thinking.md) — ThinkingDeltaEvent 的来源
- [子代理](subagents.md) — SubAgentEvent 与事件透传
- [错误处理](errors.md) — 取消、重试与部分结果
