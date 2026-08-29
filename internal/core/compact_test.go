package core

import (
	"context"
	. "github.com/great-magician-01/callable/internal/testutil"
	"strings"
	"testing"
)

// chatSessionFixture builds a session over a mock chat-completions JSON server.
func chatSessionFixture(t *testing.T, responses []string, bodies *[]string, opts ...SessionOption) *Session {
	t.Helper()
	server := NewMockJSONServer(t, responses, bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))
	return NewAgent(client).Session(opts...)
}

// chatSessionMixedFixture builds a session over a mock server that answers
// non-streaming runs with JSON and streaming compaction calls with SSE.
func chatSessionMixedFixture(t *testing.T, runBodies []string, compactBodies []string, bodies *[]string, opts ...SessionOption) *Session {
	t.Helper()
	var responses []MockBody
	for _, b := range runBodies {
		responses = append(responses, MockBody{Body: b})
	}
	for _, b := range compactBodies {
		responses = append(responses, MockBody{Body: b, SSE: true})
	}
	server := NewMockMixedServer(t, responses, bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))
	return NewAgent(client).Session(opts...)
}

func TestSessionContextUsageTracking(t *testing.T) {
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 450)}, nil,
		WithContextWindow(1000))

	result, err := sess.Ask(context.Background(), User("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.LastTurnUsage.ContextTokens != 450 {
		t.Errorf("last turn usage = %+v", result.LastTurnUsage)
	}
	if got := sess.ContextWindow(); got != 1000 {
		t.Errorf("context window = %d", got)
	}
	if got := sess.ContextUsage().ContextTokens; got != 450 {
		t.Errorf("context usage = %d", got)
	}
	if got := sess.ContextFillRatio(); got != 0.45 {
		t.Errorf("fill ratio = %v", got)
	}
}

func TestSessionDefaults(t *testing.T) {
	sess := chatSessionFixture(t, nil, nil)
	if got := sess.ContextWindow(); got != DefaultContextWindow {
		t.Errorf("context window = %d, want %d", got, DefaultContextWindow)
	}
	if sess.autoCompact {
		t.Error("auto compact must default to off")
	}
	if sess.autoCompactThreshold != DefaultAutoCompactThreshold {
		t.Errorf("threshold = %v", sess.autoCompactThreshold)
	}
	if got := sess.ContextUsage(); got != (Usage{}) {
		t.Errorf("context usage = %+v before first Ask", got)
	}
}

func TestSessionOptionValidation(t *testing.T) {
	sess := chatSessionFixture(t, nil, nil,
		WithContextWindow(0), WithContextWindow(-5),
		WithAutoCompactThreshold(0), WithAutoCompactThreshold(1.5))
	if sess.contextWindow != DefaultContextWindow {
		t.Errorf("context window = %d", sess.contextWindow)
	}
	if sess.autoCompactThreshold != DefaultAutoCompactThreshold {
		t.Errorf("threshold = %v", sess.autoCompactThreshold)
	}
}

func TestSessionAutoCompactDisabled(t *testing.T) {
	var bodies []string
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 900)}, &bodies,
		WithContextWindow(1000)) // 90% full, auto compact off

	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Errorf("requests = %d, want 1 (no compaction)", len(bodies))
	}
	if got := len(sess.History()); got != 2 {
		t.Errorf("history len = %d, want 2 (untouched)", got)
	}
}

func TestSessionAutoCompactBelowThreshold(t *testing.T) {
	var bodies []string
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 500)}, &bodies,
		WithContextWindow(1000), WithAutoCompact(true)) // 50% < 60%

	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Errorf("requests = %d, want 1 (no compaction)", len(bodies))
	}
	if got := len(sess.History()); got != 2 {
		t.Errorf("history len = %d, want 2 (untouched)", got)
	}
}

func TestSessionAutoCompactTriggers(t *testing.T) {
	var bodies []string
	sess := chatSessionMixedFixture(t,
		[]string{ChatJSONUsage("hi", 700)}, []string{ChatSSEUsage("a summary", 50)}, &bodies,
		WithContextWindow(1000), WithAutoCompact(true)) // 70% >= 60%

	result, err := sess.Ask(context.Background(), User("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "hi" {
		t.Errorf("final text = %q", result.FinalText)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2 (run + compaction)", len(bodies))
	}
	// The compaction request carries the rendered transcript plus instruction,
	// and is streamed (stream:true) to keep long summarizations alive.
	if !strings.Contains(bodies[1], "Summarize the conversation transcript") ||
		!strings.Contains(bodies[1], "hello") {
		t.Errorf("compaction request body = %s", bodies[1])
	}
	if !strings.Contains(bodies[1], `"stream":true`) {
		t.Errorf("compaction request must be streamed, body = %s", bodies[1])
	}
	// History is replaced by the summary message.
	history := sess.History()
	if len(history) != 1 || history[0].Role != RoleUser {
		t.Fatalf("history after compact = %+v", history)
	}
	if text := history[0].Text(); !strings.Contains(text, "a summary") {
		t.Errorf("compacted history text = %q", text)
	}
	// Context fill is reset until the next Ask measures it again.
	if got := sess.ContextUsage(); got != (Usage{}) {
		t.Errorf("context usage after compact = %+v", got)
	}
}

func TestSessionAutoCompactEvent(t *testing.T) {
	// Both the run and the compaction go through the SSE path.
	turn := ChatSSE(
		`{"choices":[{"delta":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":700,"completion_tokens":1}}`,
	)
	server := NewMockServer(t, []string{turn, ChatSSEUsage("a summary", 50)}, nil)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))
	sess := NewAgent(client).Session(WithContextWindow(1000), WithAutoCompact(true))

	var compactEvents []SessionCompactEvent
	_, err := sess.AskStream(context.Background(), func(ev Event) {
		if e, ok := ev.(SessionCompactEvent); ok {
			compactEvents = append(compactEvents, e)
		}
	}, User("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(compactEvents) != 1 {
		t.Fatalf("compact events = %d, want 1", len(compactEvents))
	}
	if compactEvents[0].Summary != "a summary" || compactEvents[0].TokensBefore != 700 {
		t.Errorf("compact event = %+v", compactEvents[0])
	}
}

func TestSessionAutoCompactFailureIsBestEffort(t *testing.T) {
	// Only the run response is queued; the compaction call gets a 400.
	var bodies []string
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 900)}, &bodies,
		WithContextWindow(1000), WithAutoCompact(true))

	result, err := sess.Ask(context.Background(), User("hello"))
	if err != nil {
		t.Fatalf("auto-compact failure must not fail the Ask: %v", err)
	}
	if result.FinalText != "hi" {
		t.Errorf("final text = %q", result.FinalText)
	}
	if got := len(sess.History()); got != 2 {
		t.Errorf("history len = %d, want 2 (untouched)", got)
	}
}

func TestSessionManualCompact(t *testing.T) {
	var bodies []string
	sess := chatSessionMixedFixture(t,
		[]string{ChatJSONUsage("hi", 100)}, []string{ChatSSEUsage("we talked about greetings", 50)}, &bodies,
		WithContextWindow(1000))

	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	summary, err := sess.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary != "we talked about greetings" {
		t.Errorf("summary = %q", summary)
	}
	history := sess.History()
	if len(history) != 1 || !strings.Contains(history[0].Text(), summary) {
		t.Errorf("history after compact = %+v", history)
	}
	if got := sess.ContextUsage(); got != (Usage{}) {
		t.Errorf("context usage after compact = %+v", got)
	}
}

func TestSessionCompactEmptyHistory(t *testing.T) {
	var bodies []string
	sess := chatSessionFixture(t, nil, &bodies)

	summary, err := sess.Compact(context.Background())
	if err != nil || summary != "" {
		t.Errorf("compact empty history = %q, %v", summary, err)
	}
	if len(bodies) != 0 {
		t.Errorf("requests = %d, want 0", len(bodies))
	}
}

func TestSessionCompactErrorKeepsHistory(t *testing.T) {
	// No compaction response queued: the Compact call gets a 400.
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 100)}, nil)

	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Compact(context.Background()); err == nil {
		t.Fatal("expected compaction error")
	}
	if got := len(sess.History()); got != 2 {
		t.Errorf("history len = %d, want 2 (untouched)", got)
	}
}

func TestSessionResetClearsContextUsage(t *testing.T) {
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 450)}, nil, WithContextWindow(1000))
	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	sess.Reset()
	if got := sess.ContextUsage(); got != (Usage{}) {
		t.Errorf("context usage after reset = %+v", got)
	}
	if got := sess.ContextFillRatio(); got != 0 {
		t.Errorf("fill ratio after reset = %v", got)
	}
}

func TestRenderTranscript(t *testing.T) {
	msgs := []Message{
		System("be nice"),
		User("hello", Image("pic.png")),
		Assistant(
			ThinkingPart{Text: "hmm", Signature: "sig"},
			Text("let me check"),
			ToolCallPart{ID: "c1", Name: "get_weather", Arguments: `{"city":"Oslo"}`},
		),
		ToolResults(ToolResultPart{ToolCallID: "c1", Name: "get_weather", Content: "sunny"}),
		ToolResults(ToolResultPart{ToolCallID: "c2", Name: "clock", Content: "boom", IsError: true}),
	}
	out := renderTranscript(msgs)
	for _, want := range []string{
		"[System]", "be nice",
		"[User]", "hello", "(image omitted)",
		"[Assistant]", "(thinking omitted)", "let me check",
		`tool call: get_weather({"city":"Oslo"})`,
		"[Tool results]", "tool result (get_weather): sunny",
		"tool result (clock): ERROR: boom",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q:\n%s", want, out)
		}
	}
}
