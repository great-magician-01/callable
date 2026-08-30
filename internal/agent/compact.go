package agent

import (
	"context"
	"fmt"
	"strings"

	model "github.com/great-magician-01/callable/internal/model"
)

// DefaultContextWindow is the default context window size (in tokens) a
// Session measures its context fill against.
const DefaultContextWindow = 1_000_000

// DefaultAutoCompactThreshold is the default context fill ratio at which an
// auto-compacting Session compacts its history.
const DefaultAutoCompactThreshold = 0.6

// SessionOption configures a Session.
type SessionOption func(*Session)

// WithContextWindow sets the context window size (in tokens) the session
// measures context fill against. Default DefaultContextWindow; non-positive
// values are ignored.
func WithContextWindow(tokens int) SessionOption {
	return func(s *Session) {
		if tokens > 0 {
			s.contextWindow = tokens
		}
	}
}

// WithAutoCompact enables automatic history compaction: after every Ask whose
// final turn filled the context window to WithAutoCompactThreshold or more,
// the session summarizes and replaces its history (see Session.Compact).
// Default off. Auto-compact is a session-level feature and never applies to
// delegated sub-agents, which run without a session.
func WithAutoCompact(enabled bool) SessionOption {
	return func(s *Session) { s.autoCompact = enabled }
}

// WithAutoCompactThreshold sets the context fill ratio (0, 1] at which
// auto-compact triggers. Default DefaultAutoCompactThreshold; values outside
// (0, 1] are ignored.
func WithAutoCompactThreshold(ratio float64) SessionOption {
	return func(s *Session) {
		if ratio > 0 && ratio <= 1 {
			s.autoCompactThreshold = ratio
		}
	}
}

// compactInstruction asks the model to summarize a rendered transcript.
const compactInstruction = `Summarize the conversation transcript above so it can be continued without the full history. ` +
	`Preserve: the user's goals and requirements, key decisions, important facts and code changes, ` +
	`tool results that still matter, and any pending tasks. Be concise; output only the summary.`

// compactedPrefix marks the single user message a compacted history is
// replaced with.
const compactedPrefix = "[Conversation compacted] Summary of the earlier conversation:\n\n"

// Compact summarizes the conversation history with the agent's model and
// replaces the history with the summary, freeing context for subsequent
// turns. It returns the summary. Compact is a no-op on an empty history. On
// error the history is left untouched.
//
// The summarization call is streamed (a long transcript can take a while to
// summarize, and streaming keeps the connection alive); its deltas are
// discarded.
func (s *Session) Compact(ctx context.Context) (string, error) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	return s.compact(ctx)
}

// compact is Compact without locking; callers must hold askMu.
func (s *Session) compact(ctx context.Context) (string, error) {
	s.mu.RLock()
	transcript := renderTranscript(s.history)
	s.mu.RUnlock()
	if transcript == "" {
		return "", nil
	}
	req := model.NewRequest(model.User(transcript + "\n\n" + compactInstruction))
	resp, err := s.agent.client.Stream(ctx, req, nil)
	if err != nil {
		return "", fmt.Errorf("callable: compact: %w", err)
	}
	summary := strings.TrimSpace(resp.Text)
	if summary == "" {
		return "", fmt.Errorf("callable: compact: model returned an empty summary")
	}
	s.mu.Lock()
	s.history = []model.Message{model.User(compactedPrefix + summary)}
	s.contextUsage = model.Usage{}
	s.mu.Unlock()
	return summary, nil
}

// renderTranscript renders messages as a plain-text transcript for the
// compaction summary call. Rendering to text (instead of replaying the raw
// history) sidesteps provider round-trip concerns such as thinking
// signatures, Responses reasoning items and tool-call pairing.
func renderTranscript(msgs []model.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case model.RoleSystem:
			b.WriteString("\n\n[System]\n")
		case model.RoleUser:
			b.WriteString("\n\n[User]\n")
		case model.RoleAssistant:
			b.WriteString("\n\n[Assistant]\n")
		case model.RoleTool:
			b.WriteString("\n\n[Tool results]\n")
		default:
			b.WriteString("\n\n[" + string(m.Role) + "]\n")
		}
		for _, p := range m.Parts {
			switch v := p.(type) {
			case model.TextPart:
				b.WriteString(v.Text)
				b.WriteString("\n")
			case model.ThinkingPart:
				b.WriteString("(thinking omitted)\n")
			case model.ImagePart:
				b.WriteString("(image omitted)\n")
			case model.ToolCallPart:
				fmt.Fprintf(&b, "tool call: %s(%s)\n", v.Name, v.Arguments)
			case model.ToolResultPart:
				content := v.Content
				if v.IsError {
					content = "ERROR: " + content
				}
				fmt.Fprintf(&b, "tool result (%s): %s\n", v.Name, content)
			}
		}
	}
	return strings.TrimPrefix(b.String(), "\n\n")
}
