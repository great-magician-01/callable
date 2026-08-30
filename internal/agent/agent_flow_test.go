package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	client "github.com/great-magician-01/callable/internal/client"
	. "github.com/great-magician-01/callable/internal/model"
	provider "github.com/great-magician-01/callable/internal/provider"
	. "github.com/great-magician-01/callable/internal/testutil"
)

// The agent-flow tests below drive a full agent loop against mock servers in
// each provider's wire format, verifying request construction (history
// replay) and stream parsing end to end. They live in the agent package (not
// the provider package) because they exercise the Agent loop.

// TestChatAgentFlow drives a full agent loop against a mock chat-completions
// server, verifying request construction and stream parsing end to end.
func TestChatAgentFlow(t *testing.T) {
	sse := func(lines ...string) string {
		var b strings.Builder
		for _, l := range lines {
			b.WriteString("data: " + l + "\n\n")
		}
		b.WriteString("data: [DONE]\n\n")
		return b.String()
	}
	toolCallTurn := sse(
		`{"choices":[{"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"Oslo\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := sse(
		`{"choices":[{"delta":{"role":"assistant","content":"It is "}}]}`,
		`{"choices":[{"delta":{"content":"sunny."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":13}}`,
	)

	var requestBodies []string
	server := NewMockServer(t, []string{toolCallTurn, finalTurn}, &requestBodies)
	c := client.NewClient(provider.NewOpenAIProvider("k", server.URL), client.WithModel("m"))

	var gotArgs weatherArgs
	weather := NewTool("get_weather", "", func(ctx context.Context, args weatherArgs) (any, error) {
		gotArgs = args
		return "sunny", nil
	})
	agent := NewAgent(c, WithTools(weather))

	var events []Event
	result, err := agent.RunStream(context.Background(), func(ev Event) { events = append(events, ev) },
		User("Weather in Oslo?"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "It is sunny." {
		t.Errorf("final text = %q", result.FinalText)
	}
	if result.Turns != 2 {
		t.Errorf("turns = %d", result.Turns)
	}
	if gotArgs.City != "Oslo" {
		t.Errorf("tool args = %+v", gotArgs)
	}
	if result.Usage.OutputTokens != 13 {
		t.Errorf("usage = %+v", result.Usage)
	}

	// The second request must contain the tool-call exchange.
	if len(requestBodies) != 2 {
		t.Fatalf("requests = %d", len(requestBodies))
	}
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(requestBodies[1]), &req); err != nil {
		t.Fatal(err)
	}
	var sawToolResult bool
	for _, m := range req.Messages {
		if m.Role == "tool" && m.ToolCallID == "call_1" {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Errorf("second request missing tool result: %s", requestBodies[1])
	}

	// Event sequence sanity.
	var kinds []string
	for _, ev := range events {
		switch ev.(type) {
		case TurnStartEvent:
			kinds = append(kinds, "turn")
		case TextDeltaEvent:
			kinds = append(kinds, "text")
		case ToolResultEvent:
			kinds = append(kinds, "tool")
		case AgentDoneEvent:
			kinds = append(kinds, "done")
		}
	}
	if len(kinds) < 4 || kinds[len(kinds)-1] != "done" {
		t.Errorf("event kinds = %v", kinds)
	}
}

func responsesSSE(events ...string) string {
	var b strings.Builder
	for _, data := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(data), &probe)
		// SSE data lines must be single-line: strip newlines from the fixture
		// (JSON whitespace between tokens is insignificant).
		data = strings.ReplaceAll(data, "\n", "")
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, data)
	}
	return b.String()
}

// TestResponsesAgentFlow verifies reasoning items are replayed on the next
// turn (required for reasoning continuity across tool calls).
func TestResponsesAgentFlow(t *testing.T) {
	turn1 := responsesSSE(
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"pondering"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":\"Oslo\"}"}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"pondering"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}
		],"usage":{"input_tokens":9,"output_tokens":12}}}`,
	)
	turn2 := responsesSSE(
		`{"type":"response.created","response":{"id":"resp_2"}}`,
		`{"type":"response.output_text.delta","delta":"It is sunny."}`,
		`{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"It is sunny."}]}
		],"usage":{"input_tokens":30,"output_tokens":8}}}`,
	)

	var bodies []string
	server := NewMockServer(t, []string{turn1, turn2}, &bodies)
	c := client.NewClient(provider.NewOpenAIResponsesProvider("k", server.URL), client.WithModel("gpt-x"))

	var executed []weatherArgs
	agent := NewAgent(c, WithTools(weatherTool(&executed, nil)))

	result, err := agent.RunStream(context.Background(), noopEvents, User("Weather in Oslo?"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "It is sunny." {
		t.Fatalf("final text = %q", result.FinalText)
	}
	if len(executed) != 1 || executed[0].City != "Oslo" {
		t.Errorf("executed = %+v", executed)
	}

	// Second request must replay the raw reasoning item and the
	// function_call/function_call_output pair.
	second := bodies[1]
	for _, want := range []string{
		`"type":"reasoning"`, `"id":"rs_1"`,
		`"type":"function_call"`, `"call_id":"call_1"`,
		`"type":"function_call_output"`, `"sunny"`,
	} {
		if !strings.Contains(second, want) {
			t.Errorf("second request missing %s:\n%s", want, second)
		}
	}
}

func antSSE(events ...string) string {
	var b strings.Builder
	for _, data := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(data), &probe)
		// SSE data lines must be single-line.
		data = strings.ReplaceAll(data, "\n", "")
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, data)
	}
	return b.String()
}

// TestAntAgentFlow verifies the full Anthropic loop, in particular that
// thinking blocks (with signature) and tool results are replayed correctly.
func TestAntAgentFlow(t *testing.T) {
	toolTurn := antSSE(
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"pondering"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig123"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Oslo\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`,
		`{"type":"message_stop"}`,
	)
	finalTurn := antSSE(
		`{"type":"message_start","message":{"usage":{"input_tokens":30,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"It is sunny."}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
		`{"type":"message_stop"}`,
	)

	var bodies []string
	server := NewMockServer(t, []string{toolTurn, finalTurn}, &bodies)
	c := client.NewClient(provider.NewAnthropicProvider("k", server.URL), client.WithModel("claude-x"))

	var executed []weatherArgs
	agent := NewAgent(c, WithTools(weatherTool(&executed, nil)))

	var thinkingDeltas int
	result, err := agent.RunStream(context.Background(), func(ev Event) {
		if _, ok := ev.(ThinkingDeltaEvent); ok {
			thinkingDeltas++
		}
	}, User("Weather in Oslo?"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "It is sunny." {
		t.Fatalf("final text = %q", result.FinalText)
	}
	if len(executed) != 1 || executed[0].City != "Oslo" {
		t.Errorf("executed = %+v", executed)
	}
	if thinkingDeltas != 1 {
		t.Errorf("thinking deltas = %d", thinkingDeltas)
	}
	if result.Usage.InputTokens != 40 || result.Usage.ContextTokens != 40 { // accumulated across turns
		t.Errorf("usage = %+v", result.Usage)
	}
	if result.LastTurnUsage.InputTokens != 30 || result.LastTurnUsage.ContextTokens != 30 {
		t.Errorf("last turn usage = %+v", result.LastTurnUsage)
	}

	// Second request: assistant thinking block with signature, tool_use, and
	// a tool_result block in a user message.
	second := bodies[1]
	for _, want := range []string{
		`"type":"thinking"`, `"signature":"sig123"`,
		`"type":"tool_use"`, `"id":"toolu_1"`,
		`"type":"tool_result"`, `"tool_use_id":"toolu_1"`,
	} {
		if !strings.Contains(second, want) {
			t.Errorf("second request missing %s:\n%s", want, second)
		}
	}
}
