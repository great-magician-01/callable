package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serverSearchProvider forces webSearchServer support over any base URL, so
// agent-level wiring can be tested against httptest mock servers (whose URLs
// match none of the built-in endpoints).
type serverSearchProvider struct{ *OpenAIProvider }

func (serverSearchProvider) supportsWebSearch() webSearchSupport { return webSearchServer }

// echoSearchProvider forces Kimi-style echo support for the same reason.
type echoSearchProvider struct{ *OpenAIProvider }

func (echoSearchProvider) supportsWebSearch() webSearchSupport { return webSearchEcho }

func TestWebSearchSupportDetection(t *testing.T) {
	chat := []struct {
		baseURL string
		want    webSearchSupport
	}{
		{KimiURL, webSearchEcho},
		{"https://api.moonshot.ai/v1", webSearchEcho},
		{GLMURL, webSearchServer},
		{ZAIURL, webSearchServer},
		{QwenURL, webSearchServer},
		{OpenAIURL, webSearchNone},
		{DeepSeekURL, webSearchNone},
		{ArkURL, webSearchNone},
		{"http://127.0.0.1:8080/v1", webSearchNone},
	}
	for _, c := range chat {
		if got := NewOpenAIProvider("k", c.baseURL).supportsWebSearch(); got != c.want {
			t.Errorf("chat %s: support = %v, want %v", c.baseURL, got, c.want)
		}
	}

	if got := NewOpenAIResponsesProvider("k", OpenAIURL).supportsWebSearch(); got != webSearchServer {
		t.Errorf("responses official: support = %v, want server", got)
	}
	if got := NewOpenAIResponsesProvider("k", "http://127.0.0.1:8080").supportsWebSearch(); got != webSearchNone {
		t.Errorf("responses third-party: support = %v, want none", got)
	}

	if got := NewAnthropicProvider("k", AnthropicURL).supportsWebSearch(); got != webSearchServer {
		t.Errorf("anthropic official: support = %v, want server", got)
	}
	for _, baseURL := range []string{DeepSeekAnthropicURL, GLMAnthropicURL, ZAIAnthropicURL, KimiAnthropicURL} {
		if got := NewAnthropicProvider("k", baseURL).supportsWebSearch(); got != webSearchNone {
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
		tools := asSlice(t, decodeMap(t, body)["tools"])
		if len(tools) != 1 {
			t.Fatalf("tools = %v", tools)
		}
		entry := asMap(t, tools[0])
		if got := asString(t, entry["type"]); got != "web_search" {
			t.Fatalf("type = %q", got)
		}
		ws := asMap(t, entry["web_search"])
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
		m := decodeMap(t, body)
		if m["enable_search"] != true {
			t.Fatalf("enable_search = %v (body %s)", m["enable_search"], body)
		}
		if _, ok := m["tools"]; ok {
			t.Fatalf("unexpected tools: %s", body)
		}
	})

	t.Run("Kimi builtin_function", func(t *testing.T) {
		r := req().WithTools(newKimiWebSearchTool())
		r.WebSearch = true
		body, err := NewOpenAIProvider("k", KimiURL).buildPayload(r, false)
		if err != nil {
			t.Fatal(err)
		}
		tools := asSlice(t, decodeMap(t, body)["tools"])
		if len(tools) != 1 {
			t.Fatalf("tools = %v", tools)
		}
		entry := asMap(t, tools[0])
		if got := asString(t, entry["type"]); got != "builtin_function" {
			t.Fatalf("type = %q", got)
		}
		fn := asMap(t, entry["function"])
		if got := asString(t, fn["name"]); got != kimiWebSearchToolName {
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
			m := decodeMap(t, body)
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
	tools := asSlice(t, decodeMap(t, body)["tools"])
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	entry := asMap(t, tools[0])
	if got := asString(t, entry["type"]); got != "web_search_20250305" {
		t.Fatalf("type = %q", got)
	}
	if got := asString(t, entry["name"]); got != DefaultWebSearchToolName {
		t.Fatalf("name = %q", got)
	}

	// Third-party Anthropic-compatible endpoints: no injection.
	body, err = NewAnthropicProvider("k", DeepSeekAnthropicURL).buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeMap(t, body)["tools"]; ok {
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
	tools := asSlice(t, decodeMap(t, body)["tools"])
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	if got := asString(t, asMap(t, tools[0])["type"]); got != "web_search" {
		t.Fatalf("type = %q", got)
	}

	body, err = NewOpenAIResponsesProvider("k", "http://127.0.0.1:8080").buildPayload(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeMap(t, body)["tools"]; ok {
		t.Fatalf("unexpected tools: %s", body)
	}
}

func TestAgentWebSearchDefaultBuiltin(t *testing.T) {
	var bodies []string
	finalTurn := chatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
	server := newMockServer(t, []string{finalTurn}, &bodies)
	client := NewClient(serverSearchProvider{NewOpenAIProvider("k", server.URL)}, WithModel("m"))
	agent := NewAgent(client) // default: auto -> built-in wins, no Tavily key

	if !agent.webSearchBuiltin {
		t.Fatal("expected webSearchBuiltin to be set")
	}
	if _, ok := agent.tools.get(DefaultWebSearchToolName); ok {
		t.Fatal("Tavily fallback tool must not be registered for built-in providers")
	}
	if _, err := agent.RunStream(context.Background(), noopEvents, User("hi")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bodies[0], "web_search") {
		t.Fatalf("chat-completions wire must not render server-side search for unknown dialects: %s", bodies[0])
	}
}

func TestAgentWebSearchDisabled(t *testing.T) {
	var bodies []string
	finalTurn := chatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
	server := newMockServer(t, []string{finalTurn}, &bodies)
	client := NewClient(serverSearchProvider{NewOpenAIProvider("k", server.URL)}, WithModel("m"))
	agent := NewAgent(client, WithWebSearch(false), WithTavilyAPIKey("tvly-test"))

	if agent.webSearchBuiltin {
		t.Fatal("webSearchBuiltin must stay unset when disabled")
	}
	if _, ok := agent.tools.get(DefaultWebSearchToolName); ok {
		t.Fatal("no web-search tool may be registered when disabled")
	}
	if _, err := agent.RunStream(context.Background(), noopEvents, User("hi")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bodies[0], "web_search") {
		t.Fatalf("disabled web search leaked into request: %s", bodies[0])
	}
}

func TestAgentWebSearchNoCapability(t *testing.T) {
	// No built-in support, no Tavily key: nothing is exposed.
	var bodies []string
	finalTurn := chatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
	agent := chatAgentFixture(t, []string{finalTurn}, &bodies)

	if agent.webSearchBuiltin {
		t.Fatal("webSearchBuiltin must stay unset without capability")
	}
	if _, ok := agent.tools.get(DefaultWebSearchToolName); ok {
		t.Fatal("no web-search tool may be registered without capability or key")
	}
	if _, err := agent.RunStream(context.Background(), noopEvents, User("hi")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bodies[0], "tools") {
		t.Fatalf("unexpected tools in request: %s", bodies[0])
	}
}

func TestAgentWebSearchTavilyFallback(t *testing.T) {
	var tavilyAuth, tavilyBody string
	tavilySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tavilyAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		tavilyBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"Go 1.25 is out","results":[{"title":"Go 1.25","url":"https://go.dev/blog","content":"release notes"}],"response_time":"0.1"}`))
	}))
	t.Cleanup(tavilySrv.Close)
	oldURL := tavilySearchURL
	tavilySearchURL = tavilySrv.URL
	t.Cleanup(func() { tavilySearchURL = oldURL })

	var bodies []string
	toolCallTurn := chatSSE(
		chatToolCallChunk(0, "call_1", DefaultWebSearchToolName, `{"query":"go 1.25"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := chatSSE(`{"choices":[{"delta":{"content":"Go 1.25 is out"}}]}`)
	agent := chatAgentFixture(t, []string{toolCallTurn, finalTurn}, &bodies,
		WithTavilyAPIKey("tvly-test"))

	result, err := agent.RunStream(context.Background(), noopEvents, User("what is new in go?"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "Go 1.25 is out" {
		t.Fatalf("final text = %q", result.FinalText)
	}

	// The fallback tool is advertised as a normal function tool.
	if !strings.Contains(bodies[0], `"name":"web_search"`) {
		t.Fatalf("web_search tool missing from request: %s", bodies[0])
	}
	if tavilyAuth != "Bearer tvly-test" {
		t.Fatalf("tavily auth = %q", tavilyAuth)
	}
	var tavilyReq map[string]any
	if err := json.Unmarshal([]byte(tavilyBody), &tavilyReq); err != nil {
		t.Fatalf("decode tavily body: %v", err)
	}
	if tavilyReq["query"] != "go 1.25" || tavilyReq["max_results"] != float64(5) {
		t.Fatalf("tavily body = %s", tavilyBody)
	}
	// The formatted result is fed back to the model.
	if !strings.Contains(bodies[1], "https://go.dev/blog") || !strings.Contains(bodies[1], "Go 1.25 is out") {
		t.Fatalf("tool result not fed back: %s", bodies[1])
	}
}

func TestAgentWebSearchBuiltinPreferredOverTavily(t *testing.T) {
	finalTurn := chatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
	server := newMockServer(t, []string{finalTurn}, nil)
	client := NewClient(serverSearchProvider{NewOpenAIProvider("k", server.URL)}, WithModel("m"))
	agent := NewAgent(client, WithTavilyAPIKey("tvly-test"))

	if !agent.webSearchBuiltin {
		t.Fatal("expected built-in search to win over the Tavily fallback")
	}
	if _, ok := agent.tools.get(DefaultWebSearchToolName); ok {
		t.Fatal("Tavily fallback tool must not be registered when built-in search exists")
	}
}

func TestAgentWebSearchKimiEcho(t *testing.T) {
	var bodies []string
	toolCallTurn := chatSSE(
		chatToolCallChunk(0, "call_1", kimiWebSearchToolName, `{"query":"latest news"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := chatSSE(`{"choices":[{"delta":{"content":"here is the news"}}]}`)
	server := newMockServer(t, []string{toolCallTurn, finalTurn}, &bodies)
	client := NewClient(echoSearchProvider{NewOpenAIProvider("k", server.URL)}, WithModel("m"))
	agent := NewAgent(client)

	if !agent.webSearchBuiltin {
		t.Fatal("expected webSearchBuiltin to be set for the echo protocol")
	}
	if _, ok := agent.tools.get(kimiWebSearchToolName); !ok {
		t.Fatalf("%s stub not registered", kimiWebSearchToolName)
	}

	result, err := agent.RunStream(context.Background(), noopEvents, User("news?"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "here is the news" {
		t.Fatalf("final text = %q", result.FinalText)
	}
	// Kimi protocol: the tool result echoes the call arguments verbatim.
	msgs := asSlice(t, decodeMap(t, []byte(bodies[1]))["messages"])
	var toolContent string
	for _, m := range msgs {
		mm := asMap(t, m)
		if mm["role"] == "tool" {
			toolContent = asString(t, mm["content"])
		}
	}
	if toolContent != `{"query":"latest news"}` {
		t.Fatalf("tool result = %q, want arguments echoed verbatim", toolContent)
	}
}
