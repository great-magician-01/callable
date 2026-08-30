package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/great-magician-01/callable/internal/model"
	skill "github.com/great-magician-01/callable/internal/skill"
	. "github.com/great-magician-01/callable/internal/testutil"
)

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
		WithSkills(skill.NewSkill("pdf", "Export PDFs", "base instructions")),
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
