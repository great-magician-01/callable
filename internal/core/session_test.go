package core

import (
	"context"
	. "github.com/great-magician-01/callable/internal/testutil"
	"strings"
	"sync"
	"testing"
)

// eventConversationID extracts the ConversationID from any event type.
func eventConversationID(ev Event) string {
	switch e := ev.(type) {
	case MessageStartEvent:
		return e.ConversationID
	case ThinkingDeltaEvent:
		return e.ConversationID
	case TextDeltaEvent:
		return e.ConversationID
	case ToolCallDeltaEvent:
		return e.ConversationID
	case MessageDoneEvent:
		return e.ConversationID
	case TurnStartEvent:
		return e.ConversationID
	case TurnEndEvent:
		return e.ConversationID
	case ToolCallEvent:
		return e.ConversationID
	case ToolResultEvent:
		return e.ConversationID
	case AgentDoneEvent:
		return e.ConversationID
	case SubAgentEvent:
		return e.ConversationID
	case SessionCompactEvent:
		return e.ConversationID
	default:
		return ""
	}
}

func TestEventConversationIDHelperCoversAllEvents(t *testing.T) {
	events := []Event{
		MessageStartEvent{}, ThinkingDeltaEvent{}, TextDeltaEvent{},
		ToolCallDeltaEvent{}, MessageDoneEvent{}, TurnStartEvent{}, TurnEndEvent{},
		ToolCallEvent{}, ToolResultEvent{}, AgentDoneEvent{}, SubAgentEvent{},
		SessionCompactEvent{},
	}
	for _, ev := range events {
		stamped := withConversationID(ev, "id-1")
		if got := eventConversationID(stamped); got != "id-1" {
			t.Errorf("%T: conversation id = %q", ev, got)
		}
	}
}

func TestSessionConversationID(t *testing.T) {
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 100), ChatJSONUsage("again", 200)}, nil)

	id := sess.ID()
	if !strings.HasPrefix(id, "sess-") {
		t.Errorf("session id = %q, want sess- prefix", id)
	}
	for i := 0; i < 2; i++ {
		result, err := sess.Ask(context.Background(), User("hello"))
		if err != nil {
			t.Fatal(err)
		}
		if result.ConversationID != id {
			t.Errorf("ask %d: conversation id = %q, want session id %q", i, result.ConversationID, id)
		}
	}
}

func TestSessionEventsCarryConversationID(t *testing.T) {
	turn := ChatSSE(
		`{"choices":[{"delta":{"content":"hi"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`,
	)
	server := NewMockServer(t, []string{turn}, nil)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))
	sess := NewAgent(client).Session()

	var events []Event
	if _, err := sess.AskStream(context.Background(), func(ev Event) {
		events = append(events, ev)
	}, User("hello")); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for _, ev := range events {
		if got := eventConversationID(ev); got != sess.ID() {
			t.Errorf("%T: conversation id = %q, want %q", ev, got, sess.ID())
		}
	}
}

func TestAgentRunConversationID(t *testing.T) {
	turn := ChatSSE(
		`{"choices":[{"delta":{"content":"hi"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	agent := chatAgentFixture(t, []string{turn, turn}, nil)

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		var events []Event
		result, err := agent.RunStream(context.Background(), func(ev Event) {
			events = append(events, ev)
		}, User("hello"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(result.ConversationID, "run-") {
			t.Errorf("run conversation id = %q, want run- prefix", result.ConversationID)
		}
		if seen[result.ConversationID] {
			t.Errorf("run %d reused conversation id %q", i, result.ConversationID)
		}
		seen[result.ConversationID] = true
		for _, ev := range events {
			if got := eventConversationID(ev); got != result.ConversationID {
				t.Errorf("%T: conversation id = %q, want %q", ev, got, result.ConversationID)
			}
		}
	}
}

// TestSubAgentEventConversationIDs verifies SubAgentEvents carry the parent
// conversation's ID while their inner events carry the sub-agent run's own
// ID.
func TestSubAgentEventConversationIDs(t *testing.T) {
	callTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "call_translator", `{"task":"translate: 你好"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	subAnswer := ChatSSE(
		`{"choices":[{"delta":{"content":"hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	agent := chatAgentFixture(t, []string{callTurn, subAnswer, finalTurn}, nil,
		WithSubAgents(translatorSub()),
		WithSubAgentEvents(true),
	)
	agent.subs.load("translator")

	var events []Event
	result, err := agent.RunStream(context.Background(), func(ev Event) {
		events = append(events, ev)
	}, User("translate"))
	if err != nil {
		t.Fatal(err)
	}

	var subIDs []string
	sawSubEvent := false
	for _, ev := range events {
		se, ok := ev.(SubAgentEvent)
		if !ok {
			continue
		}
		sawSubEvent = true
		if se.ConversationID != result.ConversationID {
			t.Errorf("SubAgentEvent id = %q, want parent id %q", se.ConversationID, result.ConversationID)
		}
		inner := eventConversationID(se.Event)
		if inner == "" || inner == result.ConversationID {
			t.Errorf("inner event id = %q, want the sub-agent's own run id", inner)
		}
		subIDs = append(subIDs, inner)
	}
	if !sawSubEvent {
		t.Fatal("no SubAgentEvent forwarded")
	}
	for _, id := range subIDs[1:] {
		if id != subIDs[0] {
			t.Errorf("inner events carry different ids: %v", subIDs)
		}
	}
}

func TestSessionSnapshotRestore(t *testing.T) {
	responses := []string{ChatJSONUsage("hi", 450)}
	server := NewMockJSONServer(t, responses, nil)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))
	agent := NewAgent(client)

	sess := agent.Session(WithContextWindow(1000))
	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	snap, err := sess.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	restored := agent.Session()
	if err := restored.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if restored.ID() != sess.ID() {
		t.Errorf("restored id = %q, want %q", restored.ID(), sess.ID())
	}
	hist := restored.History()
	if len(hist) != 2 || hist[0].Text() != "hello" || hist[1].Text() != "hi" {
		t.Errorf("restored history = %+v", hist)
	}
	if got := restored.ContextUsage().ContextTokens; got != 450 {
		t.Errorf("restored context usage = %d, want 450", got)
	}

	if err := restored.Restore([]byte("not json")); err == nil {
		t.Error("expected error restoring garbage")
	}
	if err := restored.Restore([]byte(`{"history":[]}`)); err == nil {
		t.Error("expected error restoring snapshot without id")
	}
}

// TestSessionConcurrentAccess runs concurrent asks and readers; it is only
// meaningful under -race (CI), where any data race fails the test.
func TestSessionConcurrentAccess(t *testing.T) {
	const asks = 4
	responses := make([]string, 0, asks)
	for i := 0; i < asks; i++ {
		responses = append(responses, ChatJSONUsage("hi", 100))
	}
	sess := chatSessionFixture(t, responses, nil)

	var wg sync.WaitGroup
	errs := make(chan error, asks)
	for i := 0; i < asks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
				errs <- err
			}
		}()
	}
	// Readers must not block on the in-flight asks.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = sess.History()
				_ = sess.ContextFillRatio()
				_, _ = sess.Snapshot()
			}
		}
	}()
	wg.Wait()
	close(stop)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ask: %v", err)
	}
	// Serialized asks accumulate history deterministically.
	if got := len(sess.History()); got != asks*2 {
		t.Errorf("history len = %d, want %d", got, asks*2)
	}
}

// TestSessionAskSerializes verifies a second Ask observes the first Ask's
// history (asks never run concurrently against a forked history).
func TestSessionAskSerializes(t *testing.T) {
	var bodies []string
	sess := chatSessionFixture(t,
		[]string{ChatJSONUsage("one", 10), ChatJSONUsage("two", 20)}, &bodies)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = sess.Ask(context.Background(), User("hello"))
		}()
	}
	wg.Wait()

	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	// The second ask's request must contain the first ask's answer ("one"),
	// proving the asks ran one after another.
	if !strings.Contains(bodies[1], "one") {
		t.Errorf("second ask did not observe the first ask's history:\n%s", bodies[1])
	}
}

// TestSessionConcurrentRestoreAndID exercises the ID()/Restore/Ask race
// surface; meaningful under -race (CI).
func TestSessionConcurrentRestoreAndID(t *testing.T) {
	sess := chatSessionFixture(t, []string{ChatJSONUsage("hi", 100)}, nil)
	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	snap, err := sess.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = sess.ID()
		}()
		go func() {
			defer wg.Done()
			_ = sess.Restore(snap)
		}()
	}
	wg.Wait()
	if sess.ID() == "" {
		t.Error("session id must never be empty")
	}
}

// TestSessionRestoreOverwritesAndContinues verifies Restore replaces existing
// state and the session remains usable afterwards.
func TestSessionRestoreOverwritesAndContinues(t *testing.T) {
	responses := []string{
		ChatJSONUsage("hi", 100),
		ChatJSONUsage("restored answer", 50),
	}
	server := NewMockJSONServer(t, responses, nil)
	client := NewClient(NewOpenAIProvider("k", server.URL), WithModel("m"))
	agent := NewAgent(client)

	sess := agent.Session()
	if _, err := sess.Ask(context.Background(), User("hello")); err != nil {
		t.Fatal(err)
	}
	snap, err := sess.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Dirty the session, then restore over it.
	sess.SetHistory([]Message{User("junk")})
	if err := sess.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if got := sess.History(); len(got) != 2 || got[1].Text() != "hi" {
		t.Fatalf("history after restore = %+v", got)
	}

	// The restored session continues the restored conversation.
	result, err := sess.Ask(context.Background(), User("again"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ConversationID != sess.ID() {
		t.Errorf("conversation id = %q, want %q", result.ConversationID, sess.ID())
	}
	if got := len(sess.History()); got != 4 {
		t.Errorf("history len = %d, want 4", got)
	}
}
