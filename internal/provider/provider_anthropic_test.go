package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/great-magician-01/callable/internal/model"
	. "github.com/great-magician-01/callable/internal/testutil"
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
	m := DecodeMap(t, body)

	if AsString(t, m["model"]) != "claude-x" {
		t.Errorf("model = %v", m["model"])
	}
	if AsFloat(t, m["max_tokens"]) != 77 {
		t.Errorf("max_tokens = %v", m["max_tokens"])
	}
	if AsString(t, m["system"]) != "be nice" {
		t.Errorf("system = %v", m["system"])
	}
	// The two user messages and the assistant turn stay separate.
	msgs := AsSlice(t, m["messages"])
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	if AsString(t, AsMap(t, msgs[0])["role"]) != "user" {
		t.Errorf("first message role = %v", msgs[0])
	}
}

func TestAntBuildPayloadMaxTokensDefault(t *testing.T) {
	p := antProvider()
	body, _ := p.buildPayload(NewRequest(User("hi")).WithModel("claude-x"), false)
	if AsFloat(t, DecodeMap(t, body)["max_tokens"]) != 4096 {
		t.Errorf("default max_tokens = %v", DecodeMap(t, body)["max_tokens"])
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
	m := DecodeMap(t, body)
	thinking := AsMap(t, m["thinking"])
	if AsString(t, thinking["type"]) != "enabled" {
		t.Errorf("thinking = %v", m["thinking"])
	}
	if AsFloat(t, thinking["budget_tokens"]) != 8192 {
		t.Errorf("budget_tokens = %v", thinking["budget_tokens"])
	}
	// max_tokens must be bumped above the budget.
	if AsFloat(t, m["max_tokens"]) != 8192+2048 {
		t.Errorf("max_tokens = %v, want budget+2048", m["max_tokens"])
	}
	// Temperature must be omitted in thinking mode.
	if _, present := m["temperature"]; present {
		t.Errorf("temperature set while thinking")
	}

	// Explicit budget overrides effort mapping.
	body, _ = p.buildPayload(
		NewRequest(User("hi")).WithModel("claude-x").WithThinking(Thinking{BudgetTokens: 2000}), false)
	thinking = AsMap(t, DecodeMap(t, body)["thinking"])
	if AsFloat(t, thinking["budget_tokens"]) != 2000 {
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
	msgs := AsSlice(t, DecodeMap(t, body)["messages"])

	userBlocks := AsSlice(t, AsMap(t, msgs[0])["content"])
	if AsString(t, AsMap(t, userBlocks[0])["type"]) != "text" {
		t.Errorf("user block 0 = %v", userBlocks[0])
	}
	imgBlock := AsMap(t, userBlocks[1])
	if AsString(t, imgBlock["type"]) != "image" {
		t.Fatalf("user block 1 = %v", imgBlock)
	}
	src := AsMap(t, imgBlock["source"])
	if AsString(t, src["type"]) != "base64" || AsString(t, src["media_type"]) != "image/png" {
		t.Errorf("image source = %v", src)
	}
	if !strings.HasPrefix(AsString(t, src["data"]), "iVBOR") {
		t.Errorf("image data = %q", src["data"])
	}

	asstBlocks := AsSlice(t, AsMap(t, msgs[1])["content"])
	thinkingBlock := AsMap(t, asstBlocks[0])
	if AsString(t, thinkingBlock["type"]) != "thinking" ||
		AsString(t, thinkingBlock["thinking"]) != "thoughts" ||
		AsString(t, thinkingBlock["signature"]) != "sig1" {
		t.Errorf("thinking block = %v", thinkingBlock)
	}
	toolUse := AsMap(t, asstBlocks[1])
	if AsString(t, toolUse["type"]) != "tool_use" || AsString(t, toolUse["id"]) != "toolu_1" {
		t.Errorf("tool_use block = %v", toolUse)
	}
	// input must be a JSON object, not a string.
	input := AsMap(t, toolUse["input"])
	if input["city"] != "Oslo" {
		t.Errorf("tool_use input = %v", toolUse["input"])
	}

	resultBlocks := AsSlice(t, AsMap(t, msgs[2])["content"])
	toolResult := AsMap(t, resultBlocks[0])
	if AsString(t, toolResult["type"]) != "tool_result" ||
		AsString(t, toolResult["tool_use_id"]) != "toolu_1" ||
		AsString(t, toolResult["content"]) != "sunny" {
		t.Errorf("tool_result block = %v", toolResult)
	}
}

func TestAntBuildPayloadTools(t *testing.T) {
	p := antProvider()
	tool := NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		return nil, nil
	})
	body, _ := p.buildPayload(NewRequest(User("hi")).WithModel("claude-x").WithTools(tool), false)
	tools := AsSlice(t, DecodeMap(t, body)["tools"])
	tw := AsMap(t, tools[0])
	if AsString(t, tw["name"]) != "get_weather" {
		t.Errorf("tool = %v", tw)
	}
	schema := AsMap(t, tw["input_schema"])
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
		resp.Usage.CacheReadTokens != 5 || resp.Usage.CacheWriteTokens != 2 ||
		resp.Usage.ContextTokens != 19 { // 12 + 5 + 2 (Anthropic input excludes cache)
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
	resp := assembleAntMessage(blocks, mapAntStopReason(state.stopReason), state.usage.toUsage(), nil)
	if got := resp.Message.Thinking(); got != "ponder" {
		t.Errorf("thinking = %q", got)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != `{"city":"Oslo"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != StopReasonToolCalls {
		t.Errorf("stop reason = %v", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 42 ||
		resp.Usage.ContextTokens != 10 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	// Signature must survive for the next turn.
	tp, _ := resp.Message.Parts[0].(ThinkingPart)
	if tp.Signature != "sigZ" {
		t.Errorf("signature = %q", tp.Signature)
	}
}

func TestAntConsecutiveUserMessagesMerged(t *testing.T) {
	p := antProvider()
	body, _ := p.buildPayload(NewRequest(
		User("a"),
		User("b"),
	).WithModel("claude-x"), false)
	msgs := AsSlice(t, DecodeMap(t, body)["messages"])
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1 (merged)", len(msgs))
	}
	blocks := AsSlice(t, AsMap(t, msgs[0])["content"])
	if len(blocks) != 2 {
		t.Errorf("blocks = %d, want 2", len(blocks))
	}
}

func TestAntParseResponseUnknownBlocks(t *testing.T) {
	p := antProvider()
	fixture := `{
		"id": "msg_1",
		"model": "claude-x",
		"content": [
			{"type": "server_tool_use", "id": "srvtoolu_1", "name": "web_search", "input": {"query": "news"}},
			{"type": "web_search_tool_result", "tool_use_id": "srvtoolu_1", "content": [{"type": "web_search_result", "url": "https://x", "title": "X"}]},
			{"type": "text", "text": "Here is the news."},
			{"type": "redacted_thinking", "data": "EoreBkAI"}
		],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 5, "output_tokens": 7, "server_tool_use": {"web_search_requests": 1}}
	}`
	resp, err := p.parseResponse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Here is the news." {
		t.Errorf("text = %q", resp.Text)
	}
	var raws []RawPart
	for _, part := range resp.Message.Parts {
		if rp, ok := part.(RawPart); ok {
			raws = append(raws, rp)
		}
	}
	if len(raws) != 3 {
		t.Fatalf("raw parts = %d, want 3: %+v", len(raws), resp.Message.Parts)
	}
	if raws[0].BlockType != "server_tool_use" || raws[0].Provider != "anthropic" {
		t.Errorf("raw part 0 = %+v", raws[0])
	}
	// The block is preserved verbatim, in its original format.
	blk := DecodeMap(t, raws[0].Raw)
	if AsString(t, blk["name"]) != "web_search" {
		t.Errorf("raw name = %v", blk["name"])
	}
	if AsString(t, AsMap(t, blk["input"])["query"]) != "news" {
		t.Errorf("raw input = %v", blk["input"])
	}
	if raws[2].BlockType != "redacted_thinking" {
		t.Errorf("raw part 2 = %+v", raws[2])
	}
	if AsString(t, DecodeMap(t, raws[2].Raw)["data"]) != "EoreBkAI" {
		t.Errorf("redacted data = %s", raws[2].Raw)
	}
	// Envelope fields the unified model does not map survive verbatim.
	if string(resp.Extra["id"]) != `"msg_1"` || string(resp.Extra["model"]) != `"claude-x"` {
		t.Errorf("extra = %v", resp.Extra)
	}
	if _, ok := resp.Extra["content"]; ok {
		t.Error("known field content must not land in Extra")
	}
	// Usage fields beyond the schema survive too.
	if string(resp.Extra["usage"]) != "" {
		t.Error("usage must not land in Extra")
	}
	if _, ok := resp.Usage.Extra["server_tool_use"]; !ok {
		t.Errorf("usage extra = %v", resp.Usage.Extra)
	}
}

func TestAntStreamUnknownBlock(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_9","model":"claude-x","usage":{"input_tokens":3,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"news\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}

	state := &antStreamState{}
	for _, data := range events {
		ev := sseMessage{event: "x", data: data}
		if err := state.processEvent(ev, nil); err != nil {
			if errors.Is(err, errStopScan) {
				break
			}
			t.Fatalf("event %s: %v", data, err)
		}
	}
	resp := state.assemble()

	if len(resp.Message.Parts) != 1 {
		t.Fatalf("parts = %+v", resp.Message.Parts)
	}
	rp, ok := resp.Message.Parts[0].(RawPart)
	if !ok {
		t.Fatalf("part type = %T", resp.Message.Parts[0])
	}
	if rp.BlockType != "server_tool_use" || rp.Provider != "anthropic" {
		t.Errorf("raw part = %+v", rp)
	}
	// The streamed input_json delta is folded back into the raw block.
	blk := DecodeMap(t, rp.Raw)
	if AsString(t, blk["id"]) != "srvtoolu_1" {
		t.Errorf("raw id = %v", blk["id"])
	}
	if AsString(t, AsMap(t, blk["input"])["query"]) != "news" {
		t.Errorf("raw input = %s", rp.Raw)
	}
	// message_start metadata survives on Response.Extra.
	if string(resp.Extra["id"]) != `"msg_9"` || string(resp.Extra["model"]) != `"claude-x"` {
		t.Errorf("extra = %v", resp.Extra)
	}
	if _, ok := resp.Extra["usage"]; ok {
		t.Error("known field usage must not land in Extra")
	}
}

func TestAntRawPartReplay(t *testing.T) {
	p := antProvider()
	raw := json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"news"}}`)
	body, err := p.buildPayload(NewRequest(
		User("search news"),
		Assistant(TextPart{Text: "checking"}, RawPart{Provider: "anthropic", BlockType: "server_tool_use", Raw: raw}),
	).WithModel("claude-x"), false)
	if err != nil {
		t.Fatal(err)
	}
	msgs := AsSlice(t, DecodeMap(t, body)["messages"])
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	content := AsSlice(t, AsMap(t, msgs[1])["content"])
	if len(content) != 2 {
		t.Fatalf("assistant blocks = %d, want 2", len(content))
	}
	blk := AsMap(t, content[1])
	if AsString(t, blk["type"]) != "server_tool_use" {
		t.Errorf("replayed block = %v", blk)
	}
	if AsString(t, AsMap(t, blk["input"])["query"]) != "news" {
		t.Errorf("replayed input = %v", blk["input"])
	}

	// A RawPart from another provider's wire format is not replayed.
	body, err = p.buildPayload(NewRequest(
		User("hi"),
		Assistant(RawPart{Provider: "openai-responses", BlockType: "x", Raw: json.RawMessage(`{"type":"x"}`)}),
	).WithModel("claude-x"), false)
	if err != nil {
		t.Fatal(err)
	}
	msgs = AsSlice(t, DecodeMap(t, body)["messages"])
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1 (foreign RawPart skipped)", len(msgs))
	}
}

func TestAntStreamGappedBlockIndex(t *testing.T) {
	// A content_block_start at index 1 with no block at index 0 must not
	// produce a placeholder RawPart for the gap.
	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_stop"}`,
	}
	state := &antStreamState{}
	for _, data := range events {
		ev := sseMessage{event: "x", data: data}
		if err := state.processEvent(ev, nil); err != nil {
			if errors.Is(err, errStopScan) {
				break
			}
			t.Fatalf("event %s: %v", data, err)
		}
	}
	resp := state.assemble()
	if resp.Text != "hi" {
		t.Errorf("text = %q", resp.Text)
	}
	if len(resp.Message.Parts) != 1 {
		t.Fatalf("parts = %+v, want exactly the text part", resp.Message.Parts)
	}
	if _, ok := resp.Message.Parts[0].(TextPart); !ok {
		t.Errorf("part 0 type = %T", resp.Message.Parts[0])
	}
}
