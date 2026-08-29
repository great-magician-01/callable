# 多轮会话（Session）

**中文** | [English](../en/session.md)

`Session` 维护跨多次调用的对话历史：每轮 Ask 自动把之前的消息（含 thinking 块和工具调用轨迹）带上发给模型，结束后把新产生的消息追加进历史。它是对 [Agent 循环](agent.md) 的一层薄封装——所有 agent 能力（工具、思考模式、skill、图片、流式事件）在会话中照常工作，只是上下文由 Session 替你管理。

Session 与 provider 无关：同一份历史可以发给 OpenAI Chat Completions、OpenAI Responses 或 Anthropic，各家所需的回传数据（Anthropic 的 thinking signature、Responses 的 reasoning item、DeepSeek/GLM 的 `reasoning_content`）都会原样保留并在发送时转换。

## API 一览

```go
sess := agent.Session()

result, err := sess.Ask(ctx, callable.User("你好"))
result, err := sess.AskStream(ctx, onEvent, callable.User("继续"))

sess.ID()                    // 会话 ID（sess- 前缀，创建时生成）
data, err := sess.Snapshot() // 整个会话（id + 历史 + 上下文用量）序列化为 JSON
err = sess.Restore(data)     // 从快照恢复

history := sess.History()       // []callable.Message 的副本
sess.SetHistory(restored)       // 整体替换历史（如手工构造上下文）
sess.Reset()                    // 清空历史
```

| 方法 | 签名 | 说明 |
|---|---|---|
| `Agent.Session` | `func (a *Agent) Session(opts ...SessionOption) *Session` | 在该 agent 上创建新会话，历史为空。一个 agent 可同时挂多个独立会话；`opts` 见下文「上下文窗口与历史压缩」 |
| `Session.Ask` | `func (s *Session) Ask(ctx context.Context, messages ...Message) (*AgentResult, error)` | 把消息追加到历史尾部并运行 agent loop（非流式），等价于 `agent.Run`，只是输入自动带上历史 |
| `Session.AskStream` | `func (s *Session) AskStream(ctx context.Context, onEvent func(Event), messages ...Message) (*AgentResult, error)` | 同上，并把全部流式事件转发给 `onEvent`（见[流式事件](streaming.md)） |
| `Session.ID` | `func (s *Session) ID() string` | 会话 ID（`sess-` 前缀），创建时生成、跨 Ask 稳定；`Reset` 不清除，`Restore` 恢复为快照中的 id。该会话的所有事件与 `AgentResult` 都携带它 |
| `Session.History` | `func (s *Session) History() []Message` | 返回当前对话历史的**副本**（不含 system prompt），修改返回值不影响会话 |
| `Session.SetHistory` | `func (s *Session) SetHistory(messages []Message)` | 整体替换历史（内部拷贝入参），用于恢复持久化的会话或手工构造上下文 |
| `Session.Snapshot` | `func (s *Session) Snapshot() ([]byte, error)` | 把会话状态（id + 历史 + 上下文用量）序列化为 JSON，用于持久化。配置项（context window、auto-compact）不在快照里 |
| `Session.Restore` | `func (s *Session) Restore(data []byte) error` | 从 `Snapshot` 产出的快照恢复（替换 id、历史与上下文用量）；无 id 的快照报错。恢复后需重新用 `SessionOption` 设置配置项 |
| `Session.Reset` | `func (s *Session) Reset()` | 清空历史与上下文用量，会话回到刚创建时的状态 |
| `Session.Compact` | `func (s *Session) Compact(ctx context.Context) (string, error)` | 手动压缩历史：用模型生成摘要并替换整段历史，返回摘要。空历史为 no-op，失败时历史不变 |
| `Session.ContextWindow` | `func (s *Session) ContextWindow() int` | 配置的上下文窗口大小（token），默认 1,000,000 |
| `Session.ContextUsage` | `func (s *Session) ContextUsage() Usage` | 最近一次 Ask 最后一轮的 token 用量（`ContextTokens` 为当时的上下文占用量） |
| `Session.ContextFillRatio` | `func (s *Session) ContextFillRatio() float64` | 上下文占用比例：`ContextTokens / ContextWindow` |

返回值 `*AgentResult` 与 `agent.Run` 一致：`FinalText`（最终回答）、`Messages`（本轮完整轨迹：输入消息 + 所有 assistant / tool 消息）、`Usage`（跨轮累计）、`LastTurnUsage`（最后一轮的用量，其 `ContextTokens` 即当前上下文占用量）、`Turns`、`StopReason`（`AgentCompleted` / `AgentMaxTurns`）。

## 会话 ID

每个会话在创建时生成一个随机 ID（`sess-` 前缀），`Session.ID()` 返回它。ID 在多次 Ask 之间保持稳定，`Reset` 不会重置它，`Restore` 会把它替换为快照中保存的 id。

这个 ID 用于标识事件与结果的归属：`AskStream` 的每个流式事件都带有 `ConversationID` 字段，值就是会话 ID；`AgentResult.ConversationID` 同样携带它。多个会话共用一个事件回调、或需要把事件关联到日志/计费记录时，用它区分来源。

裸 `agent.Run` / `RunStream`（不经过会话）每次运行生成一个 `run-` 前缀的新 ID；更底层的 `client.Create` / `Stream` 不产生 ID（事件 `ConversationID` 为空字符串）。详见[流式事件](streaming.md)。

## 基本用法

```go
client := callable.NewClient(
    callable.NewAnthropicProvider(apiKey, callable.AnthropicURL),
    callable.WithModel("claude-sonnet-5"),
)
agent := callable.NewAgent(client,
    callable.WithSystemPrompt("你是一个务实的助手"),
    callable.WithTools(weather),
    callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
)

session := agent.Session()
questions := []string{
    "一个三位数，各位数字之和为 12，且数字互不相同，这样的数最多有几个？",
    "把你的推理过程总结成两句话。", // 模型能看到上一轮的完整问答
}

for _, q := range questions {
    _, err := session.AskStream(ctx, func(ev callable.Event) {
        switch e := ev.(type) {
        case callable.ThinkingDeltaEvent:
            fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // 思考增量
        case callable.TextDeltaEvent:
            fmt.Print(e.Delta) // 正文增量
        }
    }, callable.User(q))
    if err != nil {
        log.Fatal(err)
    }
}

fmt.Println("历史消息数:", len(session.History()))
```

（完整可运行版本见 `examples/thinking/main.go`。）

## 工作原理

- 每次 Ask 时，Session 把「已有历史 + 本次输入消息」拼成完整输入交给 agent loop；system prompt 由 agent 在每次请求时注入，**不进入**历史。
- 运行**成功**（`err == nil`）后，`result.Messages` 成为新的历史——即旧历史 + 本次输入 + 本轮所有 assistant 消息（含 thinking 与 tool_call）+ 所有 tool 结果消息。下一轮 Ask 时全部回传。
- 一轮 Ask 可能包含多个 loop 轮次（模型调工具 → 工具结果回传 → 再问模型），这些中间消息同样完整保留在历史里。

## 历史持久化：Snapshot / Restore

`Session.Snapshot()` 把整个会话状态（会话 id + 完整历史 + 上下文用量）序列化为 JSON，`Session.Restore(data)` 在新会话上原样恢复。`Message` 及其所有 Part 类型都实现了 JSON 序列化（含 provider 回传数据，见下一节），所以跨进程续聊只需两行：

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    callable "github.com/great-magician-01/callable"
)

const snapshotFile = "session.json"

func main() {
    ctx := context.Background()

    client := callable.NewAnthropicClient(os.Getenv("ANTHROPIC_API_KEY"), callable.AnthropicURL, "claude-sonnet-5")
    agent := callable.NewAgent(client,
        callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
    )
    session := agent.Session(
        callable.WithContextWindow(200_000), // 配置项不在快照里，恢复后需重新设置
    )

    // 恢复上次的会话（如有）：id、历史、上下文用量一起回来
    if data, err := os.ReadFile(snapshotFile); err == nil {
        if err := session.Restore(data); err != nil {
            log.Fatal(err)
        }
    }

    result, err := session.Ask(ctx, callable.User("我们上次聊到哪儿了？"))
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.FinalText)

    // 持久化更新后的会话，供下次运行恢复
    data, err := session.Snapshot()
    if err != nil {
        log.Fatal(err)
    }
    if err := os.WriteFile(snapshotFile, data, 0o644); err != nil {
        log.Fatal(err)
    }
}
```

细节：

- 快照 JSON 形如 `{"id": "sess-…", "history": […], "context_usage": {…}}`，其中 `history` 的每条消息格式与直接序列化 `History()` 的结果一致（见下文）。
- 配置项（context window、auto-compact 阈值等）不在快照里——它们属于运行参数，恢复后按需重新用 `SessionOption` 设置。
- `Restore` 要求快照带有 id（即必须产自 `Snapshot()`）；手工拼接的、没有 id 的 JSON 会报错。

如果只想持久化/恢复历史本身（比如要先裁剪或检查历史内容），仍可以走手工路线——`History()` 的结果直接 `json.Marshal`，反序列化后用 `SetHistory` 恢复：

```go
data, _ := json.Marshal(session.History()) // 历史本身是普通 JSON
var history []callable.Message
_ = json.Unmarshal(data, &history)
session.SetHistory(history) // 只恢复历史，不带会话 id 与上下文用量
```

序列化后的每条消息形如：

```json
{
  "role": "assistant",
  "parts": [
    {"type": "thinking", "text": "……", "signature": "EgkBCk…"},
    {"type": "tool_call", "id": "toolu_01…", "name": "get_weather", "arguments": "{\"city\":\"北京\"}"},
    {"type": "text", "text": "北京当前 26°C，晴。"}
  ],
  "provider_extra": { "openai-responses": [ /* reasoning item 原文 */ ] }
}
```

其中 `type` 是 Part 的判别字段（`text` / `image` / `thinking` / `tool_call` / `tool_result`），反序列化时自动还原为具体类型。细节见[消息模型](messages.md)。

## thinking 块与工具轨迹的保留

历史保真是 Session 的核心价值，缺少任何一块都会导致 provider 报错或模型"失忆"：

- **ThinkingPart 原样保留**：思考文本连同 Anthropic 的 `signature`（加密签名，不回传会 400）、OpenAI Responses 的 reasoning item id 都会存在历史里。
- **provider 原始数据随行**：无法塞进统一模型的原始负载（如 Responses 的完整 reasoning item）通过 `Message.SetProviderExtra` 挂在消息上，按 provider 名索引，序列化时落在 `provider_extra` 字段里，跨进程持久化不丢。
- **工具轨迹完整配对**：assistant 消息里的每个 `tool_call` 都有对应 `tool` 消息里的 `tool_result`（按 ID 配对）。取消时未执行的工具会被合成 `IsError` 结果补齐，保证部分轨迹也是可重放的有效会话。
- **国产端点兼容**：DeepSeek/GLM 的 `reasoning_content`、Qwen 的思考内容同样保存在 ThinkingPart 中并在下轮回传。

因此：不要自己裁剪或改写历史里的 assistant / tool 消息。如果必须手工构造历史（`SetHistory`），请保证每个 tool_call 都有配对的 tool_result，否则各家 API 会拒绝请求。

## 取消与出错不污染历史

Session 只在运行**完全成功**（`err == nil`）时才更新历史。以下情况历史保持不变，可安全重试或换个问题继续问：

- `ctx` 被取消或超时（错误满足 `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded`）
- 网络错误、`APIError`（含重试耗尽后）
- `MaxTurnsError`（loop 达到轮数上限——注意它虽然带有 `Partial` 部分结果，但仍属于错误，历史不更新）

之所以这样设计：中止的运行可能停在一条"发起了 tool_call 但结果未回传"的 assistant 消息上，写入历史后下次请求会被 provider 拒绝。中止时的部分文本仍可通过返回的 `*AgentResult`（非 nil）拿到，只是不进历史：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

before := len(sess.History())
result, err := sess.Ask(ctx, callable.User("写一篇长文"))
if errors.Is(err, context.DeadlineExceeded) {
    // result 非 nil：result.FinalText 是已生成的部分文本
    // 但历史未变，下面这行成立：
    fmt.Println(len(sess.History()) == before) // true
    // 可以直接重试：
    result, err = sess.Ask(context.Background(), callable.User("写一篇长文"))
}
```

## 上下文窗口与历史压缩（Compact）

历史只增不减，长会话终究会逼近模型的上下文上限。Session 可以跟踪上下文占用量，并在达到阈值时自动压缩历史，也可以随时手动压缩。

### 跟踪上下文用量

`agent.Session()` 接受若干 `SessionOption`：

| Option | 说明 |
|---|---|
| `WithContextWindow(tokens int)` | 上下文窗口大小（token），默认 `DefaultContextWindow` = 1,000,000；非正值忽略 |
| `WithAutoCompact(enabled bool)` | 开启自动压缩，默认关闭 |
| `WithAutoCompactThreshold(ratio float64)` | 自动压缩触发的上下文占用比例，取值 (0, 1]，默认 `DefaultAutoCompactThreshold` = 0.6；越界值忽略 |

每次 Ask 成功后，Session 记录**最后一轮**模型调用的用量：

```go
sess := agent.Session(callable.WithContextWindow(200_000))
result, err := sess.Ask(ctx, callable.User("..."))

sess.ContextUsage()      // 最后一轮的 Usage
sess.ContextFillRatio()  // ContextTokens / ContextWindow，例如 0.45
result.LastTurnUsage     // 同一个值，也可从结果上取
```

这里的关键是 `Usage.ContextTokens`：本次请求实际占用上下文的 token 总量，已按 provider 归一化——OpenAI 系取 `prompt_tokens`（本身包含缓存部分），Anthropic 取 `input_tokens + cache_read + cache_creation`。与按调用累加的 `Usage` 不同，它回答的是"现在上下文占了多满"。provider 不上报 usage 时该值为 0。

### 自动压缩

开启后，每次 Ask 结束时若 `ContextFillRatio() >= 阈值`，Session 自动执行一次压缩：

```go
sess := agent.Session(
    callable.WithContextWindow(200_000),
    callable.WithAutoCompact(true),
    callable.WithAutoCompactThreshold(0.7), // 可选，默认 0.6
)
```

- 自动压缩是 **best-effort**：压缩调用失败不会让这次 Ask 报错，历史保持原样。
- 压缩成功后 `ContextUsage()` 清零，下一次 Ask 重新测量。
- `AskStream` 在压缩成功时会收到 `SessionCompactEvent{Summary, TokensBefore}`（见[流式事件](streaming.md)）。
- **对子 agent 无效**：子代理在自己独立的 agent loop 里运行、不经过 Session，此配置不会影响它们。

### 手动压缩

随时可调 `Compact`，无视阈值立即压缩：

```go
summary, err := sess.Compact(ctx) // 空历史是 no-op；失败时历史不变
```

### 压缩做了什么

压缩把当前历史渲染成纯文本 transcript（thinking 块与图片以占位符表示），用 agent 的 client（同一 model，不带工具与 thinking 配置）调用一次模型生成摘要，然后把整段历史替换为一条 user 消息。这次总结调用走流式请求（长 transcript 的总结耗时较长，流式可以避免网关超时），流式增量被丢弃，对外行为不变：

```
[Conversation compacted] Summary of the earlier conversation:

<摘要>
```

因此压缩是不可逆的历史改写——thinking signature、provider 回传数据、原始工具轨迹都会被摘要取代。如有审计或恢复需求，请先通过 `History()` 自行备份。

## 注意事项

- **并发安全**：`Session` 可以在多个 goroutine 间共享——`Ask` / `AskStream` / `Compact` 会被串行化（进行中的 Ask 未结束时，第二个 Ask 排队等待），读取方法（`ID` / `History` / `ContextUsage` / `ContextFillRatio` / `Snapshot`）不会被进行中的 Ask 阻塞。需要真正并行的对话时，仍建议每个 goroutine 用独立会话。
- **空消息**：`Ask(ctx)`（不传消息）会在历史为空时报错 `agent run requires at least one input message`；历史非空时则相当于"让模型基于现有历史再说点什么"。
- **History 是副本**：`History()` 返回拷贝，追加消息请通过 `Ask` 或 `SetHistory`，直接改返回值无效。
- **一个 agent 多个会话**：`agent.Session()` 可多次调用，各会话历史互不影响；但所有会话共享 agent 的配置（工具、system prompt、思考模式）。
- **历史增长**：历史只增不减（除非 Reset / SetHistory / Compact），长会话要注意 token 成本——可以开启 `WithAutoCompact` 让会话自动压缩，或自行用 `SetHistory` 截断（截断时同样要保证工具调用配对完整）。
- **system prompt 不入历史**：`History()` / `SetHistory()` 只管理 user / assistant / tool 消息；system prompt 由 agent 配置（`WithSystemPrompt`、skill 索引等）在每次请求时注入，不参与持久化。

## 相关文档

- [Agent 循环](agent.md) — Ask 背后运行的 loop 机制
- [消息模型](messages.md) — Message / Part 结构与序列化格式
- [思考模式](thinking.md) — thinking 块的 provider 映射
- [流式事件](streaming.md) — AskStream 的事件类型
- [错误处理](errors.md) — APIError、MaxTurnsError 与取消语义
