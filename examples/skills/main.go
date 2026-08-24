// Skills: progressive disclosure. Only the skill index (name + description)
// goes into the system prompt; the model loads the full instructions through
// the built-in read_skill tool when it needs them. A read hook demonstrates
// injecting runtime context into the loaded skill.
//
//	OPENAI_API_KEY=... go run ./examples/skills
package main

import (
	"context"
	"fmt"
	"os"
	"time"

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

	weatherSkill := callable.NewSkill("weather-report",
		"生成正式的天气报告文档",
		`# 天气报告生成规范

收到天气数据后，按以下格式输出报告：

1. 标题：`+"《城市名》天气报告`"+`
2. 概览表格：日期、温度、天气、风力
3. 一段生活建议

规则：
- 温度保留整数
- 使用摄氏度
- 建议控制在 100 字以内`)

	agent := callable.NewAgent(client,
		callable.WithSkills(weatherSkill),
		// The read hook runs every time the model loads a skill; use it to
		// append runtime context, audit loads, or lazily fetch content.
		callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
			return content + "\n\n(报告生成时间: " + time.Now().Format("2006-01-02 15:04") + ")", nil
		}),
	)

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		if d, ok := ev.(callable.TextDeltaEvent); ok {
			fmt.Print(d.Delta)
		}
	}, callable.User("北京今天 26 度，晴，微风。帮我生成天气报告。"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	_ = result
	fmt.Println()
}

func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
