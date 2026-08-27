// Sub-agents: delegating subtasks to specialized agents with progressive
// disclosure. Sub-agents are NOT exposed as tools by default — the system
// prompt only carries a name/description index. The model must first call the
// built-in load_agent tool, which registers a call_<name> tool it can then
// invoke. Each sub-agent can have its own model, prompt, tools and skills.
//
// Reads DEEPSEEK_API_KEY, DEEPSEEK_BASE_URL_OPENAI, DEEPSEEK_MODEL from the
// environment (e.g. via the repo-root .env):
//
//	set -a && source .env && set +a && go run ./examples/subagents
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	callable "github.com/great-magician-01/callable"
)

func main() {
	key := os.Getenv("DEEPSEEK_API_KEY")
	base := os.Getenv("DEEPSEEK_BASE_URL_OPENAI")
	model := os.Getenv("DEEPSEEK_MODEL")
	if key == "" || base == "" || model == "" {
		fmt.Println("需要 DEEPSEEK_API_KEY / DEEPSEEK_BASE_URL_OPENAI / DEEPSEEK_MODEL")
		os.Exit(1)
	}

	client := callable.NewClient(callable.NewOpenAIProvider(key, base), callable.WithModel(model))

	// A translator sub-agent: model override + its own prompt.
	translator := callable.NewSubAgent("translator", "把中文翻译成地道的英文",
		callable.WithSubAgentModel(model),
		callable.WithSubAgentPrompt("你是一名专业译者。把用户给出的中文翻译成自然、地道的英文，只输出译文。"),
	)

	// A poet sub-agent: its own tools and skills, invisible to the parent.
	rhyme := callable.NewTool("find_rhyme", "查询与给定词押韵的词",
		func(ctx context.Context, args struct {
			Word string `json:"word" jsonschema:"description=要押韵的词"`
		}) (any, error) {
			return []string{"light", "night", "bright"}, nil
		})
	poet := callable.NewSubAgent("poet", "写一首英文短诗",
		callable.WithSubAgentPrompt("你是一位诗人。写 4 行英文短诗，可以先用 find_rhyme 查韵脚。"),
		callable.WithSubAgentTools(rhyme),
		callable.WithSubAgentSkills(callable.NewSkill("sonnet", "十四行诗写作规范", "# 十四行诗\n...")),
	)

	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("你是主编排者。翻译交给 translator，写诗交给 poet，不要自己动手。"),
		callable.WithSubAgents(translator, poet),
		callable.WithMaxTurns(15),
		callable.WithSubAgentEvents(true), // 子代理内部事件包装为 SubAgentEvent 透传
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta)
		case callable.ToolCallEvent:
			fmt.Printf("\n[tool call] %s(%s)\n", e.Call.Name, e.Call.Arguments)
		case callable.ToolResultEvent:
			fmt.Printf("\n[tool result] %s -> %s\n", e.Call.Name, e.Result.Content)
		case callable.TurnStartEvent:
			fmt.Printf("\n--- turn %d ---\n", e.Turn)
		case callable.SubAgentEvent:
			// 子代理内部事件：灰暗色缩进展示，与父 agent 输出区分。
			switch se := e.Event.(type) {
			case callable.TextDeltaEvent:
				fmt.Printf("\033[2m%s\033[0m", se.Delta)
			case callable.ToolCallEvent:
				fmt.Printf("\n  [%s] tool call %s(%s)\n", e.SubAgent, se.Call.Name, se.Call.Arguments)
			case callable.ToolResultEvent:
				fmt.Printf("\n  [%s] tool result %s -> %s\n", e.SubAgent, se.Call.Name, se.Result.Content)
			}
		}
	}, callable.User("把「月色真美」翻译成英文，再围绕它写一首短诗。"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("\n[turns=%d tokens: in=%d out=%d]\n",
		result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
}
