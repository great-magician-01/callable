package core

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
type Event interface{ event() }

// MessageStartEvent marks the beginning of an assistant message.
type MessageStartEvent struct{}

func (MessageStartEvent) event() {}

// ThinkingDeltaEvent carries an increment of reasoning text.
type ThinkingDeltaEvent struct {
	Delta string
}

func (ThinkingDeltaEvent) event() {}

// TextDeltaEvent carries an increment of the answer text.
type TextDeltaEvent struct {
	Delta string
}

func (TextDeltaEvent) event() {}

// ToolCallDeltaEvent carries an increment of a tool call: the ID and Name
// fields are set on the first increment for a tool call, ArgsDelta carries
// argument JSON fragments. Index identifies the tool call within the turn.
type ToolCallDeltaEvent struct {
	Index     int
	ID        string
	Name      string
	ArgsDelta string
}

func (ToolCallDeltaEvent) event() {}

// MessageDoneEvent marks the end of an assistant message and carries the
// assembled message plus this turn's usage.
type MessageDoneEvent struct {
	Message    Message
	Usage      Usage
	StopReason StopReason
}

func (MessageDoneEvent) event() {}

// TurnStartEvent marks the beginning of agent loop turn n (1-based). Each
// turn is one model call plus any tool executions it requested.
type TurnStartEvent struct {
	Turn int
}

func (TurnStartEvent) event() {}

// TurnEndEvent marks the end of a turn.
type TurnEndEvent struct {
	Turn int
}

func (TurnEndEvent) event() {}

// ToolCallEvent is emitted just before a tool is executed, after the
// ToolCallHook (if any) approved it.
type ToolCallEvent struct {
	Call ToolCall
}

func (ToolCallEvent) event() {}

// ToolResultEvent is emitted after a tool finished executing.
type ToolResultEvent struct {
	Call   ToolCall
	Result ToolResult
}

func (ToolResultEvent) event() {}

// AgentDoneEvent is emitted when the agent loop finished with a final answer.
type AgentDoneEvent struct {
	Result *AgentResult
}

func (AgentDoneEvent) event() {}

// SubAgentEvent wraps an event emitted inside a sub-agent's own loop. It is
// only produced when the parent agent enables sub-agent event forwarding (see
// WithSubAgentEvents); SubAgent is the name of the delegated sub-agent the
// inner Event came from.
type SubAgentEvent struct {
	SubAgent string
	Event    Event
}

func (SubAgentEvent) event() {}

// eventSink is the callback signature used for streaming.
type eventSink func(Event)
