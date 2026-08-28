// Context compaction demo against a real DeepSeek endpoint. A session with a
// deliberately tiny context window ingests a long document, so the
// auto-compact threshold is reached within a couple of turns and the history
// is replaced by a model-generated summary.
//
// Reads DEEPSEEK_API_KEY, DEEPSEEK_BASE_URL_OPENAI, DEEPSEEK_MODEL from the
// environment (e.g. via the repo-root .env):
//
//	set -a && source .env && set +a && go run ./examples/compact
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	callable "github.com/great-magician-01/callable"
)

func main() {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Println("需要 DEEPSEEK_API_KEY（DEEPSEEK_BASE_URL_OPENAI / DEEPSEEK_MODEL 可选）")
		os.Exit(1)
	}
	baseURL := os.Getenv("DEEPSEEK_BASE_URL_OPENAI")
	if baseURL == "" {
		baseURL = callable.DeepSeekURL
	}
	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}

	client := callable.NewClient(
		callable.NewOpenAIProvider(apiKey, baseURL),
		callable.WithModel(model),
	)
	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("你是一个务实的助手，回答尽量简短。"),
	)

	// 窗口故意设得很小：2000 tokens 的 60% = 1200，几轮对话就能触发压缩。
	sess := agent.Session(
		callable.WithContextWindow(2000),
		callable.WithAutoCompact(true),
		callable.WithAutoCompactThreshold(0.6),
	)

	compactions := 0
	handler := func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta)
		case callable.SessionCompactEvent:
			compactions++
			fmt.Printf("\n>>> [auto compact] 压缩前 %d tokens，摘要如下：\n%s\n", e.TokensBefore, e.Summary)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	ask := func(label string, msg callable.Message) {
		fmt.Printf("\n===== %s =====\n", label)
		result, err := sess.AskStream(ctx, handler, msg)
		fmt.Println()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		u := sess.ContextUsage()
		fmt.Printf("[%s] tokens: in=%d out=%d | 上下文占用 %d/%d (%.0f%%) | 历史消息数 %d\n",
			label, u.InputTokens, u.OutputTokens,
			u.ContextTokens, sess.ContextWindow(), sess.ContextFillRatio()*100,
			len(sess.History()))
		_ = result
	}

	// 第一轮：塞入一份长材料（约数千 token），里面埋一个暗号。
	material := "【内部通知】本月团建地点的暗号是「星河咖啡馆」，请勿外泄。\n" +
		strings.Repeat("公司季度报告显示各业务线稳步增长，研发、市场与客服团队的协作效率持续提升，客户满意度创下新高。", 40)
	ask("turn 1: 长材料（应触发压缩）", callable.User("阅读以下材料，用两三句话概括它的内容（包括其中提到的暗号）。\n\n"+material))

	// 第二轮：在压缩后的摘要历史上继续追问。
	ask("turn 2: 压缩后继续追问", callable.User("这份材料的整体基调是怎样的？一句话即可。"))

	// 第三轮：模型只能靠压缩摘要回忆暗号。
	ask("turn 3: 压缩后回忆暗号", callable.User("还记得材料里的暗号吗？"))

	fmt.Printf("\n===== 共发生 %d 次自动压缩 =====\n", compactions)
	fmt.Println("压缩后的完整历史：")
	for i, m := range sess.History() {
		text := m.Text()
		if len([]rune(text)) > 200 {
			text = string([]rune(text)[:200]) + "…"
		}
		fmt.Printf("  [%d] %s: %s\n", i, m.Role, text)
	}
}
