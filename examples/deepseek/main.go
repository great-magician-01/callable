// DeepSeek real-API test harness. Exercises every wire format the library
// supports against live DeepSeek endpoints. Reads DEEPSEEK_API_KEY,
// DEEPSEEK_BASE_URL_OPENAI, DEEPSEEK_BASE_URL_ANTHROPIC, DEEPSEEK_MODEL
// from the environment (e.g. via the repo-root .env).
//
//	set -a && source .env && set +a && go run ./examples/deepseek all
//	go run ./examples/deepseek chat        # single scenario
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	callable "github.com/great-magician-01/callable"
)

var (
	apiKey         = os.Getenv("DEEPSEEK_API_KEY")
	openAIBase     = os.Getenv("DEEPSEEK_BASE_URL_OPENAI")
	anthropicBase  = os.Getenv("DEEPSEEK_BASE_URL_ANTHROPIC")
	model          = os.Getenv("DEEPSEEK_MODEL")
	passed, failed int
)

func main() {
	if apiKey == "" || openAIBase == "" || anthropicBase == "" || model == "" {
		fmt.Println("需要 DEEPSEEK_API_KEY / DEEPSEEK_BASE_URL_OPENAI / DEEPSEEK_BASE_URL_ANTHROPIC / DEEPSEEK_MODEL")
		os.Exit(1)
	}

	scenarios := map[string]func() error{
		"chat":           testChat,           // OpenAI Chat Completions: Create + Stream
		"chat-think":     testChatThinking,   // Chat Completions + 思考(reasoning_content)
		"chat-agent":     testChatAgent,      // Chat Completions + 工具调用 agent loop
		"chat-session":   testChatSession,    // Chat Completions 多轮会话
		"responses":      testResponses,      // OpenAI Responses: Create + Stream
		"responses-think": testResponsesThink, // Responses + 思考
		"anthropic":      testAnthropic,      // Anthropic Messages: Create + Stream
		"anthropic-think": testAnthropicThink, // Anthropic + 思考
	}

	names := os.Args[1:]
	if len(names) == 0 || names[0] == "all" {
		names = []string{"chat", "chat-think", "chat-agent", "chat-session",
			"responses", "responses-think", "anthropic", "anthropic-think"}
	}
	for _, name := range names {
		fn, ok := scenarios[name]
		if !ok {
			fmt.Println("未知场景:", name)
			os.Exit(1)
		}
		run(name, fn)
	}
	fmt.Printf("\n===== 结果: %d 通过, %d 失败 =====\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func run(name string, fn func() error) {
	fmt.Printf("\n===== [%s] =====\n", name)
	start := time.Now()
	err := fn()
	if err != nil {
		failed++
		fmt.Printf("❌ [%s] 失败 (%s): %v\n", name, time.Since(start).Round(time.Millisecond), err)
	} else {
		passed++
		fmt.Printf("✅ [%s] 通过 (%s)\n", name, time.Since(start).Round(time.Millisecond))
	}
}

func openAIClient() *callable.Client {
	return callable.NewClient(
		callable.NewOpenAIProvider(apiKey, openAIBase),
		callable.WithModel(model),
	)
}

func responsesClient() *callable.Client {
	return callable.NewClient(
		callable.NewOpenAIResponsesProvider(apiKey, openAIBase),
		callable.WithModel(model),
	)
}

func anthropicClient() *callable.Client {
	return callable.NewClient(
		callable.NewAnthropicProvider(apiKey, anthropicBase),
		callable.WithModel(model),
		callable.WithMaxTokens(8192),
	)
}

// check 验证一次调用结果的最基本不变量。
func check(resp *callable.Response, err error) error {
	if err != nil {
		return err
	}
	if strings.TrimSpace(resp.Text) == "" {
		return fmt.Errorf("空回复 (stop=%s, usage=%+v)", resp.StopReason, resp.Usage)
	}
	fmt.Printf("\n[stop=%s usage: in=%d out=%d reasoning=%d cache_read=%d]\n%s\n",
		resp.StopReason, resp.Usage.InputTokens, resp.Usage.OutputTokens,
		resp.Usage.ReasoningTokens, resp.Usage.CacheReadTokens, resp.Text)
	return nil
}

func streamHandler() func(callable.Event) {
	return func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.ThinkingDeltaEvent:
			fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", e.Delta) // dim
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta)
		case callable.ToolCallEvent:
			fmt.Printf("\n[tool call] %s(%s)\n", e.Call.Name, e.Call.Arguments)
		case callable.ToolResultEvent:
			fmt.Printf("\n[tool result] %s -> %s\n", e.Call.Name, e.Result.Content)
		case callable.TurnStartEvent:
			fmt.Printf("\n--- turn %d ---\n", e.Turn)
		}
	}
}

// --- OpenAI Chat Completions ---

func testChat() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := openAIClient()

	fmt.Println("--- Create (非流式) ---")
	resp, err := c.Create(ctx, callable.NewRequest(callable.User("用一句话解释什么是闭包")))
	if err := check(resp, err); err != nil {
		return fmt.Errorf("Create: %w", err)
	}

	fmt.Println("--- Stream (流式) ---")
	resp, err = c.Stream(ctx, callable.NewRequest(callable.User("用三句话介绍 Go 的 goroutine")), streamHandler())
	return check(resp, err)
}

func testChatThinking() error {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	c := openAIClient()

	req := callable.NewRequest(callable.User("9.11 和 9.9 哪个大？")).
		WithThinking(callable.Thinking{Effort: callable.EffortMedium})
	resp, err := c.Stream(ctx, req, streamHandler())
	if err != nil {
		return err
	}
	// DeepSeek 输出 reasoning_content,库应解析为 ThinkingPart。
	hasThinking := false
	for _, p := range resp.Message.Parts {
		if _, ok := p.(callable.ThinkingPart); ok {
			hasThinking = true
		}
	}
	fmt.Printf("\n[thinking part 存在: %v]\n", hasThinking)
	return check(resp, nil)
}

type WeatherArgs struct {
	City string `json:"city" jsonschema:"description=城市名称，例如：北京"`
	Unit string `json:"unit,omitempty" jsonschema:"description=温度单位,enum=celsius,enum=fahrenheit"`
}

func testChatAgent() error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	weatherCalls := 0
	weather := callable.NewTool("get_weather", "查询指定城市的实时天气",
		func(ctx context.Context, args WeatherArgs) (any, error) {
			weatherCalls++
			unit := "°C"
			if args.Unit == "fahrenheit" {
				unit = "°F"
			}
			return fmt.Sprintf(`{"city":%q,"temp":26,"unit":%q,"condition":"晴"}`, args.City, unit), nil
		})

	agent := callable.NewAgent(openAIClient(),
		callable.WithSystemPrompt("你是一个务实的天气助手。"),
		callable.WithTools(weather),
		callable.WithMaxTurns(10),
	)

	result, err := agent.RunStream(ctx, streamHandler(), callable.User("北京和上海现在多少度？用摄氏度。"))
	if err != nil {
		return err
	}
	fmt.Printf("\n[turns=%d weather 被调用 %d 次 tokens: in=%d out=%d]\n",
		result.Turns, weatherCalls, result.Usage.InputTokens, result.Usage.OutputTokens)
	if weatherCalls == 0 {
		return fmt.Errorf("工具未被调用")
	}
	if !strings.Contains(result.FinalText, "北京") || !strings.Contains(result.FinalText, "上海") {
		return fmt.Errorf("最终回答未包含两个城市: %q", result.FinalText)
	}
	return nil
}

func testChatSession() error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	agent := callable.NewAgent(openAIClient(),
		callable.WithSystemPrompt("你是一个记忆力很好的助手。"),
	)
	sess := agent.Session()

	r1, err := sess.Ask(ctx, callable.User("请记住一个暗号：蓝色苹果。只说“记住了”。"))
	if err != nil {
		return fmt.Errorf("第 1 轮: %w", err)
	}
	fmt.Println("[turn 1]", r1.FinalText)

	r2, err := sess.Ask(ctx, callable.User("暗号是什么？"))
	if err != nil {
		return fmt.Errorf("第 2 轮: %w", err)
	}
	fmt.Println("[turn 2]", r2.FinalText)
	if !strings.Contains(r2.FinalText, "蓝色苹果") {
		return fmt.Errorf("第 2 轮未记住暗号: %q", r2.FinalText)
	}
	fmt.Printf("[history 消息数: %d]\n", len(sess.History()))
	return nil
}

// --- OpenAI Responses ---

func testResponses() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := responsesClient()

	fmt.Println("--- Create (非流式) ---")
	resp, err := c.Create(ctx, callable.NewRequest(callable.User("用一句话解释什么是递归")))
	if err := check(resp, err); err != nil {
		return fmt.Errorf("Create: %w", err)
	}

	fmt.Println("--- Stream (流式) ---")
	resp, err = c.Stream(ctx, callable.NewRequest(callable.User("列出三个 Go 的优点")), streamHandler())
	return check(resp, err)
}

func testResponsesThink() error {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	c := responsesClient()

	req := callable.NewRequest(callable.User("100 以内最大的质数是多少？")).
		WithThinking(callable.Thinking{Effort: callable.EffortLow})
	resp, err := c.Stream(ctx, req, streamHandler())
	return check(resp, err)
}

// --- Anthropic Messages ---

func testAnthropic() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := anthropicClient()

	fmt.Println("--- Create (非流式) ---")
	resp, err := c.Create(ctx, callable.NewRequest(callable.User("用一句话解释什么是接口")))
	if err := check(resp, err); err != nil {
		return fmt.Errorf("Create: %w", err)
	}

	fmt.Println("--- Stream (流式) ---")
	resp, err = c.Stream(ctx, callable.NewRequest(callable.User("用两句话解释 channel")), streamHandler())
	return check(resp, err)
}

func testAnthropicThink() error {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	c := anthropicClient()

	req := callable.NewRequest(callable.User("一个自然数的各位数字之和是 9，这个数一定被 9 整除吗？只回答“是”或“否”。")).
		WithThinking(callable.Thinking{Effort: callable.EffortLow})
	resp, err := c.Stream(ctx, req, streamHandler())
	return check(resp, err)
}
