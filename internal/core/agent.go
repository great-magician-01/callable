package core

import (
	"context"
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
	thinking     *Thinking

	maxTurns      int
	toolHook      ToolCallHook
	parallelTools bool

	skillToolName     string
	skillHook         SkillReadHook
	skillToolDisabled bool

	tools *toolSet // user tools + built-in read_skill
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

// NewAgent creates an Agent over the client.
func NewAgent(client *Client, opts ...AgentOption) *Agent {
	a := &Agent{
		client:        client,
		maxTurns:      25,
		skillToolName: DefaultSkillToolName,
	}
	for _, o := range opts {
		o(a)
	}
	a.tools = newToolSet()
	a.tools.add(a.userTools...)
	if len(a.skills) > 0 && !a.skillToolDisabled {
		a.tools.add(newSkillTool(a.skillToolName, a.skills, a.skillHook))
	}
	return a
}

// AgentResult is the outcome of one agent run.
type AgentResult struct {
	// FinalText is the model's last answer (empty if the run did not
	// complete).
	FinalText string
	// Messages is the full trajectory of this run: the input messages
	// followed by every assistant message and tool result generated.
	Messages []Message
	// Usage accumulates token consumption across all turns.
	Usage Usage
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
// Canceling the context stops the run gracefully: an in-flight upstream
// request is aborted (the server stops generating), no new turn is started,
// and tools not yet executed are skipped with a synthesized error result so
// every tool call stays paired with a result. The returned result is non-nil
// and carries the partial trajectory; the error matches the context error
// (errors.Is(err, context.Canceled) / context.DeadlineExceeded).
func (a *Agent) RunStream(ctx context.Context, onEvent eventSink, messages ...Message) (*AgentResult, error) {
	result := &AgentResult{Messages: append([]Message{}, messages...)}
	if len(messages) == 0 {
		return result, fmt.Errorf("callable: agent run requires at least one input message")
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
				result.FinalText = resp.Text
			}
			return result, err
		}
		result.Turns = turn
		result.Usage.Add(resp.Usage)
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

// systemMessage assembles the system prompt: base prompt + skill index.
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
type Session struct {
	agent   *Agent
	history []Message
}

// Session creates a new conversation session.
func (a *Agent) Session() *Session {
	return &Session{agent: a}
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
	input := append(append([]Message{}, s.history...), messages...)
	result, err := s.agent.RunStream(ctx, onEvent, input...)
	// Only successful runs extend the history: an aborted run may end with a
	// dangling assistant tool-call message, which providers would reject on
	// replay.
	if err == nil {
		s.history = result.Messages
	}
	return result, err
}

// History returns the conversation so far (without the system prompt).
func (s *Session) History() []Message {
	return append([]Message{}, s.history...)
}

// SetHistory replaces the conversation, e.g. restoring persisted state.
func (s *Session) SetHistory(messages []Message) {
	s.history = append([]Message{}, messages...)
}

// Reset clears the conversation.
func (s *Session) Reset() {
	s.history = nil
}
