# Skill：渐进式披露

**中文** | [English](../en/skills.md)

Skill 是一套命名的指令包（通常是 Markdown 格式的规范、流程或模板）。callable 用**渐进式披露（progressive disclosure）**的方式把它交给模型：system prompt 里只注入每个 skill 的名称和一行描述组成的索引，模型判断任务可能用到某个 skill 时，通过内置的 `read_skill` 工具按需加载全文，然后严格遵循加载到的指令执行。

## 设计动机

把所有指令一次性塞进 system prompt 有两个问题：

- **浪费 token**：不相关的指令每轮请求都在计费，而且 token 成本随对话轮数成倍放大。
- **稀释注意力**：过长的 system prompt 会分散模型对当前任务的注意力，反而降低遵循度。

渐进式披露把成本变成「按需付费」：索引只有几行，全文只在真正需要的那一轮加载一次。代价是多一次工具往返——对指令较长的场景这几乎总是划算的。

## 快速开始

```go
skill := callable.NewSkill("pdf-export", "把数据导出为 PDF 文件",
    "# PDF 导出规范\n\n（完整指令……只有模型调用 read_skill 时才会读取）")

agent := callable.NewAgent(client,
    callable.WithSkills(skill),
)
```

模型看到任务后自行决定调用 `read_skill({"name": "pdf-export"})`，拿到全文后按其执行，全程无需业务代码介入。

## API 一览

| API | 说明 |
| --- | --- |
| `callable.NewSkill(name, description, instructions string) Skill` | 构造一个 skill（内存中的值类型） |
| `callable.WithSkills(skills ...Skill) AgentOption` | 为 agent 注册 skill |
| `callable.WithSkillReadHook(h SkillReadHook) AgentOption` | 安装读取钩子，内容返回给模型前可改写 |
| `callable.WithSkillToolName(name string) AgentOption` | 重命名内置加载工具（默认 `read_skill`） |
| `callable.WithSkillToolDisabled() AgentOption` | 不注册内置加载工具，由你自行提供替代工具 |
| `callable.DefaultSkillToolName` | 常量，值为 `"read_skill"` |

相关类型：

```go
type Skill struct {
    Name         string // 短标识，模型用这个名字调用 read_skill
    Description  string // 一行摘要，出现在 system prompt 索引里，是模型决定是否加载的依据
    Instructions string // skill 全文（通常是 Markdown），只按需返回
}

type SkillReadHook func(ctx context.Context, name, content string) (string, error)
```

## 工作原理

### system prompt 中注入的索引

注册 skill 后，agent 会把索引块追加到 system prompt（在自己的 `WithSystemPrompt` 文本之后、子代理索引之前，之间空一行分隔）。索引**只含名称和描述，不含全文**。以 `WithSkills(pdfExportSkill, webSearchSkill)` 为例，实际注入的原文是：

```
<available_skills>
The following skills are available. When a task may benefit from a skill, first call the read_skill tool to load its full instructions, then follow them.

- pdf-export: 把数据导出为 PDF 文件
- web-search: 检索互联网信息
</available_skills>
```

（`read_skill` 会随 `WithSkillToolName` 的改名而变化。）

### 内置 read_skill 工具

只要注册了至少一个 skill 且未禁用，agent 会自动注册一个内置工具：

- 名称：`read_skill`（`DefaultSkillToolName`）
- 描述（原样发给模型）：`Load the full instructions of a skill by name. Call this before attempting a task that matches one of the available skills, then follow them.`
- 参数：`{"name": "..."}`（string，必填）

行为细节：

- **命中**：返回该 skill 的 `Instructions` 全文（若装了读取钩子，先过钩子）。
- **未命中**：不会中断 agent loop，而是返回一个**失败的工具结果**，内容形如 `skill "foo" not found. Available skills: pdf-export, web-search`，模型可自行换名重试。
- **无状态**：内置工具不记录「已加载」，模型每调用一次就返回一次全文。加载过的内容保留在对话历史里，后续轮次模型可以直接引用，一般不需要重复加载。

## 读取钩子：WithSkillReadHook

钩子签名：

```go
type SkillReadHook func(ctx context.Context, name, content string) (string, error)
```

每次模型调用 `read_skill`、内容返回给模型**之前**都会触发一次。返回的字符串替换原文；返回 error 则作为失败的工具结果回传给模型（同样不中断 loop）。典型用途：

- **注入运行时上下文**：时间、租户、环境等模型需要但写死会过期的信息。
- **改写内容**：按调用方裁剪或追加指令。
- **懒加载**：`NewSkill` 时 `Instructions` 留空或只放占位，钩子里再从磁盘/远程拉取真正的内容。
- **审计**：记录哪个模型在什么任务里加载了哪个 skill。

注入运行时上下文 + 审计的示例：

```go
agent := callable.NewAgent(client,
    callable.WithSkills(weatherSkill),
    callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
        log.Printf("skill loaded: %s", name) // 审计
        return content + "\n\n(报告生成时间: " + time.Now().Format("2006-01-02 15:04") + ")", nil
    }),
)
```

懒加载示例（构造时不加载正文，首次读取时才从磁盘读）：

```go
skill := callable.NewSkill("release-checklist", "发布前检查清单", "") // Instructions 暂为空

callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
    if content == "" {
        data, err := os.ReadFile("skills/" + name + ".md")
        if err != nil {
            return "", err // 作为失败的工具结果回传给模型
        }
        content = string(data)
    }
    return content, nil
})
```

注意钩子是**每次读取都触发**的，纯懒加载场景请自行缓存（上面的例子只在 `content == ""` 时读盘，是因为 skill 是不可变的值类型；如果钩子里要缓存，请用闭包变量）。

## 自定义加载工具

### 改名：WithSkillToolName

```go
agent := callable.NewAgent(client,
    callable.WithSkills(skill),
    callable.WithSkillToolName("load_skill"), // 工具改名，索引里的提示文本同步更新
)
```

传空字符串会被忽略（保持原名）。system prompt 索引中的工具名会同步换成新名字，模型不会感知差异。

### 禁用后自行注册：WithSkillToolDisabled

```go
// 自己实现的加载工具：带权限控制
loadSkill := callable.NewTool("read_skill",
    "Load the full instructions of a skill by name.",
    func(ctx context.Context, args struct {
        Name string `json:"name" jsonschema:"description=Name of the skill to load"`
    }) (any, error) {
        if !allowed(args.Name) {
            return callable.ErrorResult(fmt.Errorf("skill %q is not available in this tenant", args.Name)), nil
        }
        return skillBody(args.Name), nil
    })

agent := callable.NewAgent(client,
    callable.WithSkills(skill),
    callable.WithSkillToolDisabled(), // 关掉内置工具
    callable.WithTools(loadSkill),    // 注册自己的替代实现
)
```

两个坑：

- **禁用内置工具后，`<available_skills>` 索引仍然会注入 system prompt**，且其中提示的仍是默认工具名 `read_skill`。如果你的替代工具用了别的名字，记得同时用 `WithSkillToolName` 把索引里的名字对齐，否则模型会去调一个不存在的工具并得到 unknown tool 错误。
- **工具重名时先注册者生效**：用户工具先于内置工具注册，所以你也可以**不禁用**，直接注册一个同名的 `read_skill` 用户工具来覆盖内置实现。但显式禁用更清晰，推荐前者。

## 完整示例

下面这个例子注册了一个「天气报告」skill，并用钩子注入生成时间（完整可运行版本见仓库 `examples/skills/main.go`）：

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
	ctx := context.Background()
	key := os.Getenv("ANTHROPIC_API_KEY")

	client := callable.NewClient(
		callable.NewAnthropicProvider(key, callable.AnthropicURL),
		callable.WithModel("claude-sonnet-5"),
	)

	// 只有 name + description 会进 system prompt；全文按需加载。
	weatherSkill := callable.NewSkill("weather-report",
		"生成正式的天气报告文档",
		`# 天气报告生成规范

收到天气数据后，按以下格式输出报告：

1. 标题：《城市名》天气报告
2. 概览表格：日期、温度、天气、风力
3. 一段生活建议

规则：
- 温度保留整数
- 使用摄氏度
- 建议控制在 100 字以内`)

	agent := callable.NewAgent(client,
		callable.WithSkills(weatherSkill),
		// 读取钩子：每次模型加载 skill 时触发，这里注入运行时上下文。
		callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
			return content + "\n\n(报告生成时间: " + time.Now().Format("2006-01-02 15:04") + ")", nil
		}),
	)

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		if d, ok := ev.(callable.TextDeltaEvent); ok {
			fmt.Print(d.Delta)
		}
	}, callable.User("北京今天 26 度，晴，微风。帮我生成天气报告。"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	_ = result
	fmt.Println()
}
```

运行时的典型轨迹：模型看到 `<available_skills>` 索引 → 判断任务匹配 `weather-report` → 调用 `read_skill` → 拿到（经钩子追加时间戳的）全文 → 按规范输出报告。

## 与子代理的关系

Skill 在子代理（sub-agent）里同样可用：用 `WithSubAgentSkills` 注册的 skill 只在子代理自己的 agent loop 中生效，子代理自带独立的 `read_skill` 工具，对父 agent 不可见。详见 [子代理](subagents.md)。

## 注意事项与坑

- **Description 是模型的唯一决策依据**：写清楚「什么时候该用这个 skill」，写得含糊模型就不会加载。Instructions 再精彩，索引吸引不了模型也白搭。
- **Instructions 是静态字符串**：`NewSkill` 之后不可变。需要动态内容（当前时间、用户身份、配置）时用 `WithSkillReadHook` 注入，不要指望 skill 本身带变量。
- **钩子每次读取都会执行**：钩子函数要幂等、要快速；做了 IO 就自己加缓存。钩子的 `ctx` 与本次 agent 运行同源，运行取消时钩子也会收到取消信号。
- **skill 按 Name 索引**：同名 skill 重复注册时后者覆盖前者（内置工具用 map 按名字查找）；工具列表里同名工具则先注册者生效。
- **不占用工具名额的错觉**：skill 本身不是工具，但每个注册了 skill 的 agent 都会多一个 `read_skill` 工具出现在工具列表里，评估 token 成本时把它的 schema 也算上。
- **全文进历史**：加载过的指令以工具结果的形式留在对话历史中，后续轮次会随历史一起回传计费——这正是「只加载一次」的意义。

## 相关文档

- [Agent 循环](agent.md) — read_skill 是 agent loop 中的一个普通工具调用
- [工具](tools.md) — 自行注册替代加载工具时的工具定义方式
- [子代理](subagents.md) — 子代理的渐进式披露与 skill 作用域
