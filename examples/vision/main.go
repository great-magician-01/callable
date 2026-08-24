// Vision: send images alongside text. Local files are read, typed and
// base64-encoded automatically in whichever format the provider expects;
// remote URLs pass through untouched.
//
//	OPENAI_API_KEY=... go run ./examples/vision photo.png
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
	imageRef := os.Args[1]

	ctx := context.Background()
	key := firstNonEmptyEnv("ANTHROPIC_API_KEY", "OPENAI_API_KEY")
	if key == "" {
		fmt.Println("Set ANTHROPIC_API_KEY or OPENAI_API_KEY.")
		os.Exit(1)
	}

	var client *callable.Client
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		client = callable.NewClient(callable.NewAnthropicProvider(key), callable.WithModel("claude-sonnet-5"))
	} else {
		client = callable.NewClient(callable.NewOpenAIProvider(key), callable.WithModel("gpt-5"))
	}

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

func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
