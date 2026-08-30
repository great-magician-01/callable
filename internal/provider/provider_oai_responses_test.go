package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	. "github.com/great-magician-01/callable/internal/model"
	. "github.com/great-magician-01/callable/internal/testutil"
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
	m := DecodeMap(t, body)

	if AsString(t, m["model"]) != "gpt-x" {
		t.Errorf("model = %v", m["model"])
	}
	if AsString(t, m["instructions"]) != "be nice" {
		t.Errorf("instructions = %v", m["instructions"])
	}
	if AsFloat(t, m["max_output_tokens"]) != 88 {
		t.Errorf("max_output_tokens = %v", m["max_output_tokens"])
	}
	if _, present := m["reasoning"]; present {
		t.Errorf("reasoning should be absent when thinking is off")
	}
	input := AsSlice(t, m["input"])
	item := AsMap(t, input[0])
	if AsString(t, item["role"]) != "user" {
		t.Errorf("input item = %v", item)
	}
	content := AsSlice(t, item["content"])
	if AsString(t, AsMap(t, content[0])["type"]) != "input_text" {
		t.Errorf("input content = %v", content[0])
	}
}

func TestResponsesBuildPayloadThinking(t *testing.T) {
	p := responsesProvider()
	body, _ := p.buildPayload(
		NewRequest(User("hi")).WithModel("gpt-x").WithThinking(Thinking{Effort: EffortHigh}).WithTemperature(0.7),
		false)
	m := DecodeMap(t, body)
	reasoning := AsMap(t, m["reasoning"])
	if AsString(t, reasoning["effort"]) != "high" {
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
	tools := AsSlice(t, DecodeMap(t, body)["tools"])
	tw := AsMap(t, tools[0])
	// Responses tools are flattened, not nested under "function".
	if AsString(t, tw["type"]) != "function" || AsString(t, tw["name"]) != "get_weather" {
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
	item := AsMap(t, items[0])
	content := AsSlice(t, item["content"])
	img := AsMap(t, content[1])
	if AsString(t, img["type"]) != "input_image" {
		t.Fatalf("image content = %v", img)
	}
	if !strings.HasPrefix(AsString(t, img["image_url"]), "data:image/png;base64,") {
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
	if got := AsString(t, AsMap(t, items[0])["type"]); got != "reasoning" {
		t.Errorf("input[0] = %v, want raw reasoning item", items[0])
	}
	if got := AsString(t, AsMap(t, items[1])["type"]); got != "function_call" {
		t.Errorf("input[1] = %v, want raw function_call item", items[1])
	}
	// toolMsg -> function_call_output (must precede the next user content)
	if got := AsString(t, AsMap(t, items[2])["type"]); got != "function_call_output" {
		t.Errorf("input[2] = %v, want function_call_output", items[2])
	}
	if got := AsString(t, AsMap(t, items[2])["call_id"]); got != "call_1" {
		t.Errorf("call_id = %v", items[2])
	}
	// plain -> reconstructed assistant message item
	third := AsMap(t, items[3])
	if AsString(t, third["type"]) != "message" || AsString(t, third["role"]) != "assistant" {
		t.Errorf("input[3] = %v, want reconstructed assistant message", items[3])
	}
	// user
	if AsString(t, AsMap(t, items[4])["role"]) != "user" {
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
	resp, err := p.assemble(env, nil)
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
	if resp.Usage.ReasoningTokens != 4 || resp.Usage.ContextTokens != 10 {
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
	resp, err := p.assemble(env, nil)
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
	server := NewMockServer(t, []string{stream}, &bodies)
	p := NewOpenAIResponsesProvider("k", server.URL)

	var events []Event
	resp, err := p.Stream(context.Background(), NewRequest(User("hi")).WithModel("gpt-x"), func(ev Event) {
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
	if resp.Usage.InputTokens != 9 || resp.Usage.ReasoningTokens != 3 ||
		resp.Usage.ContextTokens != 9 {
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

func TestResponsesUnknownItemsPreserved(t *testing.T) {
	p := responsesProvider()
	env := &respEnvelope{
		Status: "completed",
		Output: []json.RawMessage{
			json.RawMessage(`{"type":"web_search_call","id":"ws_1","status":"completed"}`),
			json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}`),
		},
	}
	resp, err := p.assemble(env, map[string]json.RawMessage{"custom_field": json.RawMessage(`1`)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "done" {
		t.Errorf("text = %q", resp.Text)
	}
	// The unknown item stays visible as a RawPart with its original JSON.
	rp, ok := resp.Message.Parts[0].(RawPart)
	if !ok {
		t.Fatalf("part 0 type = %T", resp.Message.Parts[0])
	}
	if rp.BlockType != "web_search_call" || rp.Provider != "openai-responses" {
		t.Errorf("raw part = %+v", rp)
	}
	if AsString(t, DecodeMap(t, rp.Raw)["id"]) != "ws_1" {
		t.Errorf("raw = %s", rp.Raw)
	}
	// ... and it still round-trips via the provider payload replay.
	if extra := resp.Message.ProviderExtra("openai-responses"); !strings.Contains(string(extra), "web_search_call") {
		t.Errorf("provider extra = %s", extra)
	}
	if string(resp.Extra["custom_field"]) != `1` {
		t.Errorf("custom_field = %s", resp.Extra["custom_field"])
	}
}

func TestResponsesCreatePreservesUnknownFields(t *testing.T) {
	body := `{"id":"resp_1","status":"completed","future_field":{"x":1},` +
		`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],` +
		`"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`
	server := NewMockJSONServer(t, []string{body}, nil)
	p := NewOpenAIResponsesProvider("k", server.URL)
	resp, err := p.Create(context.Background(), NewRequest(User("hi")).WithModel("gpt-x"))
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Extra["future_field"]) != `{"x":1}` {
		t.Errorf("future_field = %s", resp.Extra["future_field"])
	}
	if string(resp.Extra["id"]) != `"resp_1"` {
		t.Errorf("id = %s", resp.Extra["id"])
	}
	if _, ok := resp.Extra["output"]; ok {
		t.Error("known field output must not land in Extra")
	}
	if string(resp.Usage.Extra["total_tokens"]) != `4` {
		t.Errorf("total_tokens = %s", resp.Usage.Extra["total_tokens"])
	}
}

func TestResponsesRawPartReplay(t *testing.T) {
	p := responsesProvider()
	raw := json.RawMessage(`{"type":"custom_item","id":"ci_1","payload":{"a":1}}`)
	_, input, err := p.buildInput([]Message{
		User("hi"),
		Assistant(RawPart{Provider: "openai-responses", BlockType: "custom_item", Raw: raw}),
	})
	if err != nil {
		t.Fatal(err)
	}
	items := itemsToMaps(t, input)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if AsString(t, AsMap(t, items[1])["type"]) != "custom_item" {
		t.Errorf("replayed item = %v", items[1])
	}
	if AsFloat(t, AsMap(t, AsMap(t, items[1])["payload"])["a"]) != 1 {
		t.Errorf("replayed payload = %v", items[1])
	}

	// A RawPart from another provider's wire format is not replayed.
	_, input, err = p.buildInput([]Message{
		User("hi"),
		Assistant(RawPart{Provider: "anthropic", BlockType: "x", Raw: json.RawMessage(`{"type":"x"}`)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if items := itemsToMaps(t, input); len(items) != 1 {
		t.Fatalf("items = %d, want 1 (foreign RawPart skipped)", len(items))
	}
}
