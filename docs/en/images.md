# Image Input

[中文](../zh/images.md) | **English**

callable lets you attach images to messages, freely mixed with text. You build an `ImagePart` the same way regardless of provider (local path / remote URL / raw bytes), and the target provider converts it to its own wire format at send time — the same message history can be sent to any provider unchanged.

## Three Constructors

```go
func Image(ref string) ImagePart                       // local path, or a remote URL starting with http(s)://
func ImageURL(url string) ImagePart                    // remote http(s) URL
func ImageBytes(data []byte, mediaType string) ImagePart // raw bytes + explicit media type
```

All three return a `callable.ImagePart`:

| Field | Type | Description |
|---|---|---|
| `Path` | `string` | Local file path |
| `URL` | `string` | Remote image URL, passed through to the API untouched |
| `Data` | `[]byte` | Raw image bytes |
| `MediaType` | `string` | MIME type (e.g. `"image/png"`); detected automatically when empty |

### `callable.Image(ref)`: the common case

```go
callable.Image("/tmp/screenshot.png")                 // local path
callable.Image("https://cdn.example.com/x.jpg")       // starts with http:// or https:// → same as ImageURL
```

`Image` dispatches purely on the `http://` / `https://` prefix: a match is treated as a remote URL (equivalent to `ImageURL`), anything else as a local file path. Other reference forms (`file://`, relative URLs) are treated as local paths.

### `callable.ImageURL(url)`: remote URL pass-through

The URL is never downloaded or validated locally; it goes into the request body as-is and the server fetches it. All three wire formats support remote image URLs natively, so remote images are the cheapest option (no base64 size inflation).

### `callable.ImageBytes(data, mediaType)`: in-memory bytes

For images you already hold in memory (screenshots, generated images, bytes read from a database or the network):

```go
callable.ImageBytes(pngData, "image/png")
callable.ImageBytes(pngData, "") // empty → sniffed via http.DetectContentType
```

**Note**: `mediaType` must be a full MIME type (`"image/png"`, `"image/jpeg"`, ...) or empty for auto-detection. A bare extension like `"png"` is rejected as an invalid type (see "Media Type Detection" below).

## Mixing Text and Images, Multiple Images per Message

`callable.User(...)` accepts any number of strings and `Part`s, composing the message content in argument order:

```go
msg := callable.User(
    "Compare these two screenshots,",
    callable.Image("/tmp/before.png"),
    callable.Image("/tmp/after.png"),
    "and describe the main differences.",
)
```

A message can hold any number of images and text parts. Images are only valid in user messages — an `ImagePart` inside an assistant or tool message is silently dropped during conversion (the provider APIs don't accept images in non-user roles anyway).

## Lazy Resolution: References at Build Time, Conversion at Send Time

`Image` / `ImageURL` / `ImageBytes` only store a reference in the `ImagePart` — **no file reads, no encoding, no validation** at construction. Resolution happens once per request, inside the provider's payload conversion:

- local path → read with `os.ReadFile` → detect media type → base64-encode;
- remote URL → passed through, never downloaded;
- raw bytes → used directly, media type sniffed if needed.

The converted form depends on the wire format:

| Unified model | Chat Completions | Responses | Anthropic Messages |
|---|---|---|---|
| Local/bytes image | `image_url` with a `data:<mime>;base64,...` URL | `input_image` with `image_url` = data URL | `{type:"image", source:{type:"base64", media_type, data}}` |
| Remote URL image | `image_url` with the original URL | `input_image` with `image_url` = original URL | `{type:"image", source:{type:"url", url}}` |

Because resolution happens per request, per target provider, **the same message history is reusable across providers**: a history sent to Anthropic in the morning (images as base64 sources) can be resent through an OpenAI client in the afternoon, and the same `ImagePart`s are automatically rendered as data URLs. No business-code changes.

A corollary: **local files are re-read on every send** (there is no cache). If the file changes between requests, the new content is sent; if it is deleted, the request fails.

## Media Type Detection

The media type of a local/bytes image is determined by priority:

1. **Explicit `MediaType`** (the second argument of `ImageBytes`) — used as-is when non-empty;
2. **Extension mapping** (local paths only): `.jpg` / `.jpeg` → `image/jpeg`, `.png` → `image/png`, `.gif` → `image/gif`, `.webp` → `image/webp` (case-insensitive);
3. **Content sniffing fallback**: `http.DetectContentType` inspects the file header.

The final type must start with `image/`, otherwise payload construction fails with a clear error:

```
callable: unsupported image media type "application/octet-stream" (supported: jpeg, png, gif, webp)
```

Data that `http.DetectContentType` cannot identify yields `application/octet-stream`, which triggers exactly this error. Other resolution-time errors:

- `callable: image part has no path, url or data` — an empty `ImagePart`;
- `callable: read image <path>: ...` — the local file could not be read (missing, permission denied, ...).

All of these surface during payload construction, before any HTTP request is made.

## Serialization and Persistence

Like every Part, `ImagePart` is JSON-serializable (with a `"type":"image"` discriminator — see [Message Model](messages.md)): `Path` / `URL` / `MediaType` are stored as-is and `Data` as a base64 string. A conversation history containing images can therefore be persisted and restored; sending it again still goes through the lazy-resolution pipeline above.

## Caveats

- **Images belong to user messages only.** An `ImagePart` in an assistant message is silently dropped.
- **Large images are expensive.** Local images are inlined as base64 (~4/3 of the original size) and count against the model's context; in multi-turn conversations, historical images are resent with every request. Prefer URLs when possible.
- **URL reachability is the server's problem.** `ImageURL` does no local validation — dead links, auth-gated or intranet URLs fail server-side, not locally.
- **`mediaType` is a MIME type, not an extension.** `ImageBytes(data, "png")` fails with `unsupported image media type "png"`; pass `"image/png"` or leave it empty to sniff.
- **Local files are re-read per request.** When sending the same local image repeatedly, consider `os.ReadFile` once plus `ImageBytes` to avoid repeated disk IO.

## Complete Example

Based on `examples/vision/main.go`: pick Anthropic or OpenAI automatically, send an image, and stream the description.

```go
// Usage: OPENAI_API_KEY=... go run ./examples/vision photo.png
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
	imageRef := os.Args[1] // local path or http(s) URL

	ctx := context.Background()

	// Pick a provider by available API key; the same message works for both.
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

	// Mixed text + image; Image dispatches path vs. URL automatically.
	msg := callable.User("Describe this image in detail.", callable.Image(imageRef))

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

Image input works the same inside the [Agent loop](agent.md) and [sessions](session.md): `agent.Run(ctx, callable.User("describe this", callable.Image("a.png")))`, and images in the history are carried automatically across turns.

## See Also

- [Message Model](messages.md) — the `Message` / `Part` family and serialization
- [Getting Started](getting-started.md) — creating clients and providers
- [Streaming Events](streaming.md) — receiving model output incrementally
