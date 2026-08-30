package agent

import (
	model "github.com/great-magician-01/callable/internal/model"
)

// stampConversationID returns a sink that stamps every forwarded event with
// the given conversation ID.
func stampConversationID(sink model.EventSink, id string) model.EventSink {
	return func(ev model.Event) { sink(withConversationID(ev, id)) }
}

// withConversationID returns ev with its ConversationID field set.
func withConversationID(ev model.Event, id string) model.Event {
	switch e := ev.(type) {
	case model.MessageStartEvent:
		e.ConversationID = id
		return e
	case model.ThinkingDeltaEvent:
		e.ConversationID = id
		return e
	case model.TextDeltaEvent:
		e.ConversationID = id
		return e
	case model.ToolCallDeltaEvent:
		e.ConversationID = id
		return e
	case model.MessageDoneEvent:
		e.ConversationID = id
		return e
	case model.TurnStartEvent:
		e.ConversationID = id
		return e
	case model.TurnEndEvent:
		e.ConversationID = id
		return e
	case model.ToolCallEvent:
		e.ConversationID = id
		return e
	case model.ToolResultEvent:
		e.ConversationID = id
		return e
	case model.AgentDoneEvent:
		e.ConversationID = id
		return e
	case model.SubAgentEvent:
		e.ConversationID = id
		return e
	case model.SessionCompactEvent:
		e.ConversationID = id
		return e
	default:
		return ev
	}
}
