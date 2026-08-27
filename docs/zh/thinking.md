# 思考模式

**中文** | [English](../en/thinking.md)

思考模式（thinking / reasoning / extended thinking）让模型在给出答案前先做一段内部推理。callable 用一个统一的 `Thinking` 结构描述思考配置，并在发送请求时翻译成各 provider 的原生控制字段——同一份代码无需改动即可在 Anthropic、OpenAI（Chat Completions / Responses）、GLM、DeepSeek、Qwen、火山方舟之间切换。

## API 一览

```go
type Thinking struct {
    Effort       Effort // EffortOff / EffortLow / EffortMedium / EffortHigh
    BudgetTokens int    // Anthropic / Qwen 的显式 token 预算
}

type Effort string

const (
    EffortOff    Effort = ""       // 关闭思考（零值）
    EffortLow    Effort = "low"    // 轻量推理
    EffortMedium Effort = "medium" // 均衡（推荐默认）
    EffortHigh   Effort = "high"   // 深度推理
)
```

配置入口有三个：

| 入口 | 作用范围 |
|---|---|
| `callable.WithThinking(t Thinking)`（Agent 选项） | Agent 的每一次模型调用 |
| `req.WithThinking(t Thinking)`（`*Request` 方法） | 单次请求 |
| `callable.WithSubAgentThinking(t Thinking)` | 子代理内部（见[子代理](subagents.md)） |

语义要点：

- `Request.Thinking` 是指针字段；不传（nil）表示“不发送任何思考控制字段”，与显式传 `Thinking{}`（零值，即 `EffortOff`）不同——后者表示**显式关闭**思考。这个区别对默认开启思考的 DeepSeek 很关键（见下文）。
- `Thinking.Enabled()` 当且仅当 `Effort != EffortOff || BudgetTokens > 0` 时为真。
- 只设置 `BudgetTokens` 而不设置 `Effort` 时，对所有 provider 都视为开启思考；对使用 effort 概念的 provider 等价于 `EffortMedium`。
- 开启思考时，采样温度（`Temperature`）不会被发送：Anthropic 要求思考模式下 temperature 为 1，OpenAI 系思考模型也会拒绝自定义 temperature。

## 各 provider 映射

统一的 `Effort` / `BudgetTokens` 在发送时被翻译成各端点的原生字段：

| Provider | 请求字段 | Effort 映射 | 备注 |
|---|---|---|---|
| Anthropic Messages | `thinking: {type: "enabled", budget_tokens: N}` | low → 2048，medium → 8192，high → 16384 | 下限 1024；自动保证 `max_tokens > budget` |
| OpenAI Responses | `reasoning: {effort: "low"\|"medium"\|"high", summary: "auto"}` | 直接映射 | `summary: "auto"` 让思考摘要随流式响应返回 |
| OpenAI Chat Completions | `reasoning_effort` | 直接映射 | 官方 OpenAI 端点使用 `max_completion_tokens` |
| GLM / 智谱（含 Z.AI） | `thinking: {type: "enabled"\|"disabled"}` + `reasoning_effort` | **medium → high** | GLM-5.3 拒收 medium |
| 火山方舟 | `thinking: {type: "enabled"\|"disabled"}` + `reasoning_effort` | 原样透传 | 原生支持 low/medium/high |
| Qwen（DashScope） | `enable_thinking: true\|false` + `thinking_budget` | effort 不映射；`BudgetTokens` → `thinking_budget` | 用预算而非档位控制 |
| DeepSeek | `thinking: {type: "enabled"\|"disabled"}` + `reasoning_effort` | 原样透传 | 默认开启思考（见下文） |

`BudgetTokens` 只对 Anthropic（`budget_tokens`）和 Qwen（`thinking_budget`）生效，其他 provider 忽略它。

## 各端点的坑

### Anthropic

- `budget_tokens` 下限是 1024，传入更小的值会被自动提升到 1024。
- Anthropic 要求 `max_tokens > budget_tokens`。如果显式或默认的 `max_tokens` 不满足，库会自动把它提高到 `budget + 2048`；未设置 `max_tokens` 时默认 4096。
- 思考模式要求 temperature 为 1，因此开启思考时 `temperature` 字段整体不发送。

### OpenAI Responses

- 发送 `reasoning: {effort, summary: "auto"}`；`summary: "auto"` 使思考摘要以流式增量返回（映射为 `ThinkingDeltaEvent`，见[流式事件](streaming.md)）。
- 思考块通过 Responses 的 reasoning item 在请求间保真：响应中的原始 item 被原样存到消息里（`ProviderExtra`），下一轮原样回传。**无法**仅由 `ThinkingPart` 文本重建 reasoning item——所以跨进程持久化历史后，Anthropic / Chat Completions 端点能还原思考上下文，Responses 端点会丢失 reasoning item（正文不受影响）。

### OpenAI Chat Completions（官方端点）

- 仅发送标准字段 `reasoning_effort`，不夹带任何兼容方言字段。
- 官方端点上 `max_tokens` 会自动改用 `max_completion_tokens`（推理模型拒绝旧的 `max_tokens`）。

### GLM / 智谱（含 Z.AI）

- **medium → high**：GLM-5.3 直接拒收 `reasoning_effort: "medium"`，而 GLM-5.2 在服务端把 low/medium 折叠为 high，所以统一映射 medium 为 high 是所有 GLM 思考模型都接受的取值。
- **GLM-5.3 强制思考**：显式关闭（`Thinking{}`）会发送 `thinking: {type: "disabled"}`，GLM-5.3 对其返回 400（错误信息提示改用 low）。对 GLM-5.3 不要试图关思考，用 `EffortLow` 代替。
- Z.AI（`z.ai` 域名）与 bigmodel.cn 走同一套方言。

### 火山方舟

- `thinking: {type}` + `reasoning_effort` 原样透传，无映射陷阱。

### Qwen（DashScope）

- 使用 `enable_thinking: true|false` 开关 + `thinking_budget` 预算，不发送 `reasoning_effort`。
- 要控制思考预算就设置 `BudgetTokens`；未设置时只发开关，预算由服务端决定。

### DeepSeek

- **默认开启思考**（服务端默认 effort 为 high）：什么都不传也是思考模式。因此关闭必须显式——传 `callable.Thinking{}`（零值）才会发送 `thinking: {type: "disabled"}`。
- `medium` 会被服务端映射为 high，与 GLM 类似；想要浅思考就用 `EffortLow`。
- DeepSeek 同时提供 Anthropic 兼容端点（`callable.DeepSeekAnthropicURL`），走 Anthropic provider 时按 Anthropic 规则映射。

## 按 BaseURL 自动嗅探方言

GLM / Ark / Qwen / DeepSeek 的思考字段都不是 OpenAI 官方标准，直接发给官方端点会报错。`NewOpenAIProvider` 会根据 baseURL 的域名自动启用对应方言（`Compat` 位掩码）：

| 域名特征 | 方言 |
|---|---|
| `bigmodel.cn`、`zhipuai`、`z.ai`（含子域） | `CompatGLM` |
| `volces.com` | `CompatArk` |
| `dashscope` | `CompatQwen` |
| `deepseek` | `CompatDeepSeek` |

使用内置端点常量（`callable.GLMURL`、`callable.DeepSeekURL` 等）时方言自动生效。嗅探结果可以用 `callable.WithCompat(...)` 手动覆盖：

```go
// 自建网关代理了 GLM，但域名里没有 bigmodel.cn —— 手动指定方言
callable.NewOpenAIProvider(key, "https://llm-gateway.internal/v1",
    callable.WithCompat(callable.CompatGLM))

// 反向：某兼容端点不吃任何非标准字段，关掉嗅探结果
callable.NewOpenAIProvider(key, url, callable.WithCompat(callable.CompatNone))
```

`Compat` 是位掩码，可以按位或组合（如 `CompatGLM | CompatQwen`），实际中很少需要。

## 响应侧解析：永远宽松

无论是否开启思考、无论连接的是哪个端点，响应解析都**永远宽松**：任何端点返回的 `reasoning_content` / `reasoning` / Anthropic 的 thinking block / Responses 的 reasoning item，都会被解析为 `ThinkingPart` 挂到 assistant 消息上；流式时对应 `ThinkingDeltaEvent`。解析不做开关——即使某个端点嗅探失败、配置保守，思考内容也不会丢。

```go
type ThinkingPart struct {
    Text      string `json:"text"`                // 思考文本
    Signature string `json:"signature,omitempty"` // Anthropic thinking block 签名（仅回传用）
    ID        string `json:"id,omitempty"`        // OpenAI Responses reasoning item id（仅回传用）
}
```

思考消耗的 token 计入 `Usage.ReasoningTokens`。

## 思考块在历史中的回传保真

工具循环是多轮的：assistant 消息（含思考块、工具调用）要作为上下文回传给模型。各端点对思考回传有硬性要求，库会原样保留并回传：

- **Anthropic**：thinking block 连同 `signature` 一起回传（服务端校验签名，缺失会报错）。
- **OpenAI Responses**：整段原始 reasoning item 通过 `ProviderExtra` 原样回传，保证 reasoning 连续性。
- **GLM / Qwen / DeepSeek**：思考文本作为 assistant 消息的 `reasoning_content` 回传。

只要使用 [Agent](agent.md) 的内置循环或 [Session](session.md)，这一切自动发生——历史中的 `ThinkingPart` 会随请求序列化、回传，不需要手工处理。

## 完整示例

下面的示例（改编自 `examples/thinking/`）用 `EffortMedium` 开启思考，在一个 Session 里连续问两个问题，并把思考流以暗色输出到 stderr：

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
	key := os.Getenv("ANTHROPIC_API_KEY")

	client := callable.NewClient(
		callable.NewAnthropicProvider(key, callable.AnthropicURL),
		callable.WithModel("claude-sonnet-5"),
	)

	agent := callable.NewAgent(client,
		// EffortLow / EffortMedium / EffortHigh 会映射为各 provider 的
		// 原生思考控制（budget_tokens、reasoning.effort、reasoning_effort、
		// GLM thinking、Qwen enable_thinking 等）。
		callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
	)

	// Anthropic 也可以直接给显式预算（等价于 thinking.budget_tokens）：
	// callable.WithThinking(callable.Thinking{BudgetTokens: 16000})

	session := agent.Session()
	questions := []string{
		"一个三位数，各位数字之和为 12，且数字互不相同，这样的数最多有几个？",
		"把你的推理过程总结成两句话。",
	}

	for _, q := range questions {
		fmt.Printf("\n== 问：%s\n答：", q)
		_, err := session.AskStream(ctx, func(ev callable.Event) {
			switch e := ev.(type) {
			case callable.ThinkingDeltaEvent:
				fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // 思考流，暗色
			case callable.TextDeltaEvent:
				fmt.Print(e.Delta) // 正文流
			}
		}, callable.User(q))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println()
	}
}
```

```bash
ANTHROPIC_API_KEY=... go run ./examples/thinking
```

换一个 provider 只需改构造 client 的两行，`WithThinking` 一行不动：

```go
// DeepSeek：默认就在思考；想显式关闭传 callable.Thinking{}
client = callable.NewClient(
	callable.NewOpenAIProvider(key, callable.DeepSeekURL),
	callable.WithModel("deepseek-v4"),
)

// Qwen：BudgetTokens 会映射为 thinking_budget
client = callable.NewClient(
	callable.NewOpenAIProvider(key, callable.QwenURL),
	callable.WithModel("qwen3-max"),
)

// OpenAI Responses：reasoning.effort + summary:"auto"
client = callable.NewClient(
	callable.NewOpenAIResponsesProvider(key, callable.OpenAIURL),
	callable.WithModel("gpt-5"),
)
```

## 注意事项速查

- 想**关闭** DeepSeek 的思考：显式传 `callable.Thinking{}`；不传 `WithThinking` 仍然是开。
- 对 **GLM-5.3** 不要传 `Thinking{}` 关思考（`disabled` 会 400），改用 `EffortLow`。
- `EffortMedium` 在 GLM 上实际发出的是 `high`，在 DeepSeek 上由服务端折叠为 high——这是各端点都接受的安全取值。
- `BudgetTokens` 只对 Anthropic / Qwen 生效；只设 `BudgetTokens` 时其他 provider 按 `EffortMedium` 处理。
- 开启思考时不要依赖 `WithTemperature`：思考模式下温度字段不会发送。
- 嗅探不到的自建网关用 `WithCompat` 手动指定方言，避免把非标字段发给不认识的端点。
