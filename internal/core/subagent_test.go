package core

import (
	"context"
	. "github.com/great-magician-01/callable/internal/testutil"
	"strings"
	"testing"
)

// translatorSub builds a minimal sub-agent definition for tests.
func translatorSub(opts ...SubAgentOption) SubAgent {
	return NewSubAgent("translator", "Translate Chinese to English", opts...)
}

func TestSubAgentIndexBlock(t *testing.T) {
	block := subAgentIndexBlock(DefaultSubAgentLoadToolName, []SubAgent{
		translatorSub(WithSubAgentPrompt("You are a professional translator.")),
		NewSubAgent("researcher", "Research a topic in depth"),
	})
	for _, want := range []string{"load_agent", "translator: Translate Chinese to English", "researcher: Research a topic in depth", "call_<name>"} {
		if !strings.Contains(block, want) {
			t.Errorf("index block missing %q:\n%s", want, block)
		}
	}
	// The sub-agent's system prompt must NOT be in the index (progressive
	// disclosure).
	if strings.Contains(block, "professional translator") {
		t.Errorf("index block leaked sub-agent prompt:\n%s", block)
	}
}

func TestSubAgentLoadUnknown(t *testing.T) {
	reg := newSubAgentRegistry(nil, newToolSet(), []SubAgent{translatorSub()})
	tool := newSubAgentLoadTool(DefaultSubAgentLoadToolName, reg)

	res := tool.Execute(context.Background(), `{"name":"nope"}`)
	if !res.IsError || !strings.Contains(res.Content, "translator") {
		t.Errorf("unknown sub-agent result = %+v", res)
	}
}

func TestSubAgentLoadRegistersCallTool(t *testing.T) {
	set := newToolSet()
	reg := newSubAgentRegistry(nil, set, []SubAgent{
		translatorSub(WithSubAgentPrompt("You are a professional translator.")),
	})
	tool := newSubAgentLoadTool(DefaultSubAgentLoadToolName, reg)

	// Before loading, call_translator must not exist.
	if _, ok := set.get("call_translator"); ok {
		t.Fatal("call_translator registered before load")
	}

	res := tool.Execute(context.Background(), `{"name":"translator"}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	for _, want := range []string{"call_translator", "professional translator"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("usage card missing %q:\n%s", want, res.Content)
		}
	}
	callTool, ok := set.get("call_translator")
	if !ok {
		t.Fatal("call_translator not registered after load")
	}
	if !strings.Contains(callTool.Definition().Description, "Translate Chinese to English") {
		t.Errorf("call tool description = %q", callTool.Definition().Description)
	}

	// Loading again is idempotent and reports the already-loaded state.
	res = tool.Execute(context.Background(), `{"name":"translator"}`)
	if res.IsError || !strings.Contains(res.Content, "already loaded") {
		t.Errorf("re-load result = %+v", res)
	}
	if set.len() != 1 {
		t.Errorf("tool count = %d, want 1 (idempotent load)", set.len())
	}
}

func TestSubAgentLoadNameConflict(t *testing.T) {
	set := newToolSet()
	set.add(NewRawTool("call_translator", "user tool taken the name", `{}`, nil))
	reg := newSubAgentRegistry(nil, set, []SubAgent{translatorSub()})
	tool := newSubAgentLoadTool(DefaultSubAgentLoadToolName, reg)

	res := tool.Execute(context.Background(), `{"name":"translator"}`)
	if !res.IsError || !strings.Contains(res.Content, "already registered") {
		t.Errorf("conflict result = %+v", res)
	}
}

// TestSubAgentDelegationLoop drives a full parent loop over a mock server:
// the parent loads the sub-agent, then calls it; the sub-agent answers; the
// parent summarizes. All requests hit the same mock server in order.
func TestSubAgentDelegationLoop(t *testing.T) {
	loadTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "load_agent", `{"name":"translator"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	callTurn := ChatSSE(
		ChatToolCallChunk(0, "call_2", "call_translator", `{"task":"translate: 你好"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	subAnswer := ChatJSON("hello")
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"The translation is: hello"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var bodies []string
	agent := chatAgentFixture(t, []string{loadTurn, callTurn, subAnswer, finalTurn}, &bodies,
		WithSubAgents(translatorSub(WithSubAgentPrompt("You are a professional translator."))),
	)

	result, err := agent.RunStream(context.Background(), noopEvents, User("translate 你好"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "The translation is: hello" {
		t.Fatalf("final text = %q", result.FinalText)
	}
	if len(bodies) != 4 {
		t.Fatalf("requests = %d, want 4", len(bodies))
	}

	// Turn 1: the sub-agent is NOT exposed as a tool; only load_agent is.
	if !strings.Contains(bodies[0], "load_agent") {
		t.Errorf("first request missing load_agent tool:\n%s", bodies[0])
	}
	if strings.Contains(bodies[0], "call_translator") {
		t.Errorf("sub-agent tool leaked into first request:\n%s", bodies[0])
	}
	// The system prompt index advertises the sub-agent by name/description.
	if !strings.Contains(bodies[0], "available_agents") || !strings.Contains(bodies[0], "Translate Chinese to English") {
		t.Errorf("first request missing sub-agent index:\n%s", bodies[0])
	}

	// Turn 2: after loading, call_translator is advertised.
	if !strings.Contains(bodies[1], "call_translator") {
		t.Errorf("second request missing loaded call_translator tool:\n%s", bodies[1])
	}

	// Sub-agent run: carries its own system prompt and the delegated task.
	if !strings.Contains(bodies[2], "professional translator") {
		t.Errorf("sub-agent request missing its system prompt:\n%s", bodies[2])
	}
	if !strings.Contains(bodies[2], "translate: 你好") {
		t.Errorf("sub-agent request missing delegated task:\n%s", bodies[2])
	}

	// Parent turn 3: the sub-agent's answer is fed back as the tool result.
	if !strings.Contains(bodies[3], "hello") {
		t.Errorf("sub-agent answer not fed back to parent:\n%s", bodies[3])
	}
}

// TestSubAgentCallBeforeLoad verifies a sub-agent cannot be invoked before it
// is loaded: the model gets an unknown-tool error and recovers.
func TestSubAgentCallBeforeLoad(t *testing.T) {
	prematureCall := ChatSSE(
		ChatToolCallChunk(0, "call_1", "call_translator", `{"task":"translate: 你好"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"let me load it first"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var bodies []string
	agent := chatAgentFixture(t, []string{prematureCall, finalTurn}, &bodies,
		WithSubAgents(translatorSub()),
	)

	result, err := agent.RunStream(context.Background(), noopEvents, User("translate"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText == "" {
		t.Fatal("run should complete after the unknown-tool error")
	}
	// The error fed back must not suggest the sub-agent is callable.
	if !strings.Contains(bodies[1], "unknown tool") {
		t.Errorf("expected unknown-tool feedback:\n%s", bodies[1])
	}
}

// TestSubAgentModelOverride checks that a model override only affects the
// sub-agent's requests, not the parent's.
func TestSubAgentModelOverride(t *testing.T) {
	callTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "call_translator", `{"task":"translate: 你好"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	subAnswer := ChatJSON("hello")
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var bodies []string
	server := NewMockServer(t, []string{callTurn, subAnswer, finalTurn}, &bodies)
	parent := NewClient(NewOpenAIProvider("k", server.URL), WithModel("parent-m"))
	agent := NewAgent(parent,
		// Preload the sub-agent to keep the request sequence short.
		WithSubAgents(translatorSub(WithSubAgentModel("sub-m"))),
	)
	agent.subs.load("translator")

	_, err := agent.RunStream(context.Background(), noopEvents, User("translate"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3", len(bodies))
	}
	if !strings.Contains(bodies[0], `"model":"parent-m"`) {
		t.Errorf("parent request should use parent model:\n%s", bodies[0])
	}
	if !strings.Contains(bodies[1], `"model":"sub-m"`) {
		t.Errorf("sub-agent request should use the overridden model:\n%s", bodies[1])
	}
}

// TestSubAgentToolsAndSkills verifies a sub-agent runs with its own tools and
// skills (via its own read_skill), none of which leak into parent requests.
func TestSubAgentToolsAndSkills(t *testing.T) {
	callTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "call_researcher", `{"task":"research Go"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	subAnswer := ChatJSON("research done")
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	search := NewTool("web_search", "Search the web",
		func(ctx context.Context, args struct {
			Q string `json:"q"`
		}) (any, error) {
			return "results", nil
		})
	sub := NewSubAgent("researcher", "Research a topic",
		WithSubAgentTools(search),
		WithSubAgentSkills(NewSkill("citing", "Cite sources properly", "# citing rules")),
	)

	var bodies []string
	agent := chatAgentFixture(t, []string{callTurn, subAnswer, finalTurn}, &bodies, WithSubAgents(sub))
	agent.subs.load("researcher")

	_, err := agent.RunStream(context.Background(), noopEvents, User("research"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3", len(bodies))
	}
	// Parent request: no sub-agent internals.
	if strings.Contains(bodies[0], "web_search") || strings.Contains(bodies[0], "citing") {
		t.Errorf("sub-agent internals leaked into parent request:\n%s", bodies[0])
	}
	// Sub-agent request: its own tool plus the built-in read_skill.
	for _, want := range []string{"web_search", DefaultSkillToolName, "citing"} {
		if !strings.Contains(bodies[1], want) {
			t.Errorf("sub-agent request missing %q:\n%s", want, bodies[1])
		}
	}
}

// TestSubAgentCustomClient verifies WithSubAgentClient routes the sub-agent to
// its own endpoint instead of the parent's.
func TestSubAgentCustomClient(t *testing.T) {
	subAnswer := ChatJSON("bonjour")
	var subBodies []string
	subServer := NewMockServer(t, []string{subAnswer}, &subBodies)

	sub := translatorSub(WithSubAgentClient(NewClient(
		NewOpenAIProvider("k", subServer.URL), WithModel("sub-m"))))

	set := newToolSet()
	parent := NewClient(NewOpenAIProvider("k", "http://unused.invalid"), WithModel("parent-m"))
	reg := newSubAgentRegistry(parent, set, []SubAgent{sub})
	if _, err := reg.load("translator"); err != nil {
		t.Fatal(err)
	}
	callTool, _ := set.get("call_translator")

	res := callTool.Execute(context.Background(), `{"task":"say hello in French"}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "bonjour" {
		t.Errorf("sub-agent answer = %q", res.Content)
	}
	if len(subBodies) != 1 || !strings.Contains(subBodies[0], `"model":"sub-m"`) {
		t.Errorf("sub-server requests = %v", subBodies)
	}
}

// TestSubAgentMaxTurnsPartialAnswer verifies that a sub-agent hitting its turn
// limit hands its last partial answer back to the parent (with a note)
// instead of failing the delegation.
func TestSubAgentMaxTurnsPartialAnswer(t *testing.T) {
	callTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "call_worker", `{"task":"do work"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	// The sub-agent keeps calling its tool (with interim text) and never
	// finishes, so it stops at the max-turns limit.
	subBusy := ChatJSONToolCall("still working on it", "sub_1", "ping", `{}`)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"parent done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ping := NewTool("ping", "Ping",
		func(ctx context.Context, args struct{}) (any, error) { return "pong", nil })
	sub := NewSubAgent("worker", "Do work",
		WithSubAgentTools(ping), WithSubAgentMaxTurns(2))

	var bodies []string
	agent := chatAgentFixture(t, []string{callTurn, subBusy, subBusy, finalTurn}, &bodies,
		WithSubAgents(sub))
	agent.subs.load("worker")

	result, err := agent.RunStream(context.Background(), noopEvents, User("work"))
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "parent done" {
		t.Fatalf("final text = %q", result.FinalText)
	}
	if len(bodies) != 4 {
		t.Fatalf("requests = %d, want 4", len(bodies))
	}
	// The parent's second request must carry the sub-agent's partial answer
	// plus the max-turns note as the call_worker tool result.
	if !strings.Contains(bodies[3], "still working on it") {
		t.Errorf("partial answer not fed back to parent:\n%s", bodies[3])
	}
	if !strings.Contains(bodies[3], "reached max turns") {
		t.Errorf("max-turns note missing from tool result:\n%s", bodies[3])
	}
}

// TestLastAssistantText covers the partial-answer extraction helper.
func TestLastAssistantText(t *testing.T) {
	if got := lastAssistantText(nil); got != "" {
		t.Errorf("nil result: got %q", got)
	}
	res := &AgentResult{Messages: []Message{
		User("task"),
		{Role: RoleAssistant, Parts: []Part{TextPart{Text: "first"}}},
		ToolResults(ToolResultPart{ToolCallID: "c1", Name: "ping", Content: "pong"}),
		{Role: RoleAssistant, Parts: []Part{TextPart{Text: "  second  "}}},
	}}
	if got := lastAssistantText(res); got != "second" {
		t.Errorf("got %q, want %q", got, "second")
	}
	empty := &AgentResult{Messages: []Message{User("task")}}
	if got := lastAssistantText(empty); got != "" {
		t.Errorf("no assistant text: got %q", got)
	}
}

// TestSubAgentEventForwarding verifies that with WithSubAgentEvents(true) the
// sub-agent streams its loop and every event reaches the parent's sink
// wrapped in a SubAgentEvent.
func TestSubAgentEventForwarding(t *testing.T) {
	callTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "call_translator", `{"task":"translate: 你好"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	// With forwarding on, the sub-agent runs streaming (Stream), so its mock
	// responses are SSE too.
	subAnswer := ChatSSE(
		`{"choices":[{"delta":{"content":"hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var bodies []string
	agent := chatAgentFixture(t, []string{callTurn, subAnswer, finalTurn}, &bodies,
		WithSubAgents(translatorSub()),
		WithSubAgentEvents(true),
	)
	agent.subs.load("translator")

	var events []Event
	_, err := agent.RunStream(context.Background(), func(ev Event) { events = append(events, ev) }, User("translate"))
	if err != nil {
		t.Fatal(err)
	}

	var deltas int
	var sawTurnStart, sawDone bool
	for _, ev := range events {
		se, ok := ev.(SubAgentEvent)
		if !ok {
			continue
		}
		if se.SubAgent != "translator" {
			t.Errorf("SubAgentEvent.SubAgent = %q, want translator", se.SubAgent)
		}
		switch se.Event.(type) {
		case TextDeltaEvent:
			deltas++
		case TurnStartEvent:
			sawTurnStart = true
		case AgentDoneEvent:
			sawDone = true
		}
	}
	if deltas != 2 {
		t.Errorf("forwarded text deltas = %d, want 2", deltas)
	}
	if !sawTurnStart || !sawDone {
		t.Errorf("missing wrapped TurnStart/AgentDone events (turnStart=%v done=%v)", sawTurnStart, sawDone)
	}
	// The sub-agent must have used the streaming endpoint.
	if !strings.Contains(bodies[1], `"stream":true`) {
		t.Errorf("sub-agent request not streaming:\n%s", bodies[1])
	}
}

// TestSubAgentEventsOffByDefault verifies that without WithSubAgentEvents the
// sub-agent runs non-streaming and emits no SubAgentEvent.
func TestSubAgentEventsOffByDefault(t *testing.T) {
	callTurn := ChatSSE(
		ChatToolCallChunk(0, "call_1", "call_translator", `{"task":"translate: 你好"}`, true),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	subAnswer := ChatJSON("hello")
	finalTurn := ChatSSE(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	var bodies []string
	agent := chatAgentFixture(t, []string{callTurn, subAnswer, finalTurn}, &bodies,
		WithSubAgents(translatorSub()),
	)
	agent.subs.load("translator")

	var events []Event
	_, err := agent.RunStream(context.Background(), func(ev Event) { events = append(events, ev) }, User("translate"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if _, ok := ev.(SubAgentEvent); ok {
			t.Errorf("unexpected SubAgentEvent with forwarding disabled: %+v", ev)
		}
	}
	if strings.Contains(bodies[1], `"stream":true`) {
		t.Errorf("sub-agent request should be non-streaming by default:\n%s", bodies[1])
	}
}
