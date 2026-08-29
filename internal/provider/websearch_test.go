package provider

import (
	"context"
	"testing"

	. "github.com/great-magician-01/callable/internal/model"
	. "github.com/great-magician-01/callable/internal/testutil"
)

// kimiWebSearchStub mirrors the agent-side $web_search echo stub
// (internal/core); the wire rendering only looks at the tool name.
func kimiWebSearchStub() Tool {
	return NewRawTool(kimiWebSearchToolName,
		"Search the web for current information.",
		`{"type":"object","properties":{"query":{"type":"string","description":"The search query"}}}`,
		func(_ context.Context, rawArgs string) (any, error) {
			return rawArgs, nil
		})
}

func TestWebSearchSupportDetection(t *testing.T) {
	chat := []struct {
		baseURL string
		want    WebSearchSupport
	}{
		{KimiURL, WebSearchEcho},
		{"https://api.moonshot.ai/v1", WebSearchEcho},
		{GLMURL, WebSearchServer},
		{ZAIURL, WebSearchServer},
		{QwenURL, WebSearchServer},
		{OpenAIURL, WebSearchNone},
		{DeepSeekURL, WebSearchNone},
		{ArkURL, WebSearchNone},
		{"http://127.0.0.1:8080/v1", WebSearchNone},
	}
	for _, c := range chat {
		if got := NewOpenAIProvider("k", c.baseURL).supportsWebSearch(); got != c.want {
			t.Errorf("chat %s: support = %v, want %v", c.baseURL, got, c.want)
		}
	}

	if got := NewOpenAIResponsesProvider("k", OpenAIURL).supportsWebSearch(); got != WebSearchServer {
		t.Errorf("responses official: support = %v, want server", got)
	}
	if got := NewOpenAIResponsesProvider("k", "http://127.0.0.1:8080").supportsWebSearch(); got != WebSearchNone {
		t.Errorf("responses third-party: support = %v, want none", got)
	}

	if got := NewAnthropicProvider("k", AnthropicURL).supportsWebSearch(); got != WebSearchServer {
		t.Errorf("anthropic official: support = %v, want server", got)
	}
	for _, baseURL := range []string{DeepSeekAnthropicURL, GLMAnthropicURL, ZAIAnthropicURL, KimiAnthropicURL} {
		if got := NewAnthropicProvider("k", baseURL).supportsWebSearch(); got != WebSearchNone {
			t.Errorf("anthropic %s: support = %v, want none", baseURL, got)
		}
	}
}

func TestChatWebSearchGolden(t *testing.T) {
	req := func() *Request {
		return NewRequest(User("hi")).WithModel("m")
	}

	t.Run("GLM tool entry", func(t *testing.T) {
		r := req()
		r.WebSearch = true
		body, err := NewOpenAIProvider("k", GLMURL).buildPayload(r, false)
		if err != nil {
			t.Fatal(err)
		}
		tools := AsSlice(t, DecodeMap(t, body)["tools"])
		if len(tools) != 1 {
			t.Fatalf("tools = %v", tools)
		}
		entry := AsMap(t, tools[0])
		if got := AsString(t, entry["type"]); got != "web_search" {
			t.Fatalf("type = %q", got)
		}
		ws := AsMap(t, entry["web_search"])
		if ws["enable"] != true || ws["search_result"] != true {
			t.Fatalf("web_search = %v", ws)
		}
	})

	t.Run("Qwen enable_search", func(t *testing.T) {
		r := req()
		r.WebSearch = true
		body, err := NewOpenAIProvider("k", QwenURL).buildPayload(r, false)
		if err != nil {
			t.Fatal(err)
		}
		m := DecodeMap(t, body)
		if m["enable_search"] != true {
			t.Fatalf("enable_search = %v (body %s)", m["enable_search"], body)
		}
		if _, ok := m["tools"]; ok {
			t.Fatalf("unexpected tools: %s", body)
		}
	})

	t.Run("Kimi builtin_function", func(t *testing.T) {
		r := req().WithTools(kimiWebSearchStub())
		r.WebSearch = true
		body, err := NewOpenAIProvider("k", KimiURL).buildPayload(r, false)
		if err != nil {
			t.Fatal(err)
		}
		tools := AsSlice(t, DecodeMap(t, body)["tools"])
		if len(tools) != 1 {
			t.Fatalf("tools = %v", tools)
		}
		entry := AsMap(t, tools[0])
		if got := AsString(t, entry["type"]); got != "builtin_function" {
			t.Fatalf("type = %q", got)
		}
		fn := AsMap(t, entry["function"])
		if got := AsString(t, fn["name"]); got != kimiWebSearchToolName {
			t.Fatalf("function.name = %q", got)
		}
		if _, ok := fn["parameters"]; ok {
			t.Fatalf("builtin_function must not carry parameters: %v", fn)
		}
	})

	t.Run("unsupported endpoints stay clean", func(t *testing.T) {
		for _, baseURL := range []string{OpenAIURL, DeepSeekURL, ArkURL} {
			r := req()
			r.WebSearch = true
			body, err := NewOpenAIProvider("k", baseURL).buildPayload(r, false)
			if err != nil {
				t.Fatal(err)
			}
			m := DecodeMap(t, body)
			if _, ok := m["tools"]; ok {
				t.Errorf("%s: unexpected tools: %s", baseURL, body)
			}
			if _, ok := m["enable_search"]; ok {
				t.Errorf("%s: unexpected enable_search: %s", baseURL, body)
			}
		}
	})
}

func TestAnthropicWebSearchGolden(t *testing.T) {
	req := NewRequest(User("hi")).WithModel("m")
	req.WebSearch = true

	body, err := NewAnthropicProvider("k", AnthropicURL).buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	tools := AsSlice(t, DecodeMap(t, body)["tools"])
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	entry := AsMap(t, tools[0])
	if got := AsString(t, entry["type"]); got != "web_search_20250305" {
		t.Fatalf("type = %q", got)
	}
	if got := AsString(t, entry["name"]); got != defaultWebSearchToolName {
		t.Fatalf("name = %q", got)
	}

	// Third-party Anthropic-compatible endpoints: no injection.
	body, err = NewAnthropicProvider("k", DeepSeekAnthropicURL).buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := DecodeMap(t, body)["tools"]; ok {
		t.Fatalf("unexpected tools: %s", body)
	}
}

func TestResponsesWebSearchGolden(t *testing.T) {
	req := NewRequest(User("hi")).WithModel("m")
	req.WebSearch = true

	body, err := NewOpenAIResponsesProvider("k", OpenAIURL).buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	tools := AsSlice(t, DecodeMap(t, body)["tools"])
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	if got := AsString(t, AsMap(t, tools[0])["type"]); got != "web_search" {
		t.Fatalf("type = %q", got)
	}

	body, err = NewOpenAIResponsesProvider("k", "http://127.0.0.1:8080").buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := DecodeMap(t, body)["tools"]; ok {
		t.Fatalf("unexpected tools: %s", body)
	}
}
