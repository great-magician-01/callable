# 图片输入

**中文** | [English](../en/images.md)

callable 支持在消息中附带图片，与文本自由混排。你只需用统一的方式构造 `ImagePart`（本地路径 / 远程 URL / 原始字节），发送时由目标 provider 自动转换成各家 wire 格式——同一份消息历史可以不加修改地发给任何 provider。

## 三种构造方式

```go
func Image(ref string) ImagePart                  // 本地路径，或以 http(s):// 开头的远程 URL
func ImageURL(url string) ImagePart               // 远程 http(s) URL
func ImageBytes(data []byte, mediaType string) ImagePart // 原始字节 + 显式媒体类型
```

三者都返回 `callable.ImagePart`，其字段为：

| 字段 | 类型 | 说明 |
|---|---|---|
| `Path` | `string` | 本地文件路径 |
| `URL` | `string` | 远程图片 URL，原样透传给 API |
| `Data` | `[]byte` | 原始图片字节 |
| `MediaType` | `string` | MIME 类型（如 `"image/png"`），留空时自动识别 |

### `callable.Image(ref)`：最常用

```go
callable.Image("/tmp/截图.png")          // 本地路径
callable.Image("https://cdn.example.com/x.jpg") // 以 http:// 或 https:// 开头 → 等价于 ImageURL
```

`Image` 只按前缀 `http://` / `https://` 分流：命中则视为远程 URL（等同于 `ImageURL`），否则视为本地路径。其余形式的引用（如 `file://`、相对 URL）都会被当作本地文件路径。

### `callable.ImageURL(url)`：远程 URL 透传

URL 不做任何本地下载或校验，原样放进请求体，由服务端自行抓取。三种 wire 格式都原生支持远程图片 URL，所以远程图是开销最小的用法（不产生 base64 体积膨胀）。

### `callable.ImageBytes(data, mediaType)`：内存中的字节

适用于图片已在内存中的场景（截图、生成的图、从数据库/网络读出的字节）：

```go
callable.ImageBytes(pngData, "image/png")
callable.ImageBytes(pngData, "") // 留空 → 用 http.DetectContentType 嗅探
```

**注意**：`mediaType` 必须是完整 MIME 类型（`"image/png"`、`"image/jpeg"` 等），或者留空让库嗅探。传 `"png"` 这类裸扩展名会被视为非法类型而报错（见下文「媒体类型识别」）。

## 图文混排与一条消息多图

`callable.User(...)` 接受任意多个字符串和 `Part`，按传入顺序组成消息内容：

```go
msg := callable.User(
    "请对比这两张截图，",
    callable.Image("/tmp/before.png"),
    callable.Image("/tmp/after.png"),
    "说出它们的主要差异。",
)
```

一条消息可以包含任意数量的图片与文本片段；图片只在 user 消息中有效——assistant / tool 消息里的 `ImagePart` 在转换时会被忽略（各家 API 也不接受非 user 角色的图片输入）。

## 惰性解析：构造期只存引用，发送期按 provider 转换

`Image` / `ImageURL` / `ImageBytes` 都只是把引用存进 `ImagePart`，**不读文件、不编码、不校验**。真正的解析发生在每次请求的 provider 转换阶段：

- 本地路径 → `os.ReadFile` 读字节 → 识别媒体类型 → base64 编码；
- 远程 URL → 直接透传，不下载；
- 字节 → 直接使用，必要时嗅探媒体类型。

转换结果按 wire 格式不同而不同：

| 统一模型 | Chat Completions | Responses | Anthropic Messages |
|---|---|---|---|
| 本地/字节图片 | `image_url`，值为 `data:<mime>;base64,...` | `input_image`，`image_url` 为 data URL | `{type:"image", source:{type:"base64", media_type, data}}` |
| 远程 URL 图片 | `image_url`，值为原 URL | `input_image`，`image_url` 为原 URL | `{type:"image", source:{type:"url", url}}` |

因为解析是「每次请求、按目标 provider」进行的，所以**同一份消息历史可以跨 provider 复用**：上午发给 Anthropic 的历史（其中图片以 base64 source 发出），下午换成 OpenAI client 再发，同样的 `ImagePart` 会自动转为 data URL。业务代码完全不用改。

这也意味着：**本地文件在每次发送时都会重新读取**（没有缓存）。文件在两次请求之间被改动或删除，行为随之变化——删除会导致请求直接报错。

## 媒体类型识别

本地/字节图片的媒体类型按以下优先级确定：

1. **显式 `MediaType`**（`ImageBytes` 的第二个参数）非空 → 直接使用；
2. **扩展名识别**（仅本地路径）：`.jpg` / `.jpeg` → `image/jpeg`，`.png` → `image/png`，`.gif` → `image/gif`，`.webp` → `image/webp`（大小写不敏感）；
3. **内容嗅探兜底**：`http.DetectContentType` 读取文件头判断。

最终类型必须以 `image/` 开头，否则请求构造即失败，返回明确错误：

```
callable: unsupported image media type "application/octet-stream" (supported: jpeg, png, gif, webp)
```

`http.DetectContentType` 认不出的数据会得到 `application/octet-stream`，从而触发上面的错误。其他解析期错误：

- `callable: image part has no path, url or data` —— 空的 `ImagePart`；
- `callable: read image <path>: ...` —— 本地文件读取失败（不存在、无权限等）。

这些错误都在请求发出前的 payload 构造阶段返回，不会产生 HTTP 请求。

## 序列化与持久化

`ImagePart` 与所有 Part 一样支持 JSON 序列化（带 `"type":"image"` 判别字段，见[消息模型](messages.md)）：`Path` / `URL` / `MediaType` 原样保存，`Data` 以 base64 字符串保存。因此含图片的会话历史可以落盘恢复；恢复后再次发送时仍会走上述惰性解析流程。

## 注意事项与坑

- **图片只能是 user 消息内容**。放在 assistant 消息中的 `ImagePart` 会被静默丢弃。
- **大图成本**：本地图会 base64 内联进请求体，体积约为原图的 4/3，且计入模型上下文；多轮对话中历史图片每轮都会随请求重发。能用 URL 就用 URL。
- **URL 可达性由服务端负责**：`ImageURL` 不做本地校验，URL 失效、需要鉴权或内网地址都会在服务端报错，而非本地。
- **媒体类型参数是 MIME 不是扩展名**：`ImageBytes(data, "png")` 会报 `unsupported image media type "png"`，应传 `"image/png"` 或留空嗅探。
- **每次请求重新读盘**：高频发送同一张本地图时，考虑先用 `os.ReadFile` 读一次再 `ImageBytes` 复用，避免重复 IO。

## 完整示例

基于 `examples/vision/main.go`：自动选择 Anthropic 或 OpenAI，发送一张图并流式打印描述。

```go
// 用法：OPENAI_API_KEY=... go run ./examples/vision photo.png
package main

import (
	"context"
	"fmt"
	"os"

	callable "github.com/great-magician-01/callable"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: go run ./examples/vision <image path or URL>")
		os.Exit(1)
	}
	imageRef := os.Args[1] // 本地路径或 http(s) URL 均可

	ctx := context.Background()

	// 按可用的 API key 选择 provider；同一份消息对两者都有效
	var client *callable.Client
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		client = callable.NewClient(
			callable.NewAnthropicProvider(key, callable.AnthropicURL),
			callable.WithModel("claude-sonnet-5"),
		)
	} else if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		client = callable.NewClient(
			callable.NewOpenAIProvider(key, callable.OpenAIURL),
			callable.WithModel("gpt-5"),
		)
	} else {
		fmt.Println("Set ANTHROPIC_API_KEY or OPENAI_API_KEY.")
		os.Exit(1)
	}

	// 图文混排：文本 + 图片，Image 自动区分路径与 URL
	msg := callable.User("详细描述这张图片的内容。", callable.Image(imageRef))

	_, err := client.Stream(ctx, callable.NewRequest(msg), func(ev callable.Event) {
		if d, ok := ev.(callable.TextDeltaEvent); ok {
			fmt.Print(d.Delta)
		}
	})
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

图片输入同样可以直接用于 [Agent 循环](agent.md)与[多轮会话](session.md)：`agent.Run(ctx, callable.User("看图说话", callable.Image("a.png")))`，历史中的图片在后续轮次自动携带。

## 相关文档

- [消息模型](messages.md) — `Message` / `Part` 家族与序列化
- [快速开始](getting-started.md) — Client 与 provider 的创建
- [流式事件](streaming.md) — 流式接收模型输出
