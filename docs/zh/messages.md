# 统一消息模型

**中文** | [English](../en/messages.md)

callable 内部只有一种消息模型：`Message{Role, Parts}`。无论你用 OpenAI Chat Completions、OpenAI Responses 还是 Anthropic Messages，业务代码构造的消息完全相同——三种 wire 格式的互转全部由 Provider 适配层在发送/接收时完成。本文档完整说明这套模型。

## Role：消息的作者

`Role` 是 `string` 的别名类型，只有四个常量：

| 常量 | 值 | 说明 |
|---|---|---|
| `callable.RoleSystem` | `"system"` | 系统提示。Chat Completions 映射为 `role=system` 消息（多条合并）；Responses 映射为顶层 `instructions`；Anthropic 映射为顶层 `system` 字段 |
| `callable.RoleUser` | `"user"` | 用户输入（文本、图片或二者混排） |
| `callable.RoleAssistant` | `"assistant"` | 模型输出（正文、思考、工具调用），通常由响应产生，也可手工构造以回填历史 |
| `callable.RoleTool` | `"tool"` | 工具执行结果。Anthropic 实际映射为 user 消息内的 `tool_result` block，由适配层处理，对使用者透明 |

## Message 结构

```go
type Message struct {
    Role  Role
    Parts []Part
    // 另有不可导出的 per-provider 附加数据（见"历史回传保真"一节）
}
```

一条消息可以混合多种内容（`Parts` 是有序切片，顺序即内容顺序）。例如模型的一轮输出可能是 `[ThinkingPart, ToolCallPart]`，也可能是 `[ThinkingPart, TextPart]`。

`Message` 上提供四个便捷的访问方法，直接按类型过滤 Parts：

```go
func (m Message) Text() string                 // 拼接所有 TextPart 的文本
func (m Message) Thinking() string             // 拼接所有 ThinkingPart 的文本
func (m Message) ToolCalls() []ToolCallPart    // 按序返回所有 ToolCallPart
func (m Message) ToolResultsOf() []ToolResultPart // 按序返回所有 ToolResultPart
```

## Part 封闭类型族

`Part` 是一个封闭（sealed）接口——包含不可导出的方法，**无法在外部实现自定义 Part 类型**。具体类型只有五种，每种 JSON 序列化时都带一个 `"type"` 判别字段。

### TextPart

纯文本内容。

```go
type TextPart struct {
    Text string `json:"text"`
}
```

JSON 形态：`{"type":"text","text":"..."}`。构造器：`callable.Text(text string) TextPart`。

### ImagePart

图片引用。图片细节（支持格式、各家转换方式、限制）见 [图片输入](images.md)，此处只说明字段：

```go
type ImagePart struct {
    Path      string `json:"path,omitempty"`       // 本地文件路径
    URL       string `json:"url,omitempty"`        // 远程 URL，原样透传给 API
    Data      []byte `json:"data,omitempty"`       // 原始图片字节
    MediaType string `json:"media_type,omitempty"` // MIME 类型，如 "image/png"；为空时自动识别
}
```

- **三者只设其一**：`Path`、`URL`、`Data` 是互斥的图片来源，通过下面的构造器设置而不是直接填字段。
- **惰性解析**：构造时只存引用，读文件、识别媒体类型、base64 编码都发生在发送请求时、由目标 provider 完成。因此同一份消息历史可以发给任何 provider。代价是文件不存在等错误要到请求时才暴露。
- `MediaType` 为空时：先按文件扩展名识别（jpg/jpeg/png/gif/webp），再用 `http.DetectContentType` 嗅探字节；识别结果不是 `image/*` 则报错。

### ThinkingPart

模型推理输出（"思考" / reasoning）。详见 [思考模式](thinking.md)。

```go
type ThinkingPart struct {
    Text      string `json:"text"`                // 思考文本
    Signature string `json:"signature,omitempty"` // Anthropic thinking block 的签名（仅回传用）
    ID        string `json:"id,omitempty"`        // OpenAI Responses reasoning item 的 id（仅回传用）
}
```

保留在历史中并在下一轮回传是正确性要求：Anthropic 开启思考+工具时要求原样回传 thinking block（含 signature）；Responses 要求回传 reasoning item；DeepSeek/GLM 等要求回传 `reasoning_content`。`Signature` 和 `ID` 由各 provider 在解析响应时自动填充，**不要手工构造**——它们只对产生它们的 provider 有意义。

### ToolCallPart

模型请求的一次工具调用。

```go
type ToolCallPart struct {
    ID        string `json:"id"`        // provider 分配的调用 id（"tool_use" id / "call_id"）
    Name      string `json:"name"`      // 工具名
    Arguments string `json:"arguments"` // 模型产生的原始 JSON 参数（字符串，不是对象）
}
```

注意 `Arguments` 是**原始 JSON 字符串**（如 `{"city":"北京"}`），使用时自行 `json.Unmarshal`；agent loop 内部也是按字符串传递的。手工构造时务必保证它是合法 JSON，且 `ID` 非空——后续 `ToolResultPart.ToolCallID` 要靠它配对。

### ToolResultPart

一次工具调用的结果，由 agent loop 自动产生，也可手工构造以回放会话。

```go
type ToolResultPart struct {
    ToolCallID string `json:"tool_call_id"`       // 对应 ToolCallPart.ID
    Name       string `json:"name"`               // 工具名
    Content    string `json:"content"`            // 工具输出文本（通常是 JSON）
    IsError    bool   `json:"is_error,omitempty"` // 标记为执行失败，让模型自行应对
}
```

- 每个 `ToolCallPart` 在历史中必须有配对的 `ToolResultPart`（同一个 message 或紧随的 `RoleTool` 消息），否则部分 API（尤其 Anthropic）会直接拒绝请求。agent loop 会保证配对完整，包括取消时为未执行的调用合成 `IsError` 结果。
- `IsError=true` 不会中断 agent loop——错误作为结果回传给模型，由模型决定重试或换路。Responses 格式下错误结果会以 `"Error: "` 前缀拼进输出。

## 消息构造器

```go
func System(text string) Message
func User(parts ...any) Message
func Assistant(parts ...any) Message
func ToolResults(results ...ToolResultPart) Message
```

- `System` 只接受纯文本，生成 `RoleSystem` 消息。多数情况下不必手工构造——Agent 的 `WithSystemPrompt` 更方便；system 消息的合并/提权由各 provider 处理（见上表）。
- `User` / `Assistant` 接受任意数量的参数，每个参数可以是：
  - `string` → 自动包装成 `TextPart`
  - `Part` → 原样追加（`TextPart` / `ImagePart` / `ThinkingPart` / ...）
  - `[]Part` → 展开追加
  - 其他类型 → **panic**（消息构造出错属于编程错误，故意 fail-fast）
- `Assistant` 主要用于手工回填历史（例如 few-shot 示例或回放持久化的会话），正常流程中 assistant 消息由响应产生。
- `ToolResults` 生成 `RoleTool` 消息，携带一个或多个 `ToolResultPart`——用于在手工构造历史时回放一轮工具执行。

```go
callable.System("你是一个务实的助手")
callable.User("这张图里是什么？", callable.Image("/tmp/截图.png")) // 图文混排
callable.Assistant("巴黎是法国的首都。")                            // 回填历史
callable.ToolResults(callable.ToolResultPart{
    ToolCallID: "call_1",
    Name:       "get_weather",
    Content:    `{"temp": 26}`,
})
```

## Part 构造器

```go
func Text(text string) TextPart
func Image(ref string) ImagePart                  // 本地路径，或以 http(s):// 开头的 URL
func ImageURL(url string) ImagePart               // 远程 URL，字节不经本地，原样透传
func ImageBytes(data []byte, mediaType string) ImagePart // 原始字节 + MIME 类型
```

- `Image` 按前缀自动区分路径与 URL（`http://` / `https://` 开头视为 URL）。
- `ImageBytes` 的 `mediaType` 应为完整 MIME 类型（如 `"image/png"`）；传空串则从字节内容嗅探。
- 图片的格式支持、各家 wire 转换和大小限制见 [图片输入](images.md)。

## 历史回传保真（ProviderExtra）

多轮工具循环要求把上一轮 assistant 消息**逐字节保真**地回传，但三种 wire 格式各自有无法塞进统一模型的数据。为此 `Message` 携带一份按 provider 名索引的原始载荷：

```go
func (m *Message) SetProviderExtra(provider string, raw json.RawMessage)
func (m Message) ProviderExtra(provider string) json.RawMessage
```

各家的回传机制：

| Provider | 回传内容 | 存放位置 |
|---|---|---|
| Anthropic | thinking block 的 `signature` | `ThinkingPart.Signature` |
| OpenAI Responses | 完整的输出 item 原文（reasoning item 等） | `providerExtra["openai-responses"]` |
| Chat Completions 国产端点（GLM/DeepSeek/Qwen 等） | `reasoning_content` 思考文本 | `ThinkingPart.Text`（回传时写入 assistant 消息的 `reasoning_content` 字段） |

行为细节：

- 这些字段由 provider 在解析响应时**自动填充**，agent loop 和 Session 会原样保留，用户通常无需关心。
- `providerExtra` 按 `Provider.Name()` 索引，只在发回**同一个 provider** 时生效；把历史切换给另一家 provider 时附加数据被忽略（`ThinkingPart.Text` 等统一字段仍然按目标格式尽力转换）。
- Responses 的 reasoning item **无法从 ThinkingPart 重建**——它依赖 providerExtra 里的原文。如果你手工构造 assistant 历史消息喂给 Responses，思考连续性会丢失（文本/工具调用不受影响）。
- 高级用法（跨进程搬运历史、审计）可以直接读写 `SetProviderExtra` / `ProviderExtra`；`provider` 参数取 `Provider.Name()` 的值，如 `"openai-responses"`。

## JSON 序列化与持久化

`Message` 实现了 `json.Marshaler` / `json.Unmarshaler`，可以整体序列化（包括 providerExtra），落盘后无损恢复。`Session.History()` 返回的 `[]Message` 同样可以直接 `json.Marshal`——见 [多轮会话](session.md)。

序列化格式：

```json
{
  "role": "assistant",
  "parts": [
    {"type": "thinking", "text": "先查天气……", "signature": "EqoBCgI..."},
    {"type": "tool_call", "id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"北京\"}"}
  ],
  "provider_extra": {
    "openai-responses": [{"type": "reasoning", "id": "rs_123"}]
  }
}
```

反序列化的边界行为：

- `parts` 中每个元素按 `"type"` 字段还原为对应的具体类型；**未知的 type 返回错误** `unknown message part type`。
- `role` 为空字符串时默认为 `"user"`。
- `parts` 为 `null` 时反序列化为空切片；序列化时空 Parts 输出为 `[]` 而非 `null`。
- `ImagePart.Data`（`[]byte`）按 Go `encoding/json` 惯例序列化为 base64 字符串。
- 顶层多出的未知字段被忽略（前向兼容）。

需要单独反序列化一个 Part（例如从外部数据源逐条读取）时：

```go
func UnmarshalPart(data []byte) (Part, error)
```

## 完整示例

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/great-magician-01/callable"
)

func main() {
	// 手工构造一段含工具调用的会话历史（实际通常来自 Session.History()）
	history := []callable.Message{
		callable.System("你是一个务实的助手"),
		callable.User("北京天气怎么样？", callable.Image("/tmp/截图.png")),
		callable.Assistant(
			callable.TextPart{Text: "我查一下。"},
			callable.ToolCallPart{ID: "call_1", Name: "get_weather", Arguments: `{"city":"北京"}`},
		),
		callable.ToolResults(callable.ToolResultPart{
			ToolCallID: "call_1",
			Name:       "get_weather",
			Content:    `{"temp":26,"cond":"晴"}`,
		}),
	}

	// 持久化：整段历史（含 provider 回传数据）可以无损落盘
	data, err := json.Marshal(history)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("history.json", data, 0o644); err != nil {
		panic(err)
	}

	// 恢复
	raw, err := os.ReadFile("history.json")
	if err != nil {
		panic(err)
	}
	var restored []callable.Message
	if err := json.Unmarshal(raw, &restored); err != nil {
		panic(err)
	}

	// 按类型遍历消息的 Parts
	for _, m := range restored {
		for _, p := range m.Parts {
			switch v := p.(type) {
			case callable.TextPart:
				fmt.Println("text:", v.Text)
			case callable.ImagePart:
				fmt.Println("image:", v.Path, v.URL)
			case callable.ThinkingPart:
				fmt.Println("thinking:", v.Text)
			case callable.ToolCallPart:
				fmt.Println("tool call:", v.Name, v.Arguments)
			case callable.ToolResultPart:
				fmt.Println("tool result:", v.Name, v.Content)
			}
		}
	}

	// 恢复后的历史可以直接作为下一次请求的输入，wire 转换由 provider 完成
	// resp, err := client.Create(ctx, callable.NewRequest(restored...))
}
```

## 注意事项

- **不要丢弃 thinking/tool 部分**：回放或裁剪历史时如果把 assistant 消息的 `ThinkingPart` 删掉，Anthropic（开思考时）和 Responses 的下一次请求可能直接报错或思考连续性中断。裁剪历史请按整轮（assistant + 配对 tool 消息）进行。
- **tool_call 必须配对 tool_result**：见 ToolResultPart 一节；取消/超时场景 agent loop 会自动合成配对结果，手工构造历史时需自行保证。
- `Part` 是封闭接口，无法扩展新的 part 类型；`User`/`Assistant` 收到未知参数类型会 panic。
- `ImagePart` 的本地文件在发送时才读取——历史序列化保存的是路径引用（`path`），换机器恢复时路径可能失效；需要跨机器搬运请改用 `ImageBytes`（字节会随 JSON 一起序列化）。
- 消息模型不包含 provider 特有概念（如 cache_control、服务端工具）；这类需求请用请求级 `WithExtra` 逃生舱，见 [错误处理](errors.md)。
