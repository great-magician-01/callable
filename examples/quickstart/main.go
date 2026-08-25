// Quickstart: a minimal streaming chat against an OpenAI-compatible,
// OpenAI Responses or Anthropic endpoint. Set an API key env var and run:
//
//	ANTHROPIC_API_KEY=... go run ./examples/quickstart
//	OPENAI_API_KEY=... go run ./examples/quickstart
package main

import (
	"context"
	"fmt"
	"os"

	callable "github.com/great-magician-01/callable"
)

func main() {
	ctx := context.Background()

	client := newClient()
	if client == nil {
		fmt.Println("Set ANTHROPIC_API_KEY or OPENAI_API_KEY.")
		os.Exit(1)
	}

	_, err := client.Stream(ctx, callable.NewRequest(callable.User("用一句话解释什么是闭包")), func(ev callable.Event) {
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

// newClient picks a provider based on which API key is present. The endpoint
// is always passed explicitly; point OPENAI_BASE_URL / ANTHROPIC_BASE_URL at
// any compatible endpoint to override it.
func newClient() *callable.Client {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return callable.NewClient(
			callable.NewAnthropicProvider(key, firstNonEmptyEnv("ANTHROPIC_BASE_URL", callable.AnthropicURL)),
			callable.WithModel("claude-sonnet-5"),
			callable.WithMaxTokens(2048),
		)
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return callable.NewClient(
			callable.NewOpenAIProvider(key, firstNonEmptyEnv("OPENAI_BASE_URL", callable.OpenAIURL)), // chat completions; NewOpenAIResponsesProvider for /v1/responses
			callable.WithModel("gpt-5"),
		)
	}
	return nil
}

func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
