// Package testutil provides test helpers shared by tests across internal
// packages: JSON decoding/assertion helpers, mock HTTP servers, and canned
// chat-completions response bodies. It must not import any internal package,
// so that white-box tests in those packages can use it without import cycles.
package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// DecodeMap unmarshals a JSON object, failing the test on error.
func DecodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode JSON %s: %v", b, err)
	}
	return m
}

// AsMap asserts the value is a JSON object.
func AsMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T: %v", v, v)
	}
	return m
}

// AsSlice asserts the value is a JSON array.
func AsSlice(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("expected array, got %T: %v", v, v)
	}
	return s
}

// AsString asserts the value is a JSON string.
func AsString(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T: %v", v, v)
	}
	return s
}

// AsFloat asserts the value is a JSON number.
func AsFloat(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("expected number, got %T: %v", v, v)
	}
	return f
}

// NewMockServer serves the given SSE bodies in order (one per request) and
// records every request body. Extra requests get a 400.
func NewMockServer(t *testing.T, sseResponses []string, requestBodies *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		idx := call
		call++
		if requestBodies != nil {
			*requestBodies = append(*requestBodies, string(body))
		}
		mu.Unlock()

		if idx >= len(sseResponses) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unexpected request"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseResponses[idx]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// NewMockJSONServer serves the given JSON bodies in order.
func NewMockJSONServer(t *testing.T, jsonResponses []string, requestBodies *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		idx := call
		call++
		if requestBodies != nil {
			*requestBodies = append(*requestBodies, string(body))
		}
		mu.Unlock()

		if idx >= len(jsonResponses) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unexpected request"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jsonResponses[idx]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// MockBody is one queued response body, served as SSE or plain JSON.
type MockBody struct {
	Body string
	SSE  bool
}

// NewMockMixedServer serves the given bodies in order (one per request), each
// as text/event-stream or application/json, and records every request body.
func NewMockMixedServer(t *testing.T, responses []MockBody, requestBodies *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		idx := call
		call++
		if requestBodies != nil {
			*requestBodies = append(*requestBodies, string(body))
		}
		mu.Unlock()

		if idx >= len(responses) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unexpected request"}}`))
			return
		}
		if responses[idx].SSE {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = w.Write([]byte(responses[idx].Body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ChatSSE wraps chunks into chat-completions SSE data lines and [DONE].
func ChatSSE(chunks ...string) string {
	out := ""
	for _, c := range chunks {
		out += "data: " + c + "\n\n"
	}
	return out + "data: [DONE]\n\n"
}

// ChatToolCallChunk builds a streaming tool_call delta chunk.
func ChatToolCallChunk(index int, id, name, argsDelta string, withID bool) string {
	toolCall := map[string]any{
		"index":    index,
		"function": map[string]any{"name": name, "arguments": argsDelta},
	}
	if withID {
		toolCall["id"] = id
		toolCall["type"] = "function"
	}
	b, _ := json.Marshal(toolCall)
	return `{"choices":[{"delta":{"tool_calls":[` + string(b) + `]}}]}`
}

// ChatJSON builds a non-streaming chat-completions response body. Sub-agents
// run their internal loop non-streaming (Create), so their mock responses use
// plain JSON while the parent agent streams SSE.
func ChatJSON(content string) string {
	return `{"choices":[{"message":{"role":"assistant","content":` + mustJSON(content) + `},"finish_reason":"stop"}]}`
}

// ChatJSONToolCall builds a non-streaming chat-completions response body that
// carries both text and one tool call.
func ChatJSONToolCall(content, id, name, args string) string {
	return `{"choices":[{"message":{"role":"assistant","content":` + mustJSON(content) +
		`,"tool_calls":[{"id":` + mustJSON(id) + `,"type":"function","function":{"name":` + mustJSON(name) +
		`,"arguments":` + mustJSON(args) + `}}]},"finish_reason":"tool_calls"}]}`
}

// ChatJSONUsage builds a non-streaming chat-completions response body with usage.
func ChatJSONUsage(text string, promptTokens int) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],`+
		`"usage":{"prompt_tokens":%d,"completion_tokens":1}}`, text, promptTokens)
}

// ChatSSEUsage builds a streaming chat-completions response carrying text and usage.
func ChatSSEUsage(text string, promptTokens int) string {
	return ChatSSE(
		fmt.Sprintf(`{"choices":[{"delta":{"role":"assistant","content":%q}}]}`, text),
		fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":1}}`, promptTokens),
	)
}

// mustJSON marshals v to a JSON string, returning "{}" on error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
