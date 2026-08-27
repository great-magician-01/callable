# 子代理委派（Sub-Agents）

**中文** | [English](../en/subagents.md)

callable 的 SubAgent 机制让父 agent 可以把子任务委派给独立运行的子代理。和 [Skill 渐进披露](skills.md)一样，子代理**默认不会出现在工具列表里**：system prompt 只注入一段 name/description 索引，模型需要先调用内置 `load_agent` 工具完成加载，之后才会动态注册出 `call_<name>` 工具供其委派任务。每次委派，子代理都以**全新会话**跑自己的 [Agent 循环](agent.md)，最终回答作为工具结果返回给父 agent。

## 定义子代理：NewSubAgent

```go
func NewSubAgent(name, description string, opts ...SubAgentOption) SubAgent
```

- `name`：短标识符，用于 `load_agent` 的参数；加载后的可调用工具名是 `call_<name>`。请使用工具名安全的字符（字母、数字、`_`、`-`）。
- `description`：一行简介，会出现在 system prompt 的索引里——父 agent 的模型完全依据它来决定是否加载并委派给这个子代理，写清楚很关键。
- `opts`：见下表。

### SubAgentOption 一览

| 选项 | 签名 | 说明 |
|---|---|---|
| `WithSubAgentClient` | `func WithSubAgentClient(client *Client) SubAgentOption` | 给子代理一个完全独立的 client（可以换成另一个 provider / 端点 / 密钥）。不设置时继承父 agent 的 client。 |
| `WithSubAgentModel` | `func WithSubAgentModel(model string) SubAgentOption` | 沿用父 client（端点、密钥等默认配置），仅换一个模型。设置了 `WithSubAgentClient` 时此选项被忽略。 |
| `WithSubAgentPrompt` | `func WithSubAgentPrompt(prompt string) SubAgentOption` | 子代理的 system prompt。 |
| `WithSubAgentTools` | `func WithSubAgentTools(tools ...Tool) SubAgentOption` | 子代理内部循环可用的[工具](tools.md)，对父 agent 不可见。 |
| `WithSubAgentSkills` | `func WithSubAgentSkills(skills ...Skill) SubAgentOption` | 子代理内部可用的 skill；子代理自带自己的内置 `read_skill` 工具。 |
| `WithSubAgentThinking` | `func WithSubAgentThinking(t Thinking) SubAgentOption` | 子代理的[思考模式](thinking.md)配置。 |
| `WithSubAgentMaxTurns` | `func WithSubAgentMaxTurns(n int) SubAgentOption` | 每次委派中子代理的最大模型调用轮数，默认 25；`n <= 0` 时忽略（保持默认）。 |

所有选项都只影响子代理内部，不会泄漏到父 agent。

## 注册到父 agent：WithSubAgents

```go
func WithSubAgents(subs ...SubAgent) AgentOption
```

```go
agent := callable.NewAgent(client,
    callable.WithSystemPrompt("翻译交给 translator，调研交给 researcher"),
    callable.WithSubAgents(translator, researcher),
)
```

注册后父 agent 自动获得内置 `load_agent` 工具，system prompt 中注入索引块。同名子代理重复注册时，**先注册的生效**，后注册的静默忽略。

## 两步委派流程

整个流程由模型驱动，代码层面不需要任何手工调用：

1. **加载**：模型调用内置 `load_agent({"name": "translator"})`。注册表把该子代理的 `call_translator` 工具**动态注册**进父 agent 的工具集（下一轮请求起对模型可见），并返回一张「使用卡片」给模型，包含子代理的描述、system prompt、能力清单（工具名 + skill）、模型（若有覆盖），以及调用方式提示。
2. **委派**：模型调用 `call_translator({"task": "..."})`。`task` 是一个完整、自包含的子任务描述——子代理看不到父 agent 的对话历史，任务描述必须自己带全上下文。子代理以全新会话独立跑自己的 agent loop（用自己的 client/模型/提示词/工具/skill），其最终回答（`FinalText`）作为 `call_translator` 的工具结果返回给父 agent，父 agent 据此继续。

## system prompt 中的索引原文

注册了子代理后，system prompt 末尾会追加如下索引块（`load_agent` 为默认工具名，改名后此处同步变化）：

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

如果同时注册了 skill，`<available_skills>` 与 `<available_agents>` 两个索引块会以空行分隔、按序拼在 system prompt 之后。

## 内置工具的改名与禁用

```go
const DefaultSubAgentLoadToolName = "load_agent"

func WithSubAgentToolName(name string) AgentOption   // 改名（空字符串忽略）
func WithSubAgentToolDisabled() AgentOption          // 不注册 load_agent
```

- 禁用后，已注册的子代理仍出现在 `<available_agents>` 索引里，但模型无法自行加载——此时需要你自己注册替代的加载工具（或预先手工注册 `call_<name>` 工具）。
- 未注册任何子代理时，无论是否禁用，`load_agent` 都不会出现。

## 行为细节与边界情况

- **加载幂等**：同一个子代理 load 多次不会重复注册；第二次起 `load_agent` 返回 "Sub-agent \"xxx\" is already loaded." 及同一张使用卡片。注册表带锁，同一 agent 的并行会话各自加载是安全的。
- **与用户工具重名**：加载时若父 agent 已存在同名 `call_<name>` 用户工具，加载返回错误结果（`IsError`：`tool "call_xxx" is already registered`）而不会覆盖你的工具。规划子代理命名时注意避开 `call_` 前缀冲突。
- **加载不存在的名字**：`load_agent({"name": "nobody"})` 返回 `IsError` 结果，附带可用子代理名单，模型可自行纠正。
- **未加载直接调用**：`call_<name>` 在加载前不在工具列表里，直接调用会得到 `unknown tool "call_xxx"` 的错误工具结果（回传给模型，不中断 loop，见[错误处理](errors.md)）。
- **不嵌套**：子代理不继承父 agent 的子代理列表——子代理里没有 `load_agent`，无法继续向下委派。
- **不共享历史**：每次 `call_<name>` 调用都会新建一个全新 Agent，多次委派之间互不知道对方存在；子代理也看不到父 agent 的对话。需要传递的上下文全部写进 `task`。
- **max turns 兜底**：子代理跑满 `WithSubAgentMaxTurns` 时不会让整个委派失败——实现会从部分轨迹中取最后一条 assistant 文本，作为工具结果返回给父 agent，并附上 `[sub-agent stopped: reached max turns]` 提示。若部分轨迹里没有任何文本，才会以错误向上传递（`*MaxTurnsError`，见[错误处理](errors.md)）。
- **空最终回答**：子代理正常结束但 `FinalText` 为空时，工具结果是 `(sub-agent "xxx" finished without a final text answer)`。
- **事件透传**：默认子代理内部发生的一切对父 agent 的事件回调不可见（委派表现为一次普通的长耗时工具调用）。`WithSubAgentEvents(true)` 后子代理改为流式执行，其内部每个事件都被包装为 `SubAgentEvent` 转发到父 agent 传给 `RunStream` / `AskStream` 的事件回调：

  ```go
  type SubAgentEvent struct {
      SubAgent string // 产生该事件的子代理名
      Event    Event  // 原始事件（TextDeltaEvent / ToolCallEvent / ...）
  }
  ```

  事件回调是请求级的（每次 `RunStream` 调用各自独立转发），同一 agent 的并行会话不会串流。注意只有父 agent 走流式接口且传了非 nil 回调时透传才生效；`Run` / `Ask` 下子代理内部事件无处可达。

## 完整示例

translator（纯模型委派）+ researcher（自带工具）两个子代理，父 agent 流式运行并透传子代理事件：

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	callable "github.com/great-magician-01/callable"
)

func main() {
	client := callable.NewClient(
		callable.NewOpenAIProvider(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL),
		callable.WithModel("gpt-5"),
	)

	// 翻译子代理：换用便宜模型，只换模型不换端点。
	translator := callable.NewSubAgent("translator", "把中文翻译成地道的英文",
		callable.WithSubAgentModel("gpt-5-mini"),
		callable.WithSubAgentPrompt("你是一名专业译者。把用户给出的中文翻译成自然、地道的英文，只输出译文。"),
	)

	// 调研子代理：自带搜索工具（对父 agent 不可见）与 skill。
	search := callable.NewTool("web_search", "搜索互联网",
		func(ctx context.Context, args struct {
			Query string `json:"query" jsonschema:"description=搜索关键词"`
		}) (any, error) {
			return fmt.Sprintf("关于 %q 的搜索结果……", args.Query), nil
		})
	researcher := callable.NewSubAgent("researcher", "深度调研一个主题并给出带引用的结论",
		callable.WithSubAgentPrompt("你是研究员。先用 web_search 搜集资料，再给出结论。"),
		callable.WithSubAgentTools(search),
		callable.WithSubAgentSkills(callable.NewSkill("citing", "引用规范", "# 引用格式\n...")),
		callable.WithSubAgentThinking(callable.Thinking{Effort: callable.EffortHigh}),
		callable.WithSubAgentMaxTurns(10),
	)

	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("你是主编排者。翻译交给 translator，调研交给 researcher，不要自己动手。"),
		callable.WithSubAgents(translator, researcher),
		callable.WithMaxTurns(15),
		callable.WithSubAgentEvents(true), // 子代理内部事件包装为 SubAgentEvent 透传
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta) // 父 agent 的输出
		case callable.SubAgentEvent:
			// 子代理内部事件：e.SubAgent 是子代理名，e.Event 是原始事件。
			if d, ok := e.Event.(callable.TextDeltaEvent); ok {
				fmt.Printf("[%s] %s", e.SubAgent, d.Delta)
			}
		}
	}, callable.User("调研一下 Go 1.26 的新特性，把要点翻译成英文。"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("\n[turns=%d tokens: in=%d out=%d]\n",
		result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
}
```

可运行的完整版本见仓库 `examples/subagents/main.go`。

## 相关文档

- [Agent 循环](agent.md)：父 agent 与子代理共用的循环机制
- [工具](tools.md)：子代理工具的注册方式
- [Skill 渐进披露](skills.md)：同一套渐进披露设计在 skill 上的应用
- [流式事件](streaming.md)：全部事件类型与 `SubAgentEvent`
- [错误处理](errors.md)：`unknown tool`、`MaxTurnsError` 等
