package core

import (
	"fmt"
)

// MaxTurnsError is returned by Agent.Run when the agent loop hit the maximum
// number of turns without producing a final answer. Partial always carries
// everything that happened up to that point.
type MaxTurnsError struct {
	Turns   int
	Partial *AgentResult
}

func (e *MaxTurnsError) Error() string {
	return fmt.Sprintf("callable: agent reached the maximum number of turns (%d) without a final answer", e.Turns)
}
