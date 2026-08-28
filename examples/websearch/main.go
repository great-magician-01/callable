// Web search: an agent that answers with live web data. Web search is on by
// default ("auto"): the provider's built-in server-side search is used when
// the endpoint has one (GLM/Z.AI, Qwen, Kimi, api.anthropic.com, OpenAI
// Responses); otherwise the agent falls back to a Tavily-backed web_search
// tool when TAVILY_API_KEY is set.
//
//	GLM_API_KEY=...        go run ./examples/websearch
//	KIMI_API_KEY=...       go run ./examples/websearch
//	ANTHROPIC_API_KEY=...  go run ./examples/websearch
//	OPENAI_API_KEY=... TAVILY_API_KEY=... go run ./examples/websearch
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
		fmt.Println("Set GLM_API_KEY, KIMI_API_KEY, ANTHROPIC_API_KEY, or OPENAI_API_KEY (with TAVILY_API_KEY for the fallback).")
		os.Exit(1)
	}

	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("你是一个讲求时效的助手，需要最新信息时主动搜索。"),
		// Only used when the endpoint has no built-in search; built-in wins.
		callable.WithTavilyAPIKey(os.Getenv("TAVILY_API_KEY")),
		// callable.WithWebSearch(false) // ... or disable web search entirely.
	)

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta)
		case callable.ToolCallEvent:
			// Kimi's echo protocol shows up as a $web_search call.
			fmt.Fprintf(os.Stderr, "\n[tool call] %s(%s)\n", e.Call.Name, e.Call.Arguments)
		case callable.ToolResultEvent:
			fmt.Fprintf(os.Stderr, "\n[tool result] %s -> %s\n", e.Call.Name, e.Result.Content)
		}
	}, callable.User("最近一周 Go 语言有什么新闻？"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n\n[turns: %d, tokens: in %d / out %d]\n",
		result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
}

// newClient picks a provider based on which API key is present. GLM, Kimi and
// the official Anthropic endpoint have built-in server-side search; any other
// OpenAI-compatible endpoint works too when TAVILY_API_KEY is set — the agent
// then exposes a Tavily-backed web_search tool instead.
func newClient() *callable.Client {
	if key := os.Getenv("GLM_API_KEY"); key != "" {
		return callable.NewClient(
			callable.NewOpenAIProvider(key, firstNonEmptyEnv("GLM_BASE_URL", callable.GLMURL)),
			callable.WithModel(firstNonEmptyEnv("GLM_MODEL", "glm-4.7")),
		)
	}
	if key := os.Getenv("KIMI_API_KEY"); key != "" {
		return callable.NewClient(
			callable.NewOpenAIProvider(key, firstNonEmptyEnv("KIMI_BASE_URL", callable.KimiURL)),
			callable.WithModel(firstNonEmptyEnv("KIMI_MODEL", "moonshot-v1-8k")),
		)
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return callable.NewClient(
			callable.NewAnthropicProvider(key, firstNonEmptyEnv("ANTHROPIC_BASE_URL", callable.AnthropicURL)),
			callable.WithModel("claude-sonnet-5"),
			callable.WithMaxTokens(4096),
		)
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		// api.openai.com chat completions has no built-in search here; set
		// TAVILY_API_KEY for the fallback tool (or point OPENAI_BASE_URL at a
		// built-in-capable compatible endpoint).
		return callable.NewClient(
			callable.NewOpenAIProvider(key, firstNonEmptyEnv("OPENAI_BASE_URL", callable.OpenAIURL)),
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
