package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// newMockServer serves the given SSE bodies in order (one per request) and
// records every request body. Extra requests get a 400.
func newMockServer(t *testing.T, sseResponses []string, requestBodies *[]string) *httptest.Server {
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

// newMockJSONServer serves the given JSON bodies in order.
func newMockJSONServer(t *testing.T, jsonResponses []string, requestBodies *[]string) *httptest.Server {
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

// chatSSE wraps chunks into chat-completions SSE data lines and [DONE].
func chatSSE(chunks ...string) string {
	out := ""
	for _, c := range chunks {
		out += "data: " + c + "\n\n"
	}
	return out + "data: [DONE]\n\n"
}

// chatToolCallChunk builds a streaming tool_call delta chunk.
func chatToolCallChunk(index int, id, name, argsDelta string, withID bool) string {
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

// noopEvents discards streaming events; passing it keeps the agent on the
// streaming code path (matching SSE fixtures) without observing events.
var noopEvents = func(Event) {}
