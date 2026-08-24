// Thinking + multi-turn session: reasoning with an effort level, and a
// Session that maintains conversation history across Ask calls.
//
//	ANTHROPIC_API_KEY=... go run ./examples/thinking
package main

import (
	"context"
	"fmt"
	"os"

	callable "github.com/great-magician-01/callable"
)

func main() {
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

	agent := callable.NewAgent(client,
		// EffortLow / EffortMedium / EffortHigh map to each provider's
		// native thinking controls (budget_tokens, reasoning.effort,
		// reasoning_effort, GLM thinking, Qwen enable_thinking...).
		callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
	)

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
				fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta)
			case callable.TextDeltaEvent:
				fmt.Print(e.Delta)
			}
		}, callable.User(q))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println()
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
