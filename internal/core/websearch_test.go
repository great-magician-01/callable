package core

import (
	"context"
	"encoding/json"
	. "github.com/great-magician-01/callable/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Web-search capability detection lives in internal/provider, whose
// unexported capability seam cannot be faked from this package. The
// agent-wiring tests below therefore force capability through real detection
// paths: WithCompat(CompatQwen) marks any base URL as server-side-search
// capable, and a Moonshot base URL (combined with a host-rewriting client
// that points the requests back at the mock server) yields Kimi's echo
// protocol.

// hostRewriteClient points requests at the mock server while the provider
// keeps its original base URL, so endpoint detection still applies to
// httptest URLs.
func hostRewriteClient(srv *httptest.Server) *http.Client {
	u, _ := url.Parse(srv.URL)
	return &http.Client{Transport: hostRewriteTransport{scheme: u.Scheme, host: u.Host}}
}

type hostRewriteTransport struct{ scheme, host string }

func (t hostRewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = t.scheme
	r2.URL.Host = t.host
	return http.DefaultTransport.RoundTrip(r2)
}

func TestAgentWebSearchDefaultBuiltin(t *testing.T) {
	var bodies []string
	finalTurn := ChatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
	server := NewMockServer(t, []string{finalTurn}, &bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL, WithCompat(CompatQwen)), WithModel("m"))
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
	// The Qwen dialect renders server-side search as a top-level
	// enable_search switch, never as a web_search tool entry.
	if strings.Contains(bodies[0], "web_search") {
		t.Fatalf("chat-completions wire must not render a web_search tool entry: %s", bodies[0])
	}
}

func TestAgentWebSearchDisabled(t *testing.T) {
	var bodies []string
	finalTurn := ChatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
	server := NewMockServer(t, []string{finalTurn}, &bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL, WithCompat(CompatQwen)), WithModel("m"))
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
	finalTurn := ChatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
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
	toolCallTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", DefaultWebSearchToolName, `{"query":"go 1.25"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(`{"choices":[{"delta":{"content":"Go 1.25 is out"}}]}`)
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
	finalTurn := ChatSSE(`{"choices":[{"delta":{"content":"done"}}]}`)
	server := NewMockServer(t, []string{finalTurn}, nil)
	client := NewClient(NewOpenAIProvider("k", server.URL, WithCompat(CompatQwen)), WithModel("m"))
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
	toolCallTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", kimiWebSearchToolName, `{"query":"latest news"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(`{"choices":[{"delta":{"content":"here is the news"}}]}`)
	server := NewMockServer(t, []string{toolCallTurn, finalTurn}, &bodies)
	client := NewClient(
		NewOpenAIProvider("k", KimiURL, WithHTTPClient(hostRewriteClient(server))),
		WithModel("m"))
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
	msgs := AsSlice(t, DecodeMap(t, []byte(bodies[1]))["messages"])
	var toolContent string
	for _, m := range msgs {
		mm := AsMap(t, m)
		if mm["role"] == "tool" {
			toolContent = AsString(t, mm["content"])
		}
	}
	if toolContent != `{"query":"latest news"}` {
		t.Fatalf("tool result = %q, want arguments echoed verbatim", toolContent)
	}
}
