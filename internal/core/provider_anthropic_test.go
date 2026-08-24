package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func antProvider() *AnthropicProvider {
	return NewAnthropicProvider("test-key", "https://anthropic.example.com")
}

func TestAntBuildPayloadBasics(t *testing.T) {
	p := antProvider()
	body, err := p.buildPayload(NewRequest(
		System("be nice"),
		User("hello"),
		Assistant("hi"),
		User("bye"),
	).WithModel("claude-x").WithMaxTokens(77), false)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, body)

	if asString(t, m["model"]) != "claude-x" {
		t.Errorf("model = %v", m["model"])
	}
	if asFloat(t, m["max_tokens"]) != 77 {
		t.Errorf("max_tokens = %v", m["max_tokens"])
	}
	if asString(t, m["system"]) != "be nice" {
		t.Errorf("system = %v", m["system"])
	}
	// The two user messages and the assistant turn stay separate.
	msgs := asSlice(t, m["messages"])
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	if asString(t, asMap(t, msgs[0])["role"]) != "user" {
		t.Errorf("first message role = %v", msgs[0])
	}
}

func TestAntBuildPayloadMaxTokensDefault(t *testing.T) {
	p := antProvider()
	body, _ := p.buildPayload(NewRequest(User("hi")).WithModel("claude-x"), false)
	if asFloat(t, decodeMap(t, body)["max_tokens"]) != 4096 {
		t.Errorf("default max_tokens = %v", decodeMap(t, body)["max_tokens"])
	}
}

func TestAntBuildPayloadThinking(t *testing.T) {
	p := antProvider()
	body, err := p.buildPayload(
		NewRequest(User("hi")).WithModel("claude-x").WithMaxTokens(100).WithThinking(Thinking{Effort: EffortMedium}),
		false)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, body)
	thinking := asMap(t, m["thinking"])
	if asString(t, thinking["type"]) != "enabled" {
		t.Errorf("thinking = %v", m["thinking"])
	}
	if asFloat(t, thinking["budget_tokens"]) != 8192 {
		t.Errorf("budget_tokens = %v", thinking["budget_tokens"])
	}
	// max_tokens must be bumped above the budget.
	if asFloat(t, m["max_tokens"]) != 8192+2048 {
		t.Errorf("max_tokens = %v, want budget+2048", m["max_tokens"])
	}
	// Temperature must be omitted in thinking mode.
	if _, present := m["temperature"]; present {
		t.Errorf("temperature set while thinking")
	}

	// Explicit budget overrides effort mapping.
	body, _ = p.buildPayload(
		NewRequest(User("hi")).WithModel("claude-x").WithThinking(Thinking{BudgetTokens: 2000}), false)
	thinking = asMap(t, decodeMap(t, body)["thinking"])
	if asFloat(t, thinking["budget_tokens"]) != 2000 {
		t.Errorf("explicit budget = %v", thinking["budget_tokens"])
	}
}

func TestAntBuildPayloadContentBlocks(t *testing.T) {
	p := antProvider()
	req := NewRequest(
		System("s"),
		User("what is this?", ImageBytes(pngHeader, "image/png")),
		Assistant(
			ThinkingPart{Text: "thoughts", Signature: "sig1"},
			ToolCallPart{ID: "toolu_1", Name: "get_weather", Arguments: `{"city":"Oslo"}`},
		),
		ToolResults(ToolResultPart{ToolCallID: "toolu_1", Name: "get_weather", Content: "sunny", IsError: false}),
	).WithModel("claude-x")

	body, err := p.buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	msgs := asSlice(t, decodeMap(t, body)["messages"])

	userBlocks := asSlice(t, asMap(t, msgs[0])["content"])
	if asString(t, asMap(t, userBlocks[0])["type"]) != "text" {
		t.Errorf("user block 0 = %v", userBlocks[0])
	}
	imgBlock := asMap(t, userBlocks[1])
	if asString(t, imgBlock["type"]) != "image" {
		t.Fatalf("user block 1 = %v", imgBlock)
	}
	src := asMap(t, imgBlock["source"])
	if asString(t, src["type"]) != "base64" || asString(t, src["media_type"]) != "image/png" {
		t.Errorf("image source = %v", src)
	}
	if !strings.HasPrefix(asString(t, src["data"]), "iVBOR") {
		t.Errorf("image data = %q", src["data"])
	}

	asstBlocks := asSlice(t, asMap(t, msgs[1])["content"])
	thinkingBlock := asMap(t, asstBlocks[0])
	if asString(t, thinkingBlock["type"]) != "thinking" ||
		asString(t, thinkingBlock["thinking"]) != "thoughts" ||
		asString(t, thinkingBlock["signature"]) != "sig1" {
		t.Errorf("thinking block = %v", thinkingBlock)
	}
	toolUse := asMap(t, asstBlocks[1])
	if asString(t, toolUse["type"]) != "tool_use" || asString(t, toolUse["id"]) != "toolu_1" {
		t.Errorf("tool_use block = %v", toolUse)
	}
	// input must be a JSON object, not a string.
	input := asMap(t, toolUse["input"])
	if input["city"] != "Oslo" {
		t.Errorf("tool_use input = %v", toolUse["input"])
	}

	resultBlocks := asSlice(t, asMap(t, msgs[2])["content"])
	toolResult := asMap(t, resultBlocks[0])
	if asString(t, toolResult["type"]) != "tool_result" ||
		asString(t, toolResult["tool_use_id"]) != "toolu_1" ||
		asString(t, toolResult["content"]) != "sunny" {
		t.Errorf("tool_result block = %v", toolResult)
	}
}

func TestAntBuildPayloadTools(t *testing.T) {
	p := antProvider()
	tool := NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		return nil, nil
	})
	body, _ := p.buildPayload(NewRequest(User("hi")).WithModel("claude-x").WithTools(tool), false)
	tools := asSlice(t, decodeMap(t, body)["tools"])
	tw := asMap(t, tools[0])
	if asString(t, tw["name"]) != "get_weather" {
		t.Errorf("tool = %v", tw)
	}
	schema := asMap(t, tw["input_schema"])
	if schema["type"] != "object" {
		t.Errorf("input_schema = %v", schema)
	}
}

func TestAntParseResponse(t *testing.T) {
	p := antProvider()
	fixture := `{
		"content": [
			{"type": "thinking", "thinking": "deep", "signature": "sigA"},
			{"type": "text", "text": "Checking."},
			{"type": "tool_use", "id": "toolu_9", "name": "get_weather", "input": {"city": "Oslo"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 12, "output_tokens": 34,
			"cache_read_input_tokens": 5, "cache_creation_input_tokens": 2}
	}`
	resp, err := p.parseResponse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopReasonToolCalls {
		t.Errorf("stop reason = %v", resp.StopReason)
	}
	if resp.Text != "Checking." {
		t.Errorf("text = %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "toolu_9" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments != `{"city":"Oslo"}` {
		t.Errorf("arguments = %q", resp.ToolCalls[0].Arguments)
	}
	tp, ok := resp.Message.Parts[0].(ThinkingPart)
	if !ok || tp.Signature != "sigA" || tp.Text != "deep" {
		t.Errorf("thinking part = %+v", resp.Message.Parts[0])
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 34 ||
		resp.Usage.CacheReadTokens != 5 || resp.Usage.CacheWriteTokens != 2 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestAntStreamAccumulation(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ponder"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sigZ"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_2","name":"get_weather","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Oslo\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`,
		`{"type":"message_stop"}`,
	}

	state := &antStreamState{}
	var emitted []Event
	for _, data := range events {
		ev := sseMessage{event: "x", data: data}
		if err := state.processEvent(ev, func(e Event) { emitted = append(emitted, e) }); err != nil {
			if errors.Is(err, errStopScan) {
				break
			}
			t.Fatalf("event %s: %v", data, err)
		}
	}

	var textDeltas, thinkingDeltas, toolDeltas int
	for _, e := range emitted {
		switch e.(type) {
		case TextDeltaEvent:
			textDeltas++
		case ThinkingDeltaEvent:
			thinkingDeltas++
		case ToolCallDeltaEvent:
			toolDeltas++
		}
	}
	if thinkingDeltas != 1 || toolDeltas != 3 {
		// tool deltas: 1 start (content_block_start) + 2 input_json_delta
		t.Errorf("deltas: thinking=%d tool=%d", thinkingDeltas, toolDeltas)
	}
	_ = textDeltas

	blocks := make([]antContentBlock, len(state.blocks))
	for i, b := range state.blocks {
		input := b.inputJSON.String()
		if strings.TrimSpace(input) == "" {
			input = "{}"
		}
		blocks[i] = antContentBlock{
			Type: b.Type, Thinking: b.Thinking.String(), Signature: b.Signature,
			ID: b.ID, Name: b.Name, Input: json.RawMessage(input),
		}
	}
	resp := assembleAntMessage(blocks, mapAntStopReason(state.stopReason), state.usage.toUsage())
	if got := resp.Message.Thinking(); got != "ponder" {
		t.Errorf("thinking = %q", got)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != `{"city":"Oslo"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != StopReasonToolCalls {
		t.Errorf("stop reason = %v", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 42 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	// Signature must survive for the next turn.
	tp, _ := resp.Message.Parts[0].(ThinkingPart)
	if tp.Signature != "sigZ" {
		t.Errorf("signature = %q", tp.Signature)
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
	server := newMockServer(t, []string{toolTurn, finalTurn}, &bodies)
	client := NewClient(NewAnthropicProvider("k", server.URL), WithModel("claude-x"))

	var executed []weatherArgs
	agent := NewAgent(client, WithTools(weatherTool(&executed, nil)))

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
	if result.Usage.InputTokens != 40 { // accumulated across turns
		t.Errorf("usage = %+v", result.Usage)
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

func TestAntConsecutiveUserMessagesMerged(t *testing.T) {
	p := antProvider()
	body, _ := p.buildPayload(NewRequest(
		User("a"),
		User("b"),
	).WithModel("claude-x"), false)
	msgs := asSlice(t, decodeMap(t, body)["messages"])
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1 (merged)", len(msgs))
	}
	blocks := asSlice(t, asMap(t, msgs[0])["content"])
	if len(blocks) != 2 {
		t.Errorf("blocks = %d, want 2", len(blocks))
	}
}
