package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Agent result stop reasons.
const (
	// AgentCompleted means the model produced a final answer.
	AgentCompleted = "completed"
	// AgentMaxTurns means the loop hit the turn limit without a final answer.
	AgentMaxTurns = "max_turns"
)

// ToolDecision is the verdict of a ToolCallHook.
type ToolDecision struct {
	Allow bool
	// Reason explains a denial to the model.
	Reason string
	// NewArgs optionally replaces the tool arguments (JSON) before execution.
	NewArgs string
}

// Approve lets the tool call through unchanged.
func Approve() ToolDecision { return ToolDecision{Allow: true} }

// Deny blocks the call; Reason is fed back to the model as an error result.
func Deny(reason string) ToolDecision { return ToolDecision{Allow: false, Reason: reason} }

// ReplaceArgs lets the call through with different JSON arguments.
func ReplaceArgs(argsJSON string) ToolDecision {
	return ToolDecision{Allow: true, NewArgs: argsJSON}
}

// ToolCallHook intercepts every tool call before execution. Use it to
// implement approval gates, argument rewriting, or auditing. Returning an
// error aborts the agent run.
type ToolCallHook func(ctx context.Context, call ToolCall) (ToolDecision, error)

// Agent runs the tool-calling loop: send conversation -> if the model
// requests tools, execute them and feed results back -> repeat until the
// model answers without tool calls (or the turn limit is hit).
type Agent struct {
	client *Client

	systemPrompt string
	userTools    []Tool
	skills       []Skill
	subAgents    []SubAgent
	thinking     *Thinking

	maxTurns      int
	toolHook      ToolCallHook
	parallelTools bool

	skillToolName     string
	skillHook         SkillReadHook
	skillToolDisabled bool

	subAgentToolName     string
	subAgentToolDisabled bool
	subAgentEvents       bool

	webSearch        *bool  // nil = auto (on iff built-in or Tavily configured)
	tavilyKey        string // Tavily fallback key
	webSearchBuiltin bool   // resolved: provider has server-side search

	tools *toolSet // user tools + built-ins + dynamically loaded call_<subagent>
	subs  *subAgentRegistry
}

// AgentOption configures an Agent.
type AgentOption func(*Agent)

// WithSystemPrompt sets the base system prompt. Skill indexes are appended
// after it.
func WithSystemPrompt(prompt string) AgentOption {
	return func(a *Agent) { a.systemPrompt = prompt }
}

// WithTools registers tools the model may call.
func WithTools(tools ...Tool) AgentOption {
	return func(a *Agent) { a.userTools = append(a.userTools, tools...) }
}

// WithSkills registers skills (progressive disclosure: name and description
// go into the system prompt; the model loads full instructions via the
// built-in read_skill tool).
func WithSkills(skills ...Skill) AgentOption {
	return func(a *Agent) { a.skills = append(a.skills, skills...) }
}

// WithSubAgents registers sub-agent definitions for delegation. Like skills,
// sub-agents use progressive disclosure: only name and description go into
// the system prompt and no tool is registered up front. The model must first
// call the built-in load_agent tool, which registers a call_<name> tool it
// can then invoke to run the sub-agent.
func WithSubAgents(subs ...SubAgent) AgentOption {
	return func(a *Agent) { a.subAgents = append(a.subAgents, subs...) }
}

// WithThinking enables reasoning mode for all runs.
func WithThinking(t Thinking) AgentOption {
	return func(a *Agent) { cp := t; a.thinking = &cp }
}

// WithMaxTurns caps the number of model calls per run. Default 25.
func WithMaxTurns(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.maxTurns = n
		}
	}
}

// WithToolCallHook installs a pre-execution interceptor for tool calls.
func WithToolCallHook(h ToolCallHook) AgentOption {
	return func(a *Agent) { a.toolHook = h }
}

// WithParallelToolExecution lets multiple tool calls in one model turn run
// concurrently. Default is sequential execution.
func WithParallelToolExecution(enabled bool) AgentOption {
	return func(a *Agent) { a.parallelTools = enabled }
}

// WithSkillReadHook installs a hook that can rewrite skill instructions
// before they are returned to the model by read_skill.
func WithSkillReadHook(h SkillReadHook) AgentOption {
	return func(a *Agent) { a.skillHook = h }
}

// WithSkillToolName renames the built-in skill-loading tool (default
// "read_skill").
func WithSkillToolName(name string) AgentOption {
	return func(a *Agent) {
		if name != "" {
			a.skillToolName = name
		}
	}
}

// WithSkillToolDisabled suppresses the built-in read_skill tool; register a
// replacement tool yourself in that case.
func WithSkillToolDisabled() AgentOption {
	return func(a *Agent) { a.skillToolDisabled = true }
}

// WithSubAgentToolName renames the built-in sub-agent-loading tool (default
// "load_agent").
func WithSubAgentToolName(name string) AgentOption {
	return func(a *Agent) {
		if name != "" {
			a.subAgentToolName = name
		}
	}
}

// WithSubAgentToolDisabled suppresses the built-in load_agent tool; registered
// sub-agents then only appear in the system prompt index. Register a
// replacement loading tool (or the call_<name> tools) yourself in that case.
func WithSubAgentToolDisabled() AgentOption {
	return func(a *Agent) { a.subAgentToolDisabled = true }
}

// WithSubAgentEvents enables forwarding of sub-agent loop events to this
// agent's event sink (default off). When enabled, every event emitted inside a
// delegated sub-agent's run is wrapped in a SubAgentEvent (carrying the
// sub-agent's name) and forwarded to the sink passed to RunStream/AskStream.
func WithSubAgentEvents(enabled bool) AgentOption {
	return func(a *Agent) { a.subAgentEvents = enabled }
}

// NewAgent creates an Agent over the client.
func NewAgent(client *Client, opts ...AgentOption) *Agent {
	a := &Agent{
		client:           client,
		maxTurns:         25,
		skillToolName:    DefaultSkillToolName,
		subAgentToolName: DefaultSubAgentLoadToolName,
	}
	for _, o := range opts {
		o(a)
	}
	a.tools = newToolSet()
	a.tools.add(a.userTools...)
	if len(a.skills) > 0 && !a.skillToolDisabled {
		a.tools.add(newSkillTool(a.skillToolName, a.skills, a.skillHook))
	}
	a.subs = newSubAgentRegistry(client, a.tools, a.subAgents)
	if len(a.subAgents) > 0 && !a.subAgentToolDisabled {
		a.tools.add(newSubAgentLoadTool(a.subAgentToolName, a.subs))
	}
	a.resolveWebSearch()
	return a
}

// AgentResult is the outcome of one agent run.
type AgentResult struct {
	// ConversationID identifies the conversation this run belongs to: for a
	// session Ask it is the session's ID (stable across asks), for a bare
	// Run/RunStream it is a fresh ID generated per run.
	ConversationID string
	// FinalText is the model's last answer (empty if the run did not
	// complete).
	FinalText string
	// Messages is the full trajectory of this run: the input messages
	// followed by every assistant message and tool result generated.
	Messages []Message
	// Usage accumulates token consumption across all turns.
	Usage Usage
	// LastTurnUsage is the token usage of the final model turn. Its
	// ContextTokens reflects how full the context window was on that turn;
	// sessions use it to track context fill.
	LastTurnUsage Usage
	// Turns is the number of model calls performed.
	Turns int
	// StopReason is AgentCompleted or AgentMaxTurns.
	StopReason string
}

// Run runs the agent loop without streaming.
func (a *Agent) Run(ctx context.Context, messages ...Message) (*AgentResult, error) {
	return a.RunStream(ctx, nil, messages...)
}

// RunStream runs the agent loop, forwarding all events (turns, provider
// deltas, tool executions) to onEvent as they happen.
//
// Every run belongs to a conversation: bare runs get a fresh conversation ID
// (run-...), session asks reuse the session's ID. The ID is stamped onto
// every event's ConversationID field and reported in AgentResult.
//
// Canceling the context stops the run gracefully: an in-flight upstream
// request is aborted (the server stops generating), no new turn is started,
// and tools not yet executed are skipped with a synthesized error result so
// every tool call stays paired with a result. The returned result is non-nil
// and carries the partial trajectory; the error matches the context error
// (errors.Is(err, context.Canceled) / context.DeadlineExceeded).
func (a *Agent) RunStream(ctx context.Context, onEvent eventSink, messages ...Message) (*AgentResult, error) {
	return a.run(ctx, newID("run"), onEvent, messages...)
}

// run is RunStream with an explicit conversation ID (sessions pass theirs).
func (a *Agent) run(ctx context.Context, conversationID string, onEvent eventSink, messages ...Message) (*AgentResult, error) {
	result := &AgentResult{ConversationID: conversationID, Messages: append([]Message{}, messages...)}
	if len(messages) == 0 {
		return result, fmt.Errorf("callable: agent run requires at least one input message")
	}

	if onEvent != nil {
		onEvent = stampConversationID(onEvent, conversationID)
	}
	// Make the sink visible to call_<name> tools so delegated sub-agents can
	// forward their own events (wrapped in SubAgentEvent) to it. The sink is
	// already stamped, so SubAgentEvents carry the parent's conversation ID.
	if a.subAgentEvents && onEvent != nil {
		ctx = context.WithValue(ctx, subAgentEventSinkKey{}, onEvent)
	}

	conv := make([]Message, 0, len(messages)+4)
	if sys := a.systemMessage(); sys.Role != "" {
		conv = append(conv, sys)
	}
	conv = append(conv, messages...)

	for turn := 1; turn <= a.maxTurns; turn++ {
		// Stop between turns without issuing another upstream request.
		if err := ctx.Err(); err != nil {
			return result, err
		}
		emit(onEvent, TurnStartEvent{Turn: turn})

		req := NewRequest(conv...).WithTools(a.tools.list()...)
		if a.thinking != nil {
			req.Thinking = a.thinking
		}
		req.WebSearch = a.webSearchBuiltin

		var (
			resp *Response
			err  error
		)
		if onEvent != nil {
			resp, err = a.client.Stream(ctx, req, onEvent)
		} else {
			resp, err = a.client.Create(ctx, req)
		}
		if err != nil {
			if resp != nil {
				// Canceled mid-stream: keep the partial text and usage.
				// The incomplete message is NOT appended to Messages, so
				// Messages stays a valid, replayable trajectory.
				result.Turns = turn
				result.Usage.Add(resp.Usage)
				result.LastTurnUsage = resp.Usage
				result.FinalText = resp.Text
			}
			return result, err
		}
		result.Turns = turn
		result.Usage.Add(resp.Usage)
		result.LastTurnUsage = resp.Usage
		conv = append(conv, resp.Message)
		result.Messages = append(result.Messages, resp.Message)

		if len(resp.ToolCalls) == 0 {
			emit(onEvent, TurnEndEvent{Turn: turn})
			result.FinalText = resp.Text
			result.StopReason = AgentCompleted
			emit(onEvent, AgentDoneEvent{Result: result})
			return result, nil
		}

		resultParts, err := a.executeToolCalls(ctx, resp.ToolCalls, onEvent)
		if err != nil {
			return result, err
		}
		toolMsg := ToolResults(resultParts...)
		conv = append(conv, toolMsg)
		result.Messages = append(result.Messages, toolMsg)

		emit(onEvent, TurnEndEvent{Turn: turn})
	}

	result.StopReason = AgentMaxTurns
	return result, &MaxTurnsError{Turns: a.maxTurns, Partial: result}
}

// systemMessage assembles the system prompt: base prompt + skill index +
// sub-agent index.
func (a *Agent) systemMessage() Message {
	var b strings.Builder
	if a.systemPrompt != "" {
		b.WriteString(a.systemPrompt)
	}
	if len(a.skills) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(skillIndexBlock(a.skillToolName, a.skills))
	}
	if !a.subs.empty() {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(subAgentIndexBlock(a.subAgentToolName, a.subs.list()))
	}
	if b.Len() == 0 {
		return Message{}
	}
	return System(b.String())
}

// executeToolCalls runs every requested tool call (sequentially or in
// parallel), emitting ToolCall/ToolResult events.
func (a *Agent) executeToolCalls(ctx context.Context, calls []ToolCall, onEvent eventSink) ([]ToolResultPart, error) {
	results := make([]ToolResultPart, len(calls))
	if a.parallelTools {
		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			firstErr error
		)
		for i, call := range calls {
			wg.Add(1)
			go func(i int, call ToolCall) {
				defer wg.Done()
				part, err := a.executeOne(ctx, call, onEvent)
				results[i] = part
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}(i, call)
		}
		wg.Wait()
		if firstErr != nil {
			return results, firstErr
		}
		return results, nil
	}

	for i, call := range calls {
		part, err := a.executeOne(ctx, call, onEvent)
		results[i] = part
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// executeOne runs a single tool call through the hook (if any) and the
// registry. Tool execution errors become IsError results for the model; only
// hook errors abort the run.
func (a *Agent) executeOne(ctx context.Context, call ToolCall, onEvent eventSink) (ToolResultPart, error) {
	part := ToolResultPart{ToolCallID: call.ID, Name: call.Name}

	// If the context is already done, skip execution but still produce a
	// result so every tool call stays paired with a tool result.
	if err := ctx.Err(); err != nil {
		part.Content = "tool execution skipped: " + err.Error()
		part.IsError = true
		emit(onEvent, ToolResultEvent{Call: call, Result: ToolResult{Content: part.Content, IsError: true}})
		return part, nil
	}

	args := call.Arguments
	if a.toolHook != nil {
		decision, err := a.toolHook(ctx, call)
		if err != nil {
			return part, fmt.Errorf("tool call hook for %q: %w", call.Name, err)
		}
		if !decision.Allow {
			reason := decision.Reason
			if reason == "" {
				reason = "denied by tool call hook"
			}
			part.Content = "Tool call denied: " + reason
			part.IsError = true
			emit(onEvent, ToolResultEvent{Call: call, Result: ToolResult{Content: part.Content, IsError: part.IsError}})
			return part, nil
		}
		if decision.NewArgs != "" {
			args = decision.NewArgs
		}
	}

	emit(onEvent, ToolCallEvent{Call: call})

	tool, ok := a.tools.get(call.Name)
	var result ToolResult
	if !ok {
		result = ErrorResult(fmt.Errorf("unknown tool %q (available tools: %s)",
			call.Name, strings.Join(a.toolNames(), ", ")))
	} else {
		result = tool.Execute(ctx, args)
	}

	part.Content = result.Content
	part.IsError = result.IsError
	emit(onEvent, ToolResultEvent{Call: call, Result: result})
	return part, nil
}

func (a *Agent) toolNames() []string {
	names := make([]string, 0, a.tools.len())
	for _, t := range a.tools.list() {
		names = append(names, t.Definition().Name)
	}
	return names
}

func emit(onEvent eventSink, ev Event) {
	if onEvent != nil {
		onEvent(ev)
	}
}

// Session keeps conversation history across multiple Ask calls on the same
// Agent, preserving thinking blocks and tool trajectories for correct
// replay.
//
// A session has a stable ID (Session.ID) stamped onto every event and result
// of its asks, which distinguishes its events from those of other sessions or
// sub-agent runs sharing the same sink.
//
// Session is safe for concurrent use: Ask/AskStream/Compact are serialized
// (a second Ask waits for the in-flight one), and the read methods (ID,
// History, ContextUsage, ...) never block on an in-flight Ask.
//
// Two caveats: an event sink must not re-enter the session (calling
// Ask/AskStream/Compact from inside a callback deadlocks), and the mutating
// methods (SetHistory, Restore, Reset) must not race an in-flight Ask — the
// Ask commits its own history when it completes, overwriting the mutation.
type Session struct {
	agent *Agent
	id    string

	askMu sync.Mutex   // serializes Ask/AskStream/Compact
	mu    sync.RWMutex // guards history and contextUsage

	history []Message

	contextWindow        int
	autoCompact          bool
	autoCompactThreshold float64
	contextUsage         Usage // last turn's usage; ContextTokens is the current fill
}

// Session creates a new conversation session, configured by opts (see
// WithContextWindow, WithAutoCompact, WithAutoCompactThreshold).
func (a *Agent) Session(opts ...SessionOption) *Session {
	s := &Session{
		agent:                a,
		id:                   newID("sess"),
		contextWindow:        DefaultContextWindow,
		autoCompactThreshold: DefaultAutoCompactThreshold,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ID returns the session's conversation ID, generated at creation and stable
// across asks (it survives Reset; Restore replaces it with the snapshotted
// one). Every event and AgentResult of this session carries it.
func (s *Session) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// Ask appends messages to the conversation and runs the agent loop.
func (s *Session) Ask(ctx context.Context, messages ...Message) (*AgentResult, error) {
	return s.ask(ctx, nil, messages)
}

// AskStream is Ask with streaming events.
func (s *Session) AskStream(ctx context.Context, onEvent eventSink, messages ...Message) (*AgentResult, error) {
	return s.ask(ctx, onEvent, messages)
}

func (s *Session) ask(ctx context.Context, onEvent eventSink, messages []Message) (*AgentResult, error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()

	s.mu.RLock()
	id := s.id
	input := append(append([]Message{}, s.history...), messages...)
	s.mu.RUnlock()

	result, err := s.agent.run(ctx, id, onEvent, input...)
	// Only successful runs extend the history: an aborted run may end with a
	// dangling assistant tool-call message, which providers would reject on
	// replay.
	if err == nil {
		s.mu.Lock()
		s.history = result.Messages
		s.contextUsage = result.LastTurnUsage
		fillRatio := 0.0
		if s.contextWindow > 0 {
			fillRatio = float64(s.contextUsage.ContextTokens) / float64(s.contextWindow)
		}
		s.mu.Unlock()
		if s.autoCompact && fillRatio >= s.autoCompactThreshold {
			// Best-effort: a failed compaction leaves the history untouched
			// and does not fail the Ask.
			if summary, cerr := s.compact(ctx); cerr == nil {
				emit(onEvent, SessionCompactEvent{
					ConversationID: id,
					Summary:        summary,
					TokensBefore:   result.LastTurnUsage.ContextTokens,
				})
			}
		}
	}
	return result, err
}

// ContextWindow returns the configured context window size in tokens.
func (s *Session) ContextWindow() int { return s.contextWindow }

// ContextUsage returns the usage of the most recent successful Ask's final
// model turn. Its ContextTokens is how many tokens the conversation occupied
// in the context window at that point. The zero value is returned before the
// first successful Ask and right after a compaction or Reset; a failed Ask
// leaves the previous value untouched.
func (s *Session) ContextUsage() Usage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contextUsage
}

// ContextFillRatio reports how full the context window was after the most
// recent Ask: ContextUsage().ContextTokens / ContextWindow(), in [0, 1].
func (s *Session) ContextFillRatio() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.contextWindow <= 0 {
		return 0
	}
	return float64(s.contextUsage.ContextTokens) / float64(s.contextWindow)
}

// History returns the conversation so far (without the system prompt).
func (s *Session) History() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Message{}, s.history...)
}

// SetHistory replaces the conversation, e.g. restoring persisted state.
func (s *Session) SetHistory(messages []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append([]Message{}, messages...)
}

// Reset clears the conversation.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = nil
	s.contextUsage = Usage{}
}

// sessionSnapshot is the persisted form of a Session's state.
type sessionSnapshot struct {
	ID           string    `json:"id"`
	History      []Message `json:"history"`
	ContextUsage Usage     `json:"context_usage"`
}

// Snapshot serializes the session state (ID, history, context usage) as JSON
// for persistence. Configuration (context window, auto-compact) is not part
// of the snapshot; re-apply it via SessionOptions after restoring.
func (s *Session) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(sessionSnapshot{
		ID:           s.id,
		History:      s.history,
		ContextUsage: s.contextUsage,
	})
}

// Restore loads a snapshot produced by Snapshot, replacing the session's ID,
// history and context usage.
func (s *Session) Restore(data []byte) error {
	var snap sessionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("callable: restore session: %w", err)
	}
	if snap.ID == "" {
		return fmt.Errorf("callable: restore session: snapshot has no id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = snap.ID
	s.history = snap.History
	s.contextUsage = snap.ContextUsage
	return nil
}
