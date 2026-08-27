# 工具系统

**中文** | [English](../en/tools.md)

工具（Tool）是把你的 Go 函数暴露给模型的桥梁。callable 提供三种定义方式：

- `NewTool[A]`（推荐）：泛型构造器，参数 struct 自动反射生成 JSON Schema；
- `NewRawTool`：手写 JSON Schema，handler 收到原始 JSON 参数；
- 实现 `Tool` 接口：完全自定义，适合高级场景。

工具注册到 [Agent](agent.md) 后，agent loop 会自动完成「模型请求工具 → 执行 → 结果回传模型」的循环；工具执行出错**不会中断循环**，而是作为错误结果回传给模型让它自行调整。

## 快速示例

```go
package main

import (
	"context"
	"fmt"
	"os"

	callable "github.com/great-magician-01/callable"
)

// WeatherArgs 会被反射成工具的 JSON Schema。
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
			unit := "°C"
			if args.Unit == "fahrenheit" {
				unit = "°F"
			}
			// 返回 string：原样作为工具结果内容回传给模型
			return fmt.Sprintf(`{"city":%q,"temp":26,"unit":%q,"condition":"晴"}`, args.City, unit), nil
		})

	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("你是一个务实的天气助手。"),
		callable.WithTools(weather),
	)

	result, err := agent.Run(ctx, callable.User("北京现在多少度？用摄氏度。"))
	if err != nil {
		panic(err)
	}
	fmt.Println(result.FinalText)
}
```

## NewTool[A]：泛型构造器

```go
func NewTool[A any](name, description string,
    fn func(ctx context.Context, args A) (any, error)) Tool
```

- `name` / `description`：模型选择工具时看到的名称与说明。名称在同一 agent 内必须唯一（重复注册时后者被忽略；用户工具的优先级高于内置工具，如 `read_skill`）。
- `fn`：工具执行体。每次模型发起调用时，`call.Arguments`（模型生成的 JSON）会先 `json.Unmarshal` 到 `A`，再调用 `fn`。
- 参数 schema 由 [invopop/jsonschema](https://github.com/invopop/jsonschema)（callable 唯一的第三方依赖）在构造时反射生成，`DoNotReference: true`（所有 `$ref` 内联，provider 不喜欢外部引用），输出 draft-07 兼容的 schema。schema 根节点一定是 `{"type":"object"}`——这是各家 provider 的硬性要求；`A` 为无字段类型时退化为空 object。

### jsonschema struct tag

字段名取自 `json` tag；`json:",omitempty"` 的字段是**可选参数**，不带 `omitempty` 的字段进入 schema 的 `required` 列表。`jsonschema` tag 用于补充描述与约束：

```go
type SearchArgs struct {
	// 必填字符串，带描述
	Query string `json:"query" jsonschema:"description=搜索关键词"`
	// 可选枚举：重复 enum= 列出所有合法值
	Sort string `json:"sort,omitempty" jsonschema:"description=排序方式,enum=time,enum=relevance"`
	// 可选数字，带默认值与范围约束
	Limit int `json:"limit,omitempty" jsonschema:"description=返回条数,default=10,minimum=1,maximum=100"`
}
```

生成给模型的 schema 大致如下：

```json
{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "搜索关键词"},
    "sort":  {"type": "string", "description": "排序方式", "enum": ["time", "relevance"]},
    "limit": {"type": "integer", "description": "返回条数", "default": 10, "minimum": 1, "maximum": 100}
  },
  "required": ["query"]
}
```

### 嵌套 struct、slice、map

复杂参数结构同样直接反射，无需额外处理：

```go
type Range struct {
	Start int `json:"start" jsonschema:"description=起始页"`
	End   int `json:"end" jsonschema:"description=结束页"`
}

type ExportArgs struct {
	Format string            `json:"format" jsonschema:"description=导出格式,enum=csv,enum=pdf"`
	Range  Range             `json:"range" jsonschema:"description=页码范围"`              // 嵌套 struct
	Tags   []string          `json:"tags,omitempty" jsonschema:"description=标签过滤"`     // slice
	Meta   map[string]string `json:"meta,omitempty" jsonschema:"description=附加元数据"`   // map
}
```

嵌套 struct 会以内联 object（含自己的 `properties` / `required`）的形式出现在 schema 中，不会生成 `$ref`。

> 可用的 tag 键不止上面这些——`required`、`oneof`、`title`、`pattern`、`minLength` 等 invopop/jsonschema 支持的 tag 都能用，详见该库的文档。

## NewRawTool：手写 JSON Schema

```go
func NewRawTool(name, description, parametersJSON string,
    fn func(ctx context.Context, rawArgs string) (any, error)) Tool
```

适合 schema 无法用 struct 表达（如 `oneOf`、动态结构）、或参数需自行解析的场景：

```go
sqlQuery := callable.NewRawTool("run_sql", "执行只读 SQL 查询", `{
	"type": "object",
	"properties": {
		"sql": {"type": "string", "description": "SELECT 语句"}
	},
	"required": ["sql"],
	"additionalProperties": false
}`, func(ctx context.Context, rawArgs string) (any, error) {
	// rawArgs 是模型生成的原始 JSON 字符串，自行解析
	var args struct {
		SQL string `json:"sql"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return nil, fmt.Errorf("bad arguments: %w", err) // 变成 IsError 结果回传模型
	}
	return runReadOnlyQuery(ctx, args.SQL)
})
```

注意：

- `parametersJSON` 在构造时解析一次；**非法 JSON 会直接 panic**（属于编程错误，尽早暴露）。
- 传空字符串表示「无参数」，此时 `Parameters` 为 `nil`，发给 provider 时退化为 `{"type":"object"}`。
- 与 `NewTool` 不同，`NewRawTool` 的 handler 拿到的是原始字符串，库不做任何解析/校验——模型给出的参数不合法时要自己处理并返回 error。

## Handler 返回值规则

两种构造器的 handler 都返回 `(any, error)`，归一化规则（`coerceToolOutput`）：

| 返回值 | 工具结果 |
|---|---|
| `nil` | 空结果（`ToolResult{}`，内容为空字符串） |
| `string` | 原样作为结果内容（适合返回纯文本或手写 JSON 字符串） |
| `callable.ToolResult` | 原样使用（可精确控制 `IsError`） |
| 其他任意类型 | `json.Marshal` 后的 JSON 字符串（struct/map/slice 推荐直接返回） |
| `error != nil` | `ErrorResult(err)`：`Content = err.Error()`、`IsError = true` |
| 返回值无法 `json.Marshal` | 同样变为 `IsError` 结果，不中断循环 |

也就是说，返回一个普通 struct 即可得到格式良好的 JSON 结果：

```go
callable.NewTool("get_user", "按 ID 查询用户",
	func(ctx context.Context, args struct {
		ID int `json:"id" jsonschema:"description=用户 ID"`
	}) (any, error) {
		u, err := db.FindUser(ctx, args.ID)
		if err != nil {
			return nil, err // 自动回传模型
		}
		return u, nil // json.Marshal 后作为结果内容
	})
```

## 错误处理：工具出错不中断 loop

这是 callable 的核心设计：**工具层的失败全部回传给模型，只有 API/网络层错误才中断 agent run**（见[错误处理](errors.md)）。

会作为 `IsError=true` 的结果回传模型的情况：

- handler 返回 error（最常见的业务失败，模型可据此重试或换路）；
- 模型给出的参数 JSON 无法解析为 `A`（回传内容中附带期望的 schema，方便模型自我修正）；
- 返回值 `json.Marshal` 失败；
- 模型调用了不存在的工具（回传 `unknown tool "xxx" (available tools: ...)`）；
- 调用被 [审批钩子](agent.md) `Deny(reason)` 拒绝（回传 `Tool call denied: ...`）；
- context 取消时尚未执行的工具调用，会合成 `"tool execution skipped: ..."` 结果，保证每个 tool_call 都有配对的 tool_result（历史仍是可重放的有效会话）。

`IsError` 的 wire 表示：

- Anthropic：`tool_result` block 的 `is_error: true`（内容若不以 `Error` 开头会自动加 `Error: ` 前缀）；
- OpenAI Chat Completions：`role=tool` 消息携带错误文本；
- OpenAI Responses：`function_call_output` item 携带错误文本。

仍然会导致 `Run` 返回 error 的：provider 的 4xx/5xx（重试耗尽后）、网络错误、`ToolCallHook` 自身返回 error。

## ToolResult / TextResult / ErrorResult

```go
type ToolResult struct {
	Content string // 展示给模型的输出，通常是 JSON 或纯文本
	IsError bool   // 为 true 时告诉模型执行失败
}

func TextResult(content string) ToolResult // ToolResult{Content: content}
func ErrorResult(err error) ToolResult     // ToolResult{Content: err.Error(), IsError: true}
```

需要精确控制结果时，handler 直接返回 `ToolResult` 即可绕过归一化，例如「业务上失败但不是 Go error」的场景：

```go
if resp.StatusCode == 404 {
	return callable.ErrorResult(fmt.Errorf("user %d not found", args.ID)), nil
}
```

> 注意是「返回 `ErrorResult` 且 error 为 nil」——如果返回 `(nil, err)`，库会替你包成同样的 `ErrorResult(err)`，两种写法等价。

## Tool 接口：完全自定义

```go
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"` // nil 表示无参数
}

type Tool interface {
	Definition() ToolDefinition                       // 发给模型的 schema
	Execute(ctx context.Context, rawArgs string) ToolResult
}
```

实现该接口即可获得最大自由度（动态 schema、自己的参数解析、非 JSON 输出等）。`Execute` 只返回 `ToolResult`、不返回 error——想让模型看到失败就返回 `IsError=true` 的结果，这与内置构造器的行为一致：

```go
type timeTool struct{}

func (timeTool) Definition() callable.ToolDefinition {
	return callable.ToolDefinition{
		Name:        "now",
		Description: "返回当前服务器时间",
		// Parameters 为 nil：无参数工具
	}
}

func (timeTool) Execute(ctx context.Context, rawArgs string) callable.ToolResult {
	return callable.TextResult(time.Now().Format(time.RFC3339))
}

agent := callable.NewAgent(client, callable.WithTools(timeTool{}))
```

## 注意事项

- **参数校验靠模型自觉**：schema 里的 `enum`/`minimum` 等约束只是给模型的提示，`NewTool` 只保证 JSON 能 `Unmarshal` 进 `A`；关键参数在 handler 里再校验一次，不合法就返回 error。
- **并行执行**：模型一轮返回多个 tool call 时默认顺序执行（确定性）；`WithParallelToolExecution(true)` 开启并发后，工具间共享的状态要自己加锁。
- **响应 ctx**：handler 收到的 ctx 与 agent run 相同，run 被取消时它会被 cancel；工具内部的慢操作（HTTP、DB）应当传递并响应这个 ctx——Go 无法强杀无视 ctx 的 goroutine。
- **工具名冲突**：同一 agent 内重名工具后者被忽略，先注册者生效；用户工具先于内置工具注册，因此可以用同名工具覆盖内置的 `read_skill` / `load_agent`（也可以用 `WithSkillToolDisabled` 等先禁用再自行注册）。
- **schema 体积**：schema 在构造时生成一次并随每个请求发送，过大的嵌套结构会占用 token，必要时分拆工具或精简描述。
- **观察执行过程**：用 `RunStream` 监听 `ToolCallEvent` / `ToolResultEvent` 可实时看到每次工具调用与结果，详见[流式事件](streaming.md)；执行前的审批/改参见 [Agent 循环](agent.md) 的 `WithToolCallHook`。

## 相关文档

- [快速开始](getting-started.md) — 第一个可运行的 agent
- [Agent 循环](agent.md) — 工具循环、审批钩子、并行执行、max turns
- [流式事件](streaming.md) — `ToolCallEvent` / `ToolResultEvent` 等事件
- [错误处理](errors.md) — API 错误、重试与取消
- [Skill 渐进披露](skills.md) / [子代理](subagents.md) — 内置工具 read_skill / load_agent
