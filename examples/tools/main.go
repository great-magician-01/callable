// Tools: an agent that calls a weather tool automatically, streaming every
// step (thinking, text, tool calls, tool results) as it happens.
//
//	OPENAI_API_KEY=... go run ./examples/tools
package main

import (
	"context"
	"fmt"
	"os"

	callable "github.com/great-magician-01/callable"
)

// WeatherArgs becomes the tool's JSON Schema via reflection.
type WeatherArgs struct {
	City string `json:"city" jsonschema:"description=城市名称，例如：北京"`
	Unit string `json:"unit,omitempty" jsonschema:"description=温度单位,enum=celsius,enum=fahrenheit"`
}

func main() {
	ctx := context.Background()
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	if key == "" {
		fmt.Println("Set OPENAI_API_KEY or ANTHROPIC_API_KEY.")
		os.Exit(1)
	}

	var client *callable.Client
	if os.Getenv("OPENAI_API_KEY") != "" {
		client = callable.NewClient(callable.NewOpenAIProvider(key, firstNonEmptyEnv("OPENAI_BASE_URL", callable.OpenAIURL)), callable.WithModel("gpt-5"))
	} else {
		client = callable.NewClient(callable.NewAnthropicProvider(key, firstNonEmptyEnv("ANTHROPIC_BASE_URL", callable.AnthropicURL)), callable.WithModel("claude-sonnet-5"))
	}

	weather := callable.NewTool("get_weather", "查询指定城市的实时天气",
		func(ctx context.Context, args WeatherArgs) (any, error) {
			// Replace with a real API call.
			unit := "°C"
			if args.Unit == "fahrenheit" {
				unit = "°F"
			}
			return fmt.Sprintf(`{"city":%q,"temp":26,"unit":%q,"condition":"晴"}`, args.City, unit), nil
		})

	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("你是一个务实的天气助手。"),
		callable.WithTools(weather),
		// Approval gate example: log every call before it runs.
		callable.WithToolCallHook(func(ctx context.Context, call callable.ToolCall) (callable.ToolDecision, error) {
			fmt.Printf("\n[tool call] %s(%s)\n", call.Name, call.Arguments)
			return callable.Approve(), nil
		}),
	)

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.ThinkingDeltaEvent:
			fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // dim thinking
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta)
		case callable.ToolResultEvent:
			fmt.Fprintf(os.Stderr, "\n[tool result] %s -> %s\n", e.Call.Name, e.Result.Content)
		}
	}, callable.User("北京和上海现在多少度？用摄氏度。"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n\n[turns: %d, tokens: in %d / out %d]\n",
		result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
}

func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
