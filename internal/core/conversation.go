package core

// stampConversationID returns a sink that stamps every forwarded event with
// the given conversation ID.
func stampConversationID(sink eventSink, id string) eventSink {
	return func(ev Event) { sink(withConversationID(ev, id)) }
}

// withConversationID returns ev with its ConversationID field set.
func withConversationID(ev Event, id string) Event {
	switch e := ev.(type) {
	case MessageStartEvent:
		e.ConversationID = id
		return e
	case ThinkingDeltaEvent:
		e.ConversationID = id
		return e
	case TextDeltaEvent:
		e.ConversationID = id
		return e
	case ToolCallDeltaEvent:
		e.ConversationID = id
		return e
	case MessageDoneEvent:
		e.ConversationID = id
		return e
	case TurnStartEvent:
		e.ConversationID = id
		return e
	case TurnEndEvent:
		e.ConversationID = id
		return e
	case ToolCallEvent:
		e.ConversationID = id
		return e
	case ToolResultEvent:
		e.ConversationID = id
		return e
	case AgentDoneEvent:
		e.ConversationID = id
		return e
	case SubAgentEvent:
		e.ConversationID = id
		return e
	case SessionCompactEvent:
		e.ConversationID = id
		return e
	default:
		return ev
	}
}
