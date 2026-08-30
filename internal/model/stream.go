package model

// Event is a streaming event emitted during a Stream call or an Agent loop.
// Provider-level events describe one model turn; agent-level events wrap the
// whole loop (turns and tool executions).
//
// Handle events with a type switch:
//
//	func(ev callable.Event) {
//		switch e := ev.(type) {
//		case callable.ThinkingDeltaEvent: ...
//		case callable.TextDeltaEvent:     ...
//		case callable.ToolResultEvent:    ...
//		}
//	}
//
// Every event carries a ConversationID identifying the conversation it
// belongs to: a session's events all share the session's ID, a bare
// Agent.Run/RunStream gets a fresh ID per run, and a SubAgentEvent is stamped
// with the parent conversation's ID (its inner Event carries the sub-agent's
// own ID). Events from a bare Client.Create/Stream call have no ID.
type Event interface{ event() }

// MessageStartEvent marks the beginning of an assistant message.
type MessageStartEvent struct {
	ConversationID string
}

func (MessageStartEvent) event() {}

// ThinkingDeltaEvent carries an increment of reasoning text.
type ThinkingDeltaEvent struct {
	ConversationID string
	Delta          string
}

func (ThinkingDeltaEvent) event() {}

// TextDeltaEvent carries an increment of the answer text.
type TextDeltaEvent struct {
	ConversationID string
	Delta          string
}

func (TextDeltaEvent) event() {}

// ToolCallDeltaEvent carries an increment of a streamed tool call: the ID and
// Name fields are set on the first increment for a tool call, ArgsDelta
// carries argument JSON fragments. Index identifies the tool call within the
// turn.
type ToolCallDeltaEvent struct {
	ConversationID string
	Index          int
	ID             string
	Name           string
	ArgsDelta      string
}

func (ToolCallDeltaEvent) event() {}

// MessageDoneEvent marks the end of an assistant message and carries the
// assembled message plus this turn's usage.
type MessageDoneEvent struct {
	ConversationID string
	Message        Message
	Usage          Usage
	StopReason     StopReason
}

func (MessageDoneEvent) event() {}

// TurnStartEvent marks the beginning of agent loop turn n (1-based). Each
// turn is one model call plus any tool executions it requested.
type TurnStartEvent struct {
	ConversationID string
	Turn           int
}

func (TurnStartEvent) event() {}

// TurnEndEvent marks the end of a turn.
type TurnEndEvent struct {
	ConversationID string
	Turn           int
}

func (TurnEndEvent) event() {}

// ToolCallEvent is emitted just before a tool is executed, after the
// ToolCallHook (if any) approved it.
type ToolCallEvent struct {
	ConversationID string
	Call           ToolCall
}

func (ToolCallEvent) event() {}

// ToolResultEvent is emitted after a tool finished executing.
type ToolResultEvent struct {
	ConversationID string
	Call           ToolCall
	Result         ToolResult
}

func (ToolResultEvent) event() {}

// AgentDoneEvent is emitted when the agent loop finished with a final answer.
type AgentDoneEvent struct {
	ConversationID string
	Result         *AgentResult
}

func (AgentDoneEvent) event() {}

// SubAgentEvent wraps an event emitted inside a sub-agent's own loop. It is
// only produced when the parent agent enables sub-agent event forwarding (see
// WithSubAgentEvents); SubAgent is the name of the delegated sub-agent the
// inner Event came from. Its ConversationID is the parent conversation's,
// while the inner Event carries the sub-agent run's own ID.
type SubAgentEvent struct {
	ConversationID string
	SubAgent       string
	Event          Event
}

func (SubAgentEvent) event() {}

// SessionCompactEvent is emitted to the AskStream sink after a session
// auto-compacts its history at the end of a run. Summary is the generated
// conversation summary; TokensBefore is how many context tokens the
// conversation occupied before compaction.
type SessionCompactEvent struct {
	ConversationID string
	Summary        string
	TokensBefore   int
}

func (SessionCompactEvent) event() {}

// EventSink is the callback signature used for streaming.
type EventSink func(Event)
