# 结构化输出与采样参数

**中文** | [English](../en/structured-output.md)

本篇介绍两个请求级能力：用 `ResponseFormat` 约束模型输出的 JSON 形态（结构化输出），以及 `top_p` 与停止序列两个采样参数。两者都可以设在单个请求上，也可以设为 Client 默认值。

## 结构化输出（ResponseFormat）

`ResponseFormat` 统一描述「模型必须以某种 JSON 形态回答」这一约束：

```go
type ResponseFormat struct {
    // schema 名称；部分 provider 要求必填，留空默认为 "output"
    Name   string
    // 输出要满足的 JSON Schema；nil 表示自由 JSON 模式（"给我 JSON，形状不限"）
    Schema map[string]any
    // 要求支持该能力的 provider 严格保证输出符合 schema
    Strict bool
}
```

三个构造函数：

| 构造函数 | 说明 |
|---|---|
| `callable.JSONMode() ResponseFormat` | 自由 JSON 模式：保证输出是合法 JSON，形状不限 |
| `callable.JSONSchema(name string, schema map[string]any, strict bool) ResponseFormat` | 手工给出 JSON Schema（以解码后的 JSON 对象形式） |
| `callable.JSONSchemaFor[T any](name string, strict bool) ResponseFormat` | 从 Go struct 反射生成 schema——与 `NewTool` 相同的反射逻辑和 `jsonschema` struct tag（见[工具](tools.md)） |

### 设置与读取

请求级用 `Request.WithResponseFormat`；Client 默认值用 `callable.WithResponseFormat`（请求级覆盖默认值）。读取结果用 `Response.DecodeJSON(&v)`——它就是 `json.Unmarshal(resp.Text, v)` 的封装，但配合格式约束使用时几乎不会失败：

```go
type Recipe struct {
    Name  string   `json:"name" jsonschema:"description=菜名"`
    Steps []string `json:"steps" jsonschema:"description=烹饪步骤"`
}

resp, err := client.Create(ctx, callable.NewRequest(
    callable.User("给我一个松饼食谱"),
).WithResponseFormat(callable.JSONSchemaFor[Recipe]("recipe", true)))
if err != nil {
    log.Fatal(err)
}

var recipe Recipe
if err := resp.DecodeJSON(&recipe); err != nil {
    log.Fatal(err)
}
```

也可以不设格式约束直接 `DecodeJSON`——它适用于任何 JSON 文本响应；只是没有约束时模型可能返回非 JSON，解析会报错。

### 各 provider 的映射

同一个 `ResponseFormat` 映射到各家的原生控制字段：

| Provider | wire 字段 | 自由 JSON 模式 | schema 模式 |
|---|---|---|---|
| OpenAI Chat Completions | `response_format` | `{"type":"json_object"}` | `{"type":"json_schema","json_schema":{name,schema,strict}}` |
| OpenAI Responses | `text.format` | `{"type":"json_object"}` | `{"type":"json_schema",name,schema,strict}` |
| Anthropic Messages | `output_config.format` | `{"type":"json_schema","schema":{"type":"object"}}` | `{"type":"json_schema","schema":...}` |

几个要点：

- **Anthropic 没有自由 JSON 模式**：`JSONMode()` 被映射为一个宽松的对象 schema（`{"type":"object"}`）。其 strict 是天生的——schema 总是被强制约束，`Strict` 标志对它没有影响。
- **Anthropic 只接受 JSON Schema 子集**：不支持 `minimum` / `minLength` 等约束关键字，带了会返回 400 `*APIError`。要跨 provider 复用同一份 schema 时，只保留 `type` / `properties` / `required` / `description` / `enum` 这类结构关键字，约束条件写进 prompt 或字段描述里。
- 使用自由 JSON 模式时，OpenAI 系端点要求 prompt 中明确提到 JSON（例如「以 JSON 回答」），否则可能报错或输出非 JSON。
- **支持程度因端点而异**（基于各家官方文档与 DeepSeek 实测）：DeepSeek 和 GLM/Z.AI 只接受 `json_object`，库会自动把 schema 模式降级为 `json_object` 并把 schema 注入提示词（按 BaseURL 自动嗅探，无需手动处理）；Qwen 百炼、火山方舟（beta）、Kimi 原生支持 `json_schema`，原样发送。第三方 Anthropic 兼容端点的行为取决于网关实现（例如 DeepSeek 的 Anthropic 端点会静默忽略 `output_config`，不报错也不约束输出）。对行为不确定的端点，用 `DecodeJSON` 的返回错误兜底。

### 完整示例

```go
package main

import (
    "context"
    "fmt"
    "os"

    callable "github.com/great-magician-01/callable"
)

type Answer struct {
    Summary string   `json:"summary" jsonschema:"description=一句话总结"`
    Points  []string `json:"points" jsonschema:"description=要点列表"`
}

func main() {
    ctx := context.Background()
    client := callable.NewOpenAIClient(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL, "gpt-5")

    resp, err := client.Create(ctx, callable.NewRequest(
        callable.User("总结 Go 的 interface，以 JSON 回答"),
    ).WithResponseFormat(callable.JSONSchemaFor[Answer]("answer", true)))
    if err != nil {
        fmt.Fprintln(os.Stderr, "Create:", err)
        os.Exit(1)
    }

    var answer Answer
    if err := resp.DecodeJSON(&answer); err != nil {
        fmt.Fprintln(os.Stderr, "DecodeJSON:", err)
        os.Exit(1)
    }
    fmt.Println(answer.Summary)
    for _, p := range answer.Points {
        fmt.Println("-", p)
    }
}
```

## 采样参数：top_p 与停止序列

| 层级 | API |
|---|---|
| 请求级 | `req.WithTopP(v float64)` / `req.WithStopSequences(seq ...string)` |
| Client 默认值 | `callable.WithTopP(v)` / `callable.WithStopSequences(seq ...)` |

与 `WithTemperature` 一样，请求级覆盖 Client 默认值。各 provider 的映射：

| Provider | top_p | 停止序列 |
|---|---|---|
| OpenAI Chat Completions | `top_p` | `stop` |
| OpenAI Responses | `top_p` | 不支持（该 API 无 stop 参数，设置后被忽略） |
| Anthropic Messages | `top_p` | `stop_sequences` |

**思考模式下省略采样参数**：开启 thinking（`WithThinking`，见[思考模式](thinking.md)）后，`temperature` 与 `top_p` 都不会发送——推理模型通常拒绝自定义采样参数（400）。停止序列不受影响，照常发送。

## 相关文档

- [快速开始](getting-started.md) — Client 默认值与请求/响应钩子
- [工具](tools.md) — `jsonschema` struct tag 的完整写法
- [思考模式](thinking.md) — thinking 配置与各端点映射
- [错误处理](errors.md) — `APIError` 与重试语义
