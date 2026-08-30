package model

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
