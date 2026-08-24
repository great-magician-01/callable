package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func responsesProvider() *OpenAIResponsesProvider {
	return NewOpenAIResponsesProvider("test-key", "https://responses.example.com/v1")
}

// itemsToMaps round-trips input items through JSON so typed structs and raw
// messages both become plain maps for assertions.
func itemsToMaps(t *testing.T, items []any) []any {
	t.Helper()
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	var out []any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal items: %v", err)
	}
	return out
}

func TestResponsesBuildPayloadBasics(t *testing.T) {
	p := responsesProvider()
	body, err := p.buildPayload(NewRequest(
		System("be nice"),
		User("hello"),
	).WithModel("gpt-x").WithMaxTokens(88), false)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, body)

	if asString(t, m["model"]) != "gpt-x" {
		t.Errorf("model = %v", m["model"])
	}
	if asString(t, m["instructions"]) != "be nice" {
		t.Errorf("instructions = %v", m["instructions"])
	}
	if asFloat(t, m["max_output_tokens"]) != 88 {
		t.Errorf("max_output_tokens = %v", m["max_output_tokens"])
	}
	if _, present := m["reasoning"]; present {
		t.Errorf("reasoning should be absent when thinking is off")
	}
	input := asSlice(t, m["input"])
	item := asMap(t, input[0])
	if asString(t, item["role"]) != "user" {
		t.Errorf("input item = %v", item)
	}
	content := asSlice(t, item["content"])
	if asString(t, asMap(t, content[0])["type"]) != "input_text" {
		t.Errorf("input content = %v", content[0])
	}
}

func TestResponsesBuildPayloadThinking(t *testing.T) {
	p := responsesProvider()
	body, _ := p.buildPayload(
		NewRequest(User("hi")).WithModel("gpt-x").WithThinking(Thinking{Effort: EffortHigh}).WithTemperature(0.7),
		false)
	m := decodeMap(t, body)
	reasoning := asMap(t, m["reasoning"])
	if asString(t, reasoning["effort"]) != "high" {
		t.Errorf("effort = %v", reasoning["effort"])
	}
	if _, present := m["temperature"]; present {
		t.Errorf("temperature must be omitted when reasoning is on")
	}
}

func TestResponsesBuildPayloadTools(t *testing.T) {
	p := responsesProvider()
	tool := NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		return nil, nil
	})
	body, _ := p.buildPayload(NewRequest(User("hi")).WithModel("gpt-x").WithTools(tool), false)
	tools := asSlice(t, decodeMap(t, body)["tools"])
	tw := asMap(t, tools[0])
	// Responses tools are flattened, not nested under "function".
	if asString(t, tw["type"]) != "function" || asString(t, tw["name"]) != "get_weather" {
		t.Errorf("tool = %v", tw)
	}
	if _, present := tw["parameters"]; !present {
		t.Errorf("parameters missing: %v", tw)
	}
}

func TestResponsesBuildInputImage(t *testing.T) {
	p := responsesProvider()
	_, input, err := p.buildInput([]Message{
		User("look", ImageBytes(pngHeader, "image/png")),
	})
	if err != nil {
		t.Fatal(err)
	}
	items := itemsToMaps(t, input)
	item := asMap(t, items[0])
	content := asSlice(t, item["content"])
	img := asMap(t, content[1])
	if asString(t, img["type"]) != "input_image" {
		t.Fatalf("image content = %v", img)
	}
	if !strings.HasPrefix(asString(t, img["image_url"]), "data:image/png;base64,") {
		t.Errorf("image_url = %v", img["image_url"])
	}
}

func TestResponsesBuildInputReplayAndReconstruct(t *testing.T) {
	p := responsesProvider()

	// Assistant message carrying raw output items: replayed verbatim.
	asst := Message{Role: RoleAssistant, Parts: []Part{
		ThinkingPart{ID: "rs_1", Text: "summary"},
		ToolCallPart{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Oslo"}`},
	}}
	asst.SetProviderExtra(p.Name(), json.RawMessage(
		`[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"summary"}]},{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}]`))

	// Assistant message without extras: reconstructed from parts.
	plain := Message{Role: RoleAssistant, Parts: []Part{TextPart{Text: "hi there"}}}

	// Tool results must become function_call_output items.
	toolMsg := ToolResults(ToolResultPart{ToolCallID: "call_1", Name: "get_weather", Content: "sunny"})

	_, input, err := p.buildInput([]Message{asst, toolMsg, plain, User("next")})
	if err != nil {
		t.Fatal(err)
	}
	items := itemsToMaps(t, input)

	// asst -> 2 raw items
	if got := asString(t, asMap(t, items[0])["type"]); got != "reasoning" {
		t.Errorf("input[0] = %v, want raw reasoning item", items[0])
	}
	if got := asString(t, asMap(t, items[1])["type"]); got != "function_call" {
		t.Errorf("input[1] = %v, want raw function_call item", items[1])
	}
	// toolMsg -> function_call_output (must precede the next user content)
	if got := asString(t, asMap(t, items[2])["type"]); got != "function_call_output" {
		t.Errorf("input[2] = %v, want function_call_output", items[2])
	}
	if got := asString(t, asMap(t, items[2])["call_id"]); got != "call_1" {
		t.Errorf("call_id = %v", items[2])
	}
	// plain -> reconstructed assistant message item
	third := asMap(t, items[3])
	if asString(t, third["type"]) != "message" || asString(t, third["role"]) != "assistant" {
		t.Errorf("input[3] = %v, want reconstructed assistant message", items[3])
	}
	// user
	if asString(t, asMap(t, items[4])["role"]) != "user" {
		t.Errorf("input[4] = %v, want user", items[4])
	}
}

func TestResponsesAssemble(t *testing.T) {
	p := responsesProvider()
	env := &respEnvelope{
		Status: "completed",
		Output: []json.RawMessage{
			json.RawMessage(`{"type":"reasoning","id":"rs_9","summary":[{"type":"summary_text","text":"sum "},{"type":"summary_text","text":"up"}]}`),
			json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}`),
			json.RawMessage(`{"type":"function_call","call_id":"call_5","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}`),
		},
		Usage: &respUsageBody{
			InputTokens:  10,
			OutputTokens: 20,
			OutputTokensDetails: &struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			}{ReasoningTokens: 4},
		},
	}
	resp, err := p.assemble(env)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Hello" {
		t.Errorf("text = %q", resp.Text)
	}
	if got := resp.Message.Thinking(); got != "sum up" {
		t.Errorf("thinking = %q", got)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_5" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.StopReason != StopReasonToolCalls {
		t.Errorf("stop reason = %v", resp.StopReason)
	}
	if resp.Usage.ReasoningTokens != 4 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	// Raw items attached for replay.
	extra := resp.Message.ProviderExtra(p.Name())
	if !strings.Contains(string(extra), `"rs_9"`) {
		t.Errorf("provider extra = %s", extra)
	}
}

func TestResponsesAssembleIncomplete(t *testing.T) {
	p := responsesProvider()
	env := &respEnvelope{
		Status: "incomplete",
		Output: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"trunc"}]}`),
		},
	}
	resp, err := p.assemble(env)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopReasonMaxTokens {
		t.Errorf("stop reason = %v", resp.StopReason)
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

func TestResponsesStream(t *testing.T) {
	completedResponse := `{"id":"resp_1","status":"completed",
		"output":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thought"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}
		],
		"usage":{"input_tokens":9,"output_tokens":12,"output_tokens_details":{"reasoning_tokens":3}}}`
	stream := responsesSSE(
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"thought"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":\"Oslo\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}}`,
		`{"type":"response.completed","response":`+completedResponse+`}`,
	)

	var bodies []string
	server := newMockServer(t, []string{stream}, &bodies)
	client := NewClient(NewOpenAIResponsesProvider("k", server.URL), WithModel("gpt-x"))

	var events []Event
	resp, err := client.Stream(context.Background(), NewRequest(User("hi")), func(ev Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "" {
		t.Errorf("text = %q (tool-only turn)", resp.Text)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != `{"city":"Oslo"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.ReasoningTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if got := resp.Message.Thinking(); got != "thought" {
		t.Errorf("thinking = %q", got)
	}

	var sawThinking, sawToolDelta bool
	for _, ev := range events {
		switch e := ev.(type) {
		case ThinkingDeltaEvent:
			sawThinking = true
		case ToolCallDeltaEvent:
			if e.ArgsDelta != "" {
				sawToolDelta = true
			}
		}
	}
	if !sawThinking || !sawToolDelta {
		t.Errorf("stream events missing: thinking=%v tool=%v", sawThinking, sawToolDelta)
	}
	if !strings.Contains(bodies[0], `"stream":true`) {
		t.Errorf("stream flag not set")
	}
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
	server := newMockServer(t, []string{turn1, turn2}, &bodies)
	client := NewClient(NewOpenAIResponsesProvider("k", server.URL), WithModel("gpt-x"))

	var executed []weatherArgs
	agent := NewAgent(client, WithTools(weatherTool(&executed, nil)))

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
