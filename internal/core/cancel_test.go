package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// blockingSSEServer sends one SSE chunk, flushes, then blocks until the
// client disconnects. disconnected is closed once the server observes the
// client abort (proof that the upstream connection was closed).
func blockingSSEServer(t *testing.T, firstChunk string, disconnected chan struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(firstChunk))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // block until the client aborts the request
		close(disconnected)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestChatStreamCancelReturnsPartial verifies that canceling mid-stream
// aborts the upstream connection and returns the partially assembled
// response together with the context error.
func TestChatStreamCancelReturnsPartial(t *testing.T) {
	disconnected := make(chan struct{})
	srv := blockingSSEServer(t,
		`data: {"choices":[{"delta":{"content":"partial answer"}}]}`+"\n\n",
		disconnected)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL)), WithModel("m"))

	resp, err := client.Stream(ctx, NewRequest(User("hi")), func(ev Event) {
		if _, ok := ev.(TextDeltaEvent); ok {
			cancel() // stop after the first text delta
		}
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if resp == nil {
		t.Fatal("partial response is nil")
	}
	if resp.Text != "partial answer" {
		t.Errorf("partial text = %q", resp.Text)
	}

	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Error("server never saw the client disconnect — upstream call not aborted")
	}
}

// TestAnthropicStreamCancelReturnsPartial covers the Anthropic assemble path
// on cancellation.
func TestAnthropicStreamCancelReturnsPartial(t *testing.T) {
	disconnected := make(chan struct{})
	chunk := "event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"part"}}` + "\n\n"
	srv := blockingSSEServer(t, chunk, disconnected)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewClient(NewAnthropicProvider("k", WithBaseURL(srv.URL)), WithModel("claude-x"))

	resp, err := client.Stream(ctx, NewRequest(User("hi")), func(ev Event) {
		if _, ok := ev.(TextDeltaEvent); ok {
			cancel()
		}
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if resp == nil || resp.Text != "part" {
		t.Errorf("partial response = %+v", resp)
	}
	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Error("server never saw the client disconnect")
	}
}

// TestAgentCancelStopsBeforeNextTurn cancels during tool execution and
// verifies no further upstream request is made, and the partial result still
// forms a complete, replayable trajectory.
func TestAgentCancelStopsBeforeNextTurn(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(chatSSE(
			chatToolCallChunk(0, "call_1", "get_weather", `{"city":"Oslo"}`, true),
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		)))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tool := NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		cancel() // cancel the run from inside the tool
		return "sunny", nil
	})
	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL)), WithModel("m"))
	agent := NewAgent(client, WithTools(tool))

	result, err := agent.RunStream(ctx, noopEvents, User("weather?"))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if result == nil {
		t.Fatal("partial result is nil")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1 (no new turn after cancel)", got)
	}
	if result.Turns != 1 {
		t.Errorf("turns = %d, want 1", result.Turns)
	}
	// Trajectory must be complete: assistant tool call + its tool result.
	if len(result.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (user, assistant, tool results)", len(result.Messages))
	}
	last := result.Messages[2]
	if len(last.ToolResultsOf()) != 1 || last.ToolResultsOf()[0].Content != "sunny" {
		t.Errorf("tool results = %+v", last.ToolResultsOf())
	}
}

// TestAgentCancelSkipsRemainingTools verifies that once the context is
// canceled, not-yet-executed tools are skipped with a synthesized error
// result (keeping every tool call paired with a result).
func TestAgentCancelSkipsRemainingTools(t *testing.T) {
	turn := chatSSE(
		chatToolCallChunk(0, "call_1", "first", `{}`, true),
		chatToolCallChunk(1, "call_2", "second", `{}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	srv := newMockServer(t, []string{turn}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := NewTool("first", "First", func(ctx context.Context, args struct{}) (any, error) {
		cancel()
		return "ran", nil
	})
	var secondRan atomic.Bool
	second := NewTool("second", "Second", func(ctx context.Context, args struct{}) (any, error) {
		secondRan.Store(true)
		return "should not run", nil
	})
	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL)), WithModel("m"))
	agent := NewAgent(client, WithTools(first, second))

	result, err := agent.RunStream(ctx, noopEvents, User("go"))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if secondRan.Load() {
		t.Error("second tool executed despite cancellation")
	}
	if result == nil || len(result.Messages) != 3 {
		t.Fatalf("result = %+v", result)
	}
	results := result.Messages[2].ToolResultsOf()
	if len(results) != 2 {
		t.Fatalf("tool results = %d, want 2", len(results))
	}
	if !results[1].IsError || !strings.Contains(results[1].Content, "skipped") {
		t.Errorf("second result = %+v, want synthesized skip error", results[1])
	}
}

// TestCreateCanceledContext verifies a pre-canceled context fails fast
// without any upstream request.
func TestCreateCanceledContext(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL)), WithModel("m"))

	if _, err := client.Create(ctx, NewRequest(User("hi"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("upstream requests = %d, want 0", got)
	}
}

// TestSessionCancelKeepsHistory verifies an aborted run does not corrupt the
// session history.
func TestSessionCancelKeepsHistory(t *testing.T) {
	turn := chatSSE(
		chatToolCallChunk(0, "call_1", "get_weather", `{}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	srv := newMockServer(t, []string{turn, turn}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tool := NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		cancel()
		return "sunny", nil
	})
	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL)), WithModel("m"))
	sess := NewAgent(client, WithTools(tool)).Session()

	if _, err := sess.AskStream(ctx, noopEvents, User("weather?")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(sess.History()) != 0 {
		t.Errorf("history = %d messages, want 0 after aborted run", len(sess.History()))
	}
}
