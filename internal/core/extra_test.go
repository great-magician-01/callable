package core

import (
	"context"
	"encoding/json"
	. "github.com/great-magician-01/callable/internal/testutil"
	"strings"
	"testing"
)

// TestAntCreatePath exercises the non-streaming Anthropic code path.
func TestAntCreatePath(t *testing.T) {
	jsonResp := `{"content":[{"type":"thinking","thinking":"deep","signature":"sigX"},
		{"type":"text","text":"done"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":7}}`
	server := NewMockJSONServer(t, []string{jsonResp}, nil)
	client := NewClient(NewAnthropicProvider("k", server.URL), WithModel("claude-x"))

	resp, err := client.Create(context.Background(), NewRequest(User("hi")).WithThinking(Thinking{Effort: EffortLow}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "done" {
		t.Errorf("text = %q", resp.Text)
	}
	tp, ok := resp.Message.Parts[0].(ThinkingPart)
	if !ok || tp.Signature != "sigX" {
		t.Errorf("thinking part = %+v", resp.Message.Parts[0])
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 7 ||
		resp.Usage.ContextTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

// TestResponsesCreatePath exercises the non-streaming Responses code path.
func TestResponsesCreatePath(t *testing.T) {
	jsonResp := `{"id":"resp_1","status":"completed","output":[
		{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"pondered"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}
	],"usage":{"input_tokens":5,"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2}}}`
	server := NewMockJSONServer(t, []string{jsonResp}, nil)
	client := NewClient(NewOpenAIResponsesProvider("k", server.URL), WithModel("gpt-x"))

	resp, err := client.Create(context.Background(), NewRequest(User("hi")).WithThinking(Thinking{Effort: EffortLow}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "done" {
		t.Errorf("text = %q", resp.Text)
	}
	if got := resp.Message.Thinking(); got != "pondered" {
		t.Errorf("thinking = %q", got)
	}
	if resp.Message.ProviderExtra(client.Provider().Name()) == nil {
		t.Errorf("reasoning item not preserved for replay")
	}
	if resp.Usage.ReasoningTokens != 2 || resp.Usage.ContextTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

// TestAgentSkillReadHook verifies the agent-level skill read hook and tool
// rename options.
func TestAgentSkillReadHook(t *testing.T) {
	readSkillTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "load_skill", `{"name":"pdf"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	var bodies []string

	agent := chatAgentFixture(t, []string{readSkillTurn, finalTurn}, &bodies,
		WithSkills(NewSkill("pdf", "Export PDFs", "base instructions")),
		WithSkillToolName("load_skill"),
		WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
			return content + " +hooked", nil
		}),
	)

	if _, err := agent.RunStream(context.Background(), noopEvents, User("export")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[1], "base instructions +hooked") {
		t.Errorf("hook-modified skill content not delivered: %s", bodies[1])
	}
	if !strings.Contains(bodies[0], "load_skill") {
		t.Errorf("renamed tool not referenced: %s", bodies[0])
	}
}

// TestSessionSetHistory restores a persisted conversation and continues it.
func TestSessionSetHistory(t *testing.T) {
	answer := ChatSSE(
		`{"choices":[{"delta":{"content":"yes"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	var bodies []string
	agent := chatAgentFixture(t, []string{answer}, &bodies)

	sess := agent.Session()
	sess.SetHistory([]Message{User("earlier question"), Assistant("earlier answer")})
	if _, err := sess.AskStream(context.Background(), noopEvents, User("follow up?")); err != nil {
		t.Fatal(err)
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	var nonSystem []string
	for _, m := range req.Messages {
		if m.Role != "system" {
			nonSystem = append(nonSystem, m.Role)
		}
	}
	if strings.Join(nonSystem, ",") != "user,assistant,user" {
		t.Errorf("roles = %v, want restored history first", nonSystem)
	}
}
