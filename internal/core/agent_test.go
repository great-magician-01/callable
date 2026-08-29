package core

import (
	"context"
	"encoding/json"
	"errors"
	. "github.com/great-magician-01/callable/internal/testutil"
	"strings"
	"sync"
	"testing"
)

// noopEvents discards streaming events; passing it keeps the agent on the
// streaming code path (matching SSE fixtures) without observing events.
var noopEvents = func(Event) {}

// weatherTool builds a tool that records its invocation (goroutine-safe for
// parallel tool execution tests).
func weatherTool(executed *[]weatherArgs, result func(args weatherArgs) (any, error)) Tool {
	var mu sync.Mutex
	return NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		if executed != nil {
			mu.Lock()
			*executed = append(*executed, args)
			mu.Unlock()
		}
		if result != nil {
			return result(args)
		}
		return "sunny", nil
	})
}

// chatAgentFixture builds an agent over a mock chat-completions SSE server.
func chatAgentFixture(t *testing.T, turns []string, bodies *[]string, opts ...AgentOption) *Agent {
	t.Helper()
	server := NewMockServer(t, turns, bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))
	return NewAgent(client, opts...)
}

func TestAgentToolErrorRecovery(t *testing.T) {
	var bodies []string
	toolCallTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "get_weather", `{"city":"Oslo"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"sorry, weather service failed"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var executed []weatherArgs
	agent := chatAgentFixture(t, []string{toolCallTurn, finalTurn}, &bodies,
		WithTools(weatherTool(&executed, func(args weatherArgs) (any, error) {
			return nil, errors.New("service unavailable")
		})),
	)

	result, err := agent.RunStream(context.Background(), noopEvents, User("weather?"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "sorry, weather service failed" {
		t.Fatalf("final text = %q", result.FinalText)
	}
	if len(executed) != 1 {
		t.Fatalf("tool executions = %d", len(executed))
	}

	// The failed result must be fed back to the model.
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[1]), &req); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range req.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "service unavailable") {
			found = true
		}
	}
	if !found {
		t.Errorf("error result not sent back to model: %s", bodies[1])
	}
}

func TestAgentHookDeny(t *testing.T) {
	toolCallTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "get_weather", `{"city":"Oslo"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"ok, I will not call it"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	var bodies []string

	var executed []weatherArgs
	agent := chatAgentFixture(t, []string{toolCallTurn, finalTurn}, &bodies,
		WithTools(weatherTool(&executed, nil)),
		WithToolCallHook(func(ctx context.Context, call ToolCall) (ToolDecision, error) {
			return Deny("not allowed in tests"), nil
		}),
	)

	result, err := agent.RunStream(context.Background(), noopEvents, User("weather?"))
	if err != nil {
		t.Fatal(err)
	}
	if len(executed) != 0 {
		t.Errorf("tool should not have executed")
	}
	if !strings.Contains(bodies[1], "not allowed in tests") {
		t.Errorf("denial reason not fed back: %s", bodies[1])
	}
	if result.FinalText == "" {
		t.Errorf("run should complete after denial")
	}
}

func TestAgentHookReplaceArgs(t *testing.T) {
	toolCallTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "get_weather", `{"city":"Oslo"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var executed []weatherArgs
	agent := chatAgentFixture(t, []string{toolCallTurn, finalTurn}, nil,
		WithTools(weatherTool(&executed, nil)),
		WithToolCallHook(func(ctx context.Context, call ToolCall) (ToolDecision, error) {
			return ReplaceArgs(`{"city":"Tokyo"}`), nil
		}),
	)

	if _, err := agent.RunStream(context.Background(), noopEvents, User("weather?")); err != nil {
		t.Fatal(err)
	}
	if len(executed) != 1 || executed[0].City != "Tokyo" {
		t.Errorf("executed args = %+v, want Tokyo", executed)
	}
}

func TestAgentMaxTurns(t *testing.T) {
	toolCallTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "get_weather", `{"city":"Oslo"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	var executed []weatherArgs
	agent := chatAgentFixture(t, []string{toolCallTurn, toolCallTurn, toolCallTurn}, nil,
		WithTools(weatherTool(&executed, nil)),
		WithMaxTurns(2),
	)

	result, err := agent.RunStream(context.Background(), noopEvents, User("weather?"))
	if err == nil {
		t.Fatal("expected MaxTurnsError")
	}
	var maxErr *MaxTurnsError
	if !errors.As(err, &maxErr) || maxErr.Turns != 2 {
		t.Fatalf("err = %v", err)
	}
	if result.StopReason != AgentMaxTurns {
		t.Errorf("stop reason = %q", result.StopReason)
	}
	if result.Turns != 2 {
		t.Errorf("turns = %d", result.Turns)
	}
	if maxErr.Partial != result {
		t.Errorf("MaxTurnsError.Partial should carry the partial result")
	}
}

func TestAgentUnknownTool(t *testing.T) {
	toolCallTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "nonexistent", `{}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"that tool does not exist"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	var bodies []string

	agent := chatAgentFixture(t, []string{toolCallTurn, finalTurn}, &bodies)

	result, err := agent.RunStream(context.Background(), noopEvents, User("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[1], "unknown tool") {
		t.Errorf("unknown tool error not fed back: %s", bodies[1])
	}
	if result.FinalText == "" {
		t.Errorf("run should complete")
	}
}

func TestAgentSession(t *testing.T) {
	firstAnswer := ChatSSE(
		`{"choices":[{"delta":{"content":"hello!"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	secondAnswer := ChatSSE(
		`{"choices":[{"delta":{"content":"still here"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	var bodies []string
	agent := chatAgentFixture(t, []string{firstAnswer, secondAnswer}, &bodies)

	sess := agent.Session()
	if _, err := sess.AskStream(context.Background(), noopEvents, User("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AskStream(context.Background(), noopEvents, User("again?")); err != nil {
		t.Fatal(err)
	}

	// Second request must contain the first exchange in order.
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[1]), &req); err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, m := range req.Messages {
		if m.Role != "system" {
			roles = append(roles, m.Role)
		}
	}
	if strings.Join(roles, ",") != "user,assistant,user" {
		t.Errorf("roles in second request = %v", roles)
	}
	if len(sess.History()) != 4 { // 2 user + 2 assistant
		t.Errorf("history = %d messages, want 4", len(sess.History()))
	}

	sess.Reset()
	if len(sess.History()) != 0 {
		t.Errorf("reset failed")
	}
}

func TestAgentSkillFlow(t *testing.T) {
	readSkillTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "read_skill", `{"name":"pdf"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"loaded skill, proceeding"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	var bodies []string

	agent := chatAgentFixture(t, []string{readSkillTurn, finalTurn}, &bodies,
		WithSkills(NewSkill("pdf", "Export PDFs", "# PDF export instructions")),
	)

	result, err := agent.RunStream(context.Background(), noopEvents, User("export a pdf"))
	if err != nil {
		t.Fatal(err)
	}

	// The read_skill call must have returned the full instructions.
	if !strings.Contains(bodies[1], "PDF export instructions") {
		t.Errorf("skill instructions not delivered: %s", bodies[1])
	}
	// The system prompt must contain the skill index (progressive
	// disclosure), but not the full instructions.
	if !strings.Contains(bodies[0], "pdf") || !strings.Contains(bodies[0], "Export PDFs") {
		t.Errorf("skill index missing from system prompt: %s", bodies[0])
	}
	if strings.Contains(bodies[0], "PDF export instructions") {
		t.Errorf("system prompt leaked full instructions: %s", bodies[0])
	}
	if result.FinalText == "" {
		t.Errorf("run should complete")
	}
}

func TestAgentParallelTools(t *testing.T) {
	twoCalls := ChatSSE(
		ChatToolCallChunk(0, "call_1", "get_weather", `{"city":"Oslo"}`, true),
		ChatToolCallChunk(1, "call_2", "get_weather", `{"city":"Tokyo"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"both done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var executed []weatherArgs
	agent := chatAgentFixture(t, []string{twoCalls, finalTurn}, nil,
		WithTools(weatherTool(&executed, nil)),
		WithParallelToolExecution(true),
	)

	if _, err := agent.RunStream(context.Background(), noopEvents, User("weather?")); err != nil {
		t.Fatal(err)
	}
	if len(executed) != 2 {
		t.Fatalf("executions = %d, want 2", len(executed))
	}
	cities := map[string]bool{}
	for _, a := range executed {
		cities[a.City] = true
	}
	if !cities["Oslo"] || !cities["Tokyo"] {
		t.Errorf("cities = %v", cities)
	}
}

func TestAgentCreatePath(t *testing.T) {
	// Non-streaming path: server returns regular JSON.
	toolCallJSON := `{"choices":[{"message":{"role":"assistant","content":"",
		"tool_calls":[{"id":"call_1","type":"function",
		"function":{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}}]},
		"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":7}}`
	finalJSON := `{"choices":[{"message":{"role":"assistant","content":"sunny"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":5,"completion_tokens":7}}`
	var bodies []string
	server := NewMockJSONServer(t, []string{toolCallJSON, finalJSON}, &bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))

	var executed []weatherArgs
	agent := NewAgent(client, WithTools(weatherTool(&executed, nil)))

	result, err := agent.Run(context.Background(), User("weather?"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "sunny" {
		t.Fatalf("final text = %q", result.FinalText)
	}
	if len(executed) != 1 || executed[0].City != "Oslo" {
		t.Errorf("executed = %+v", executed)
	}
	if result.Usage.OutputTokens != 14 { // accumulated across two turns
		t.Errorf("usage = %+v", result.Usage)
	}
	var streamed bool
	for _, b := range bodies {
		if strings.Contains(b, `"stream":true`) {
			streamed = true
		}
	}
	if streamed {
		t.Errorf("non-event run must not request streaming")
	}
}

func TestAgentRequiresInput(t *testing.T) {
	agent := NewAgent(NewClient(NewOpenAIProvider("k", "https://chat.example.com/v1")))
	if _, err := agent.Run(context.Background()); err == nil {
		t.Fatal("expected error for empty input")
	}
}
