package core

import (
	"strings"
	"testing"
)

func TestChatResponseFormatPayload(t *testing.T) {
	p := NewOpenAIProvider("k", "https://api.example.com")
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
	}
	req := NewRequest(User("hi")).WithResponseFormat(JSONSchema("recipe", schema, true))
	m := decodeMap(t, must(p.buildPayload(req, false)))

	rf := asMap(t, m["response_format"])
	if got := asString(t, rf["type"]); got != "json_schema" {
		t.Errorf("response_format.type = %q", got)
	}
	js := asMap(t, rf["json_schema"])
	if got := asString(t, js["name"]); got != "recipe" {
		t.Errorf("json_schema.name = %q", got)
	}
	if got := js["strict"]; got != true {
		t.Errorf("json_schema.strict = %v", got)
	}
	if got := asString(t, asMap(t, js["schema"])["type"]); got != "object" {
		t.Errorf("json_schema.schema.type = %q", got)
	}
}

func TestChatJSONModePayload(t *testing.T) {
	p := NewOpenAIProvider("k", "https://api.example.com")
	req := NewRequest(User("hi")).WithResponseFormat(JSONMode())
	m := decodeMap(t, must(p.buildPayload(req, false)))
	if got := asString(t, asMap(t, m["response_format"])["type"]); got != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", got)
	}
	// No format: the field must be absent entirely.
	m2 := decodeMap(t, must(p.buildPayload(NewRequest(User("hi")), false)))
	if _, ok := m2["response_format"]; ok {
		t.Errorf("response_format must be omitted when unset: %v", m2["response_format"])
	}
}

func TestChatSamplingParamsPayload(t *testing.T) {
	p := NewOpenAIProvider("k", "https://api.example.com")
	req := NewRequest(User("hi")).WithTopP(0.5).WithStopSequences("END", "STOP")
	m := decodeMap(t, must(p.buildPayload(req, false)))
	if got := asFloat(t, m["top_p"]); got != 0.5 {
		t.Errorf("top_p = %v", got)
	}
	stop := asSlice(t, m["stop"])
	if len(stop) != 2 || stop[0] != "END" || stop[1] != "STOP" {
		t.Errorf("stop = %v", stop)
	}

	// Thinking models reject sampling parameters; stop sequences still go out.
	thinking := NewRequest(User("hi")).WithTopP(0.5).WithTemperature(0.7).
		WithStopSequences("END").WithThinking(Thinking{Effort: EffortHigh})
	m2 := decodeMap(t, must(p.buildPayload(thinking, false)))
	if _, ok := m2["top_p"]; ok {
		t.Errorf("top_p must be omitted when thinking is on: %v", m2["top_p"])
	}
	if _, ok := m2["temperature"]; ok {
		t.Errorf("temperature must be omitted when thinking is on: %v", m2["temperature"])
	}
	if got := asSlice(t, m2["stop"]); len(got) != 1 || got[0] != "END" {
		t.Errorf("stop = %v", got)
	}
}

func TestResponsesFormatPayload(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "https://api.example.com")
	schema := map[string]any{"type": "object"}
	req := NewRequest(User("hi")).
		WithResponseFormat(JSONSchema("", schema, true)). // blank name defaults to "output"
		WithTopP(0.9)
	m := decodeMap(t, must(p.buildPayload(req, false)))

	format := asMap(t, asMap(t, m["text"])["format"])
	if got := asString(t, format["type"]); got != "json_schema" {
		t.Errorf("text.format.type = %q", got)
	}
	if got := asString(t, format["name"]); got != "output" {
		t.Errorf("text.format.name = %q, want defaulted output", got)
	}
	if got := format["strict"]; got != true {
		t.Errorf("text.format.strict = %v", got)
	}
	if got := asFloat(t, m["top_p"]); got != 0.9 {
		t.Errorf("top_p = %v", got)
	}

	m2 := decodeMap(t, must(p.buildPayload(NewRequest(User("hi")).WithResponseFormat(JSONMode()), false)))
	if got := asString(t, asMap(t, asMap(t, m2["text"])["format"])["type"]); got != "json_object" {
		t.Errorf("text.format.type = %q, want json_object", got)
	}
}

func TestAnthropicFormatPayload(t *testing.T) {
	p := NewAnthropicProvider("k", "https://api.example.com")
	schema := map[string]any{"type": "object"}
	req := NewRequest(User("hi")).
		WithResponseFormat(JSONSchema("recipe", schema, true)).
		WithTopP(0.4).
		WithStopSequences("END")
	m := decodeMap(t, must(p.buildPayload(req, false)))

	format := asMap(t, asMap(t, m["output_config"])["format"])
	if got := asString(t, format["type"]); got != "json_schema" {
		t.Errorf("output_config.format.type = %q", got)
	}
	if got := asString(t, asMap(t, format["schema"])["type"]); got != "object" {
		t.Errorf("output_config.format.schema.type = %q", got)
	}
	if got := asFloat(t, m["top_p"]); got != 0.4 {
		t.Errorf("top_p = %v", got)
	}
	if got := asSlice(t, m["stop_sequences"]); len(got) != 1 || got[0] != "END" {
		t.Errorf("stop_sequences = %v", got)
	}

	// Free-form JSON mode becomes a permissive object schema on Anthropic.
	m2 := decodeMap(t, must(p.buildPayload(NewRequest(User("hi")).WithResponseFormat(JSONMode()), false)))
	format2 := asMap(t, asMap(t, m2["output_config"])["format"])
	if got := asString(t, asMap(t, format2["schema"])["type"]); got != "object" {
		t.Errorf("json mode schema.type = %q, want object", got)
	}

	// Thinking mode omits sampling parameters but keeps stop sequences.
	thinking := NewRequest(User("hi")).WithTopP(0.4).WithStopSequences("END").
		WithThinking(Thinking{Effort: EffortHigh})
	m3 := decodeMap(t, must(p.buildPayload(thinking, false)))
	if _, ok := m3["top_p"]; ok {
		t.Errorf("top_p must be omitted when thinking is on: %v", m3["top_p"])
	}
	if got := asSlice(t, m3["stop_sequences"]); len(got) != 1 {
		t.Errorf("stop_sequences = %v", got)
	}
}

func TestJSONSchemaForReflectsStruct(t *testing.T) {
	type recipe struct {
		Name  string   `json:"name" jsonschema:"description=Dish name"`
		Steps []string `json:"steps,omitempty"`
	}
	f := JSONSchemaFor[recipe]("recipe", true)
	if f.Name != "recipe" || !f.Strict {
		t.Errorf("format = %+v", f)
	}
	if got := asString(t, f.Schema["type"]); got != "object" {
		t.Errorf("schema.type = %q", got)
	}
	props := asMap(t, f.Schema["properties"])
	if _, ok := props["name"]; !ok {
		t.Errorf("schema missing name property: %v", f.Schema)
	}
	if _, ok := props["steps"]; !ok {
		t.Errorf("schema missing steps property: %v", f.Schema)
	}
}

// TestChatDeepSeekSchemaDowngrade verifies that on DeepSeek endpoints (which
// reject json_schema) a schema-mode request is downgraded to json_object with
// the schema spelled out in the last user message.
func TestChatDeepSeekSchemaDowngrade(t *testing.T) {
	p := NewOpenAIProvider("k", DeepSeekURL) // CompatDeepSeek auto-detected
	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}
	req := NewRequest(User("给我一个菜谱"), Assistant("好的"), User("要 JSON")).
		WithResponseFormat(JSONSchema("recipe", schema, true))
	m := decodeMap(t, must(p.buildPayload(req, false)))

	rf := asMap(t, m["response_format"])
	if got := asString(t, rf["type"]); got != "json_object" {
		t.Errorf("response_format.type = %q, want downgraded json_object", got)
	}
	msgs := asSlice(t, m["messages"])
	last := asMap(t, msgs[len(msgs)-1])
	content := asString(t, last["content"])
	if !strings.Contains(content, "要 JSON") || !strings.Contains(content, "JSON Schema") || !strings.Contains(content, `"name"`) {
		t.Errorf("schema hint not injected into last user message: %q", content)
	}
	// The caller's messages must not be mutated.
	if strings.Contains(req.Messages[2].Text(), "JSON Schema") {
		t.Error("caller message was mutated")
	}

	// A non-DeepSeek endpoint keeps json_schema.
	p2 := NewOpenAIProvider("k", "https://api.example.com")
	m2 := decodeMap(t, must(p2.buildPayload(req, false)))
	if got := asString(t, asMap(t, m2["response_format"])["type"]); got != "json_schema" {
		t.Errorf("response_format.type = %q, want json_schema", got)
	}

	// GLM/Z.AI document only text/json_object; the same downgrade applies.
	p3 := NewOpenAIProvider("k", GLMURL) // CompatGLM auto-detected
	m3 := decodeMap(t, must(p3.buildPayload(req, false)))
	if got := asString(t, asMap(t, m3["response_format"])["type"]); got != "json_object" {
		t.Errorf("GLM response_format.type = %q, want downgraded json_object", got)
	}
	msgs3 := asSlice(t, m3["messages"])
	if got := asString(t, asMap(t, msgs3[len(msgs3)-1])["content"]); !strings.Contains(got, "JSON Schema") {
		t.Errorf("GLM schema hint not injected: %q", got)
	}

	// Qwen, Ark and Kimi support json_schema natively: no downgrade.
	for _, u := range []string{QwenURL, ArkURL, KimiURL} {
		pm := decodeMap(t, must(NewOpenAIProvider("k", u).buildPayload(req, false)))
		if got := asString(t, asMap(t, pm["response_format"])["type"]); got != "json_schema" {
			t.Errorf("%s: response_format.type = %q, want json_schema (no downgrade)", u, got)
		}
	}
}

func TestResponseDecodeJSON(t *testing.T) {
	resp := &Response{Text: `{"name":"pancake","steps":["mix","fry"]}`}
	var r struct {
		Name  string   `json:"name"`
		Steps []string `json:"steps"`
	}
	if err := resp.DecodeJSON(&r); err != nil {
		t.Fatal(err)
	}
	if r.Name != "pancake" || len(r.Steps) != 2 {
		t.Errorf("decoded = %+v", r)
	}
	if err := (&Response{Text: "not json"}).DecodeJSON(&r); err == nil {
		t.Error("expected decode error for non-JSON text")
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
