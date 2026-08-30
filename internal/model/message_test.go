package model

import (
	"encoding/json"
	"testing"
)

func TestUserMessageConstruction(t *testing.T) {
	m := User("hello", Image("/tmp/a.png"))
	if m.Role != RoleUser {
		t.Fatalf("role = %q, want user", m.Role)
	}
	if len(m.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(m.Parts))
	}
	if _, ok := m.Parts[0].(TextPart); !ok {
		t.Fatalf("part 0 type = %T, want TextPart", m.Parts[0])
	}
	if _, ok := m.Parts[1].(ImagePart); !ok {
		t.Fatalf("part 1 type = %T, want ImagePart", m.Parts[1])
	}

	// []Part spread
	m = User("a", []Part{Text("b"), Text("c")})
	if len(m.Parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(m.Parts))
	}

	// unsupported type panics
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unsupported part type")
		}
	}()
	User(42)
}

func TestImageURLConstructor(t *testing.T) {
	p := Image("https://example.com/a.png")
	if p.URL == "" || p.Path != "" {
		t.Fatalf("Image() with URL should set URL only, got %+v", p)
	}
	p = Image("/tmp/a.png")
	if p.Path == "" || p.URL != "" {
		t.Fatalf("Image() with path should set Path only, got %+v", p)
	}
}

func TestMessageAccessors(t *testing.T) {
	m := Message{Role: RoleAssistant, Parts: []Part{
		ThinkingPart{Text: "hmm"},
		TextPart{Text: "answer"},
		ToolCallPart{ID: "1", Name: "t", Arguments: "{}"},
		ToolCallPart{ID: "2", Name: "t", Arguments: "{}"},
	}}
	if got := m.Text(); got != "answer" {
		t.Errorf("Text() = %q", got)
	}
	if got := m.Thinking(); got != "hmm" {
		t.Errorf("Thinking() = %q", got)
	}
	if len(m.ToolCalls()) != 2 {
		t.Errorf("ToolCalls() = %d, want 2", len(m.ToolCalls()))
	}
}

func TestMessageJSONRoundTrip(t *testing.T) {
	m := Message{Role: RoleAssistant, Parts: []Part{
		ThinkingPart{Text: "think", Signature: "sig"},
		TextPart{Text: "hi"},
		ImagePart{URL: "https://x/y.png"},
		ToolCallPart{ID: "c1", Name: "tool", Arguments: `{"a":1}`},
		ToolResultPart{ToolCallID: "c1", Name: "tool", Content: "ok", IsError: true},
	}}
	m.SetProviderExtra("openai-responses", json.RawMessage(`[{"type":"reasoning"}]`))

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Role != RoleAssistant {
		t.Errorf("role = %q", back.Role)
	}
	if len(back.Parts) != 5 {
		t.Fatalf("parts = %d, want 5", len(back.Parts))
	}
	wantTypes := []string{"thinking", "text", "image", "tool_call", "tool_result"}
	for i, want := range wantTypes {
		if got := back.Parts[i].partType(); got != want {
			t.Errorf("part %d type = %q, want %q", i, got, want)
		}
	}
	if tp, _ := back.Parts[0].(ThinkingPart); tp.Signature != "sig" {
		t.Errorf("signature lost: %+v", tp)
	}
	if extra := back.ProviderExtra("openai-responses"); string(extra) != `[{"type":"reasoning"}]` {
		t.Errorf("provider extra = %s", extra)
	}
}

func TestMessageJSONRoundTripRawPartAndExtra(t *testing.T) {
	m := Message{
		Role: RoleAssistant,
		Parts: []Part{
			TextPart{Text: "hi"},
			RawPart{
				Provider:  "anthropic",
				BlockType: "server_tool_use",
				Raw:       json.RawMessage(`{"type":"server_tool_use","id":"srv_1","name":"web_search"}`),
			},
		},
		Extra: map[string]json.RawMessage{
			"annotations": json.RawMessage(`[{"url":"https://x"}]`),
		},
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(back.Parts))
	}
	rp, ok := back.Parts[1].(RawPart)
	if !ok {
		t.Fatalf("part 1 type = %T, want RawPart", back.Parts[1])
	}
	if rp.Provider != "anthropic" || rp.BlockType != "server_tool_use" {
		t.Errorf("raw part = %+v", rp)
	}
	if string(rp.Raw) != `{"type":"server_tool_use","id":"srv_1","name":"web_search"}` {
		t.Errorf("raw = %s", rp.Raw)
	}
	if string(back.Extra["annotations"]) != `[{"url":"https://x"}]` {
		t.Errorf("extra = %v", back.Extra)
	}

	// The discriminator survives a standalone UnmarshalPart too.
	p, err := UnmarshalPart(json.RawMessage(`{"type":"raw","provider":"anthropic","block_type":"x","raw":{"type":"x"}}`))
	if err != nil {
		t.Fatalf("UnmarshalPart: %v", err)
	}
	if rp, ok := p.(RawPart); !ok || rp.BlockType != "x" {
		t.Errorf("part = %+v", p)
	}
}
