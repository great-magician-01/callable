package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func chatProviderWithBase(base string) *OpenAIProvider {
	return NewOpenAIProvider("test-key", base)
}

func TestChatBuildPayloadBasics(t *testing.T) {
	p := chatProviderWithBase("https://compat.example.com/api")
	body, err := p.buildPayload(NewRequest(
		System("be nice"),
		User("hello"),
		Assistant("hi"),
		User("how are you"),
	).WithModel("m1").WithMaxTokens(123).WithTemperature(0.5), false)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, body)

	if asString(t, m["model"]) != "m1" {
		t.Errorf("model = %v", m["model"])
	}
	if asFloat(t, m["max_tokens"]) != 123 {
		t.Errorf("max_tokens = %v (compat endpoints use max_tokens)", m["max_tokens"])
	}
	if _, present := m["max_completion_tokens"]; present {
		t.Errorf("max_completion_tokens should not be set on compat endpoints")
	}
	if asFloat(t, m["temperature"]) != 0.5 {
		t.Errorf("temperature = %v", m["temperature"])
	}

	msgs := asSlice(t, m["messages"])
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4", len(msgs))
	}
	first := asMap(t, msgs[0])
	if asString(t, first["role"]) != "system" || asString(t, first["content"]) != "be nice" {
		t.Errorf("system message = %v", first)
	}
	second := asMap(t, msgs[1])
	if asString(t, second["role"]) != "user" || asString(t, second["content"]) != "hello" {
		t.Errorf("user message = %v", second)
	}
}

func TestChatBuildPayloadOfficialOpenAI(t *testing.T) {
	p := chatProviderWithBase("https://api.openai.com/v1")
	body, err := p.buildPayload(NewRequest(User("hi")).WithModel("gpt-5").WithMaxTokens(99), true)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, body)
	if _, present := m["max_tokens"]; present {
		t.Errorf("official OpenAI should use max_completion_tokens")
	}
	if asFloat(t, m["max_completion_tokens"]) != 99 {
		t.Errorf("max_completion_tokens = %v", m["max_completion_tokens"])
	}
	if m["stream"] != true {
		t.Errorf("stream = %v", m["stream"])
	}
	so := asMap(t, m["stream_options"])
	if so["include_usage"] != true {
		t.Errorf("stream_options = %v", m["stream_options"])
	}
}

func TestChatBuildPayloadTools(t *testing.T) {
	p := chatProviderWithBase("https://compat.example.com")
	tool := NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		return nil, nil
	})
	body, err := p.buildPayload(NewRequest(User("weather?")).WithTools(tool), false)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, body)
	if asString(t, m["tool_choice"]) != "auto" {
		t.Errorf("tool_choice = %v", m["tool_choice"])
	}
	tools := asSlice(t, m["tools"])
	tw := asMap(t, tools[0])
	if asString(t, tw["type"]) != "function" {
		t.Errorf("tool type = %v", tw["type"])
	}
	fn := asMap(t, tw["function"])
	if asString(t, fn["name"]) != "get_weather" {
		t.Errorf("tool name = %v", fn["name"])
	}
	if _, ok := fn["parameters"]; !ok {
		t.Errorf("tool parameters missing: %v", fn)
	}
}

func TestChatBuildPayloadThinking(t *testing.T) {
	// Standard OpenAI-compatible endpoint: reasoning_effort.
	p := chatProviderWithBase("https://api.somewhere.com/v1")
	body, _ := p.buildPayload(NewRequest(User("hi")).WithThinking(Thinking{Effort: EffortHigh}), false)
	m := decodeMap(t, body)
	if asString(t, m["reasoning_effort"]) != "high" {
		t.Errorf("reasoning_effort = %v", m["reasoning_effort"])
	}
	if _, present := m["temperature"]; present {
		t.Errorf("temperature must be omitted when thinking is on")
	}

	// GLM: thinking object.
	p = chatProviderWithBase("https://open.bigmodel.cn/api/paas/v4")
	body, _ = p.buildPayload(NewRequest(User("hi")).WithThinking(Thinking{Effort: EffortMedium}), false)
	m = decodeMap(t, body)
	thinking := asMap(t, m["thinking"])
	if asString(t, thinking["type"]) != "enabled" {
		t.Errorf("glm thinking = %v", m["thinking"])
	}
	if _, present := m["reasoning_effort"]; present {
		t.Errorf("reasoning_effort should not be sent to GLM")
	}

	// Explicitly disabled on GLM.
	body, _ = p.buildPayload(NewRequest(User("hi")).WithThinking(Thinking{}), false)
	m = decodeMap(t, body)
	thinking = asMap(t, m["thinking"])
	if asString(t, thinking["type"]) != "disabled" {
		t.Errorf("glm disabled thinking = %v", m["thinking"])
	}

	// Qwen: enable_thinking.
	p = chatProviderWithBase("https://dashscope.aliyuncs.com/compatible-mode/v1")
	body, _ = p.buildPayload(NewRequest(User("hi")).WithThinking(Thinking{Effort: EffortLow}), false)
	m = decodeMap(t, body)
	if m["enable_thinking"] != true {
		t.Errorf("qwen enable_thinking = %v", m["enable_thinking"])
	}

	// No thinking config: nothing sent.
	p = chatProviderWithBase("https://api.somewhere.com/v1")
	body, _ = p.buildPayload(NewRequest(User("hi")), false)
	m = decodeMap(t, body)
	for _, key := range []string{"reasoning_effort", "thinking", "enable_thinking"} {
		if _, present := m[key]; present {
			t.Errorf("%s should be absent when thinking is nil", key)
		}
	}
}

func TestChatBuildPayloadImages(t *testing.T) {
	p := chatProviderWithBase("https://compat.example.com")
	body, err := p.buildPayload(NewRequest(User("what is this?", ImageBytes(pngHeader, "image/png"))), false)
	if err != nil {
		t.Fatal(err)
	}
	msgs := asSlice(t, decodeMap(t, body)["messages"])
	content := asSlice(t, asMap(t, msgs[0])["content"])
	if len(content) != 2 {
		t.Fatalf("content parts = %d, want 2", len(content))
	}
	if asString(t, asMap(t, content[0])["type"]) != "text" {
		t.Errorf("part 0 = %v", content[0])
	}
	img := asMap(t, content[1])
	if asString(t, img["type"]) != "image_url" {
		t.Fatalf("part 1 = %v", img)
	}
	url := asString(t, asMap(t, img["image_url"])["url"])
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("image url = %q", url)
	}
}

func TestChatBuildPayloadToolCallHistory(t *testing.T) {
	p := chatProviderWithBase("https://compat.example.com")
	req := NewRequest(
		User("weather?"),
		Assistant(ToolCallPart{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Beijing"}`}),
		ToolResults(ToolResultPart{ToolCallID: "call_1", Name: "get_weather", Content: "sunny"}),
		Assistant("It is sunny"),
	)
	req.Messages[1].Parts = append(req.Messages[1].Parts, ThinkingPart{Text: "thoughts"})
	body, err := p.buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	msgs := asSlice(t, decodeMap(t, body)["messages"])

	assistant := asMap(t, msgs[1])
	calls := asSlice(t, assistant["tool_calls"])
	call := asMap(t, calls[0])
	if asString(t, call["id"]) != "call_1" {
		t.Errorf("tool call id = %v", call["id"])
	}
	fn := asMap(t, call["function"])
	if asString(t, fn["name"]) != "get_weather" || asString(t, fn["arguments"]) != `{"city":"Beijing"}` {
		t.Errorf("function = %v", fn)
	}
	if asString(t, assistant["reasoning_content"]) != "thoughts" {
		t.Errorf("reasoning_content = %v", assistant["reasoning_content"])
	}

	toolMsg := asMap(t, msgs[2])
	if asString(t, toolMsg["role"]) != "tool" || asString(t, toolMsg["tool_call_id"]) != "call_1" {
		t.Errorf("tool message = %v", toolMsg)
	}
	if asString(t, toolMsg["content"]) != "sunny" {
		t.Errorf("tool content = %v", toolMsg["content"])
	}
}

func TestChatParseResponse(t *testing.T) {
	p := chatProviderWithBase("https://compat.example.com")
	fixture := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "Let me check.",
				"reasoning_content": "thinking hard",
				"tool_calls": [{
					"id": "call_9",
					"type": "function",
					"function": {"name": "get_weather", "arguments": "{\"city\":\"Oslo\"}"}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 20,
			"prompt_tokens_details": {"cached_tokens": 3},
			"completion_tokens_details": {"reasoning_tokens": 5}}
	}`
	resp, err := p.parseResponse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopReasonToolCalls {
		t.Errorf("stop reason = %v", resp.StopReason)
	}
	if resp.Text != "Let me check." {
		t.Errorf("text = %q", resp.Text)
	}
	if got := resp.Message.Thinking(); got != "thinking hard" {
		t.Errorf("thinking = %q", got)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_9" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments != `{"city":"Oslo"}` {
		t.Errorf("arguments = %q", resp.ToolCalls[0].Arguments)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 20 ||
		resp.Usage.CacheReadTokens != 3 || resp.Usage.ReasoningTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestChatParseResponseError(t *testing.T) {
	p := chatProviderWithBase("https://compat.example.com")
	_, err := p.parseResponse([]byte(`{"error": {"type": "invalid_request_error", "message": "bad"}}`))
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if apiErr.Type != "invalid_request_error" || apiErr.Message != "bad" {
		t.Errorf("api error = %+v", apiErr)
	}
}

func TestChatStreamAccumulation(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"role":"assistant","reasoning_content":"think "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"more"}}]}`,
		`{"choices":[{"delta":{"content":"Hi "}}]}`,
		`{"choices":[{"delta":{"content":"there"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Oslo\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"clock","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":9}}`,
	}

	state := &chatStreamState{}
	var events []Event
	for _, c := range chunks {
		if err := state.processChunk(c, func(ev Event) { events = append(events, ev) }); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := state.assemble()
	if err != nil {
		t.Fatal(err)
	}

	if resp.Text != "Hi there" {
		t.Errorf("text = %q", resp.Text)
	}
	if got := resp.Message.Thinking(); got != "think more" {
		t.Errorf("thinking = %q", got)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != "get_weather" || resp.ToolCalls[0].Arguments != `{"city":"Oslo"}` {
		t.Errorf("tool call 0 = %+v", resp.ToolCalls[0])
	}
	if resp.ToolCalls[1].Name != "clock" || resp.ToolCalls[1].Arguments != "{}" {
		t.Errorf("tool call 1 = %+v", resp.ToolCalls[1])
	}
	if resp.StopReason != StopReasonToolCalls {
		t.Errorf("stop reason = %v", resp.StopReason)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 9 {
		t.Errorf("usage = %+v", resp.Usage)
	}

	// Event stream sanity.
	var textDeltas, thinkingDeltas, toolDeltas int
	for _, ev := range events {
		switch ev.(type) {
		case TextDeltaEvent:
			textDeltas++
		case ThinkingDeltaEvent:
			thinkingDeltas++
		case ToolCallDeltaEvent:
			toolDeltas++
		}
	}
	if textDeltas != 2 || thinkingDeltas != 2 || toolDeltas != 4 {
		t.Errorf("deltas: text=%d thinking=%d tool=%d", textDeltas, thinkingDeltas, toolDeltas)
	}
}

func TestChatStreamErrorChunk(t *testing.T) {
	state := &chatStreamState{}
	err := state.processChunk(`{"error":{"type":"overloaded_error","message":"slow down"}}`, nil)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error = %T (%v)", err, err)
	}
	if apiErr.Message != "slow down" {
		t.Errorf("message = %q", apiErr.Message)
	}
}

func TestChatExtraPassthrough(t *testing.T) {
	p := chatProviderWithBase("https://compat.example.com")
	body, err := p.buildPayload(NewRequest(User("hi")).WithExtra("custom_flag", true), false)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, body)
	if m["custom_flag"] != true {
		t.Errorf("custom_flag = %v", m["custom_flag"])
	}
}

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
	server := newMockServer(t, []string{toolCallTurn, finalTurn}, &requestBodies)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))

	var gotArgs weatherArgs
	weather := NewTool("get_weather", "", func(ctx context.Context, args weatherArgs) (any, error) {
		gotArgs = args
		return "sunny", nil
	})
	agent := NewAgent(client, WithTools(weather))

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
