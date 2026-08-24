package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Role is the author of a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a provider-agnostic chat message. Parts may mix text, images,
// thinking, tool calls and tool results; providers convert them to their
// native wire format.
//
// Messages serialize to JSON (including any provider round-trip data), so
// conversation history can be persisted and restored.
type Message struct {
	Role  Role
	Parts []Part

	// providerExtra holds raw provider payloads that cannot be represented in
	// the unified model but must be replayed verbatim on the next turn (e.g.
	// OpenAI Responses output items such as reasoning). Keyed by provider name.
	providerExtra map[string]json.RawMessage
}

// System creates a system message from plain text.
func System(text string) Message {
	return Message{Role: RoleSystem, Parts: []Part{TextPart{Text: text}}}
}

// User creates a user message. Each argument may be a string (plain text), a
// Part (Text/Image/...), or a []Part. It panics on any other type, since a
// wrong argument is a programming error.
//
//	callable.User("What is in this picture?", callable.Image("/tmp/a.png"))
func User(parts ...any) Message {
	return newMessage(RoleUser, parts)
}

// Assistant creates an assistant message, mainly for seeding conversation
// history manually. It accepts the same argument types as User.
func Assistant(parts ...any) Message {
	return newMessage(RoleAssistant, parts)
}

// ToolResults creates a message holding one or more tool results.
func ToolResults(results ...ToolResultPart) Message {
	parts := make([]Part, len(results))
	for i, r := range results {
		parts[i] = r
	}
	return Message{Role: RoleTool, Parts: parts}
}

func newMessage(role Role, parts []any) Message {
	m := Message{Role: role}
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			m.Parts = append(m.Parts, TextPart{Text: v})
		case Part:
			m.Parts = append(m.Parts, v)
		case []Part:
			m.Parts = append(m.Parts, v...)
		default:
			panic(fmt.Sprintf("callable: unsupported message part type %T (use string, Part or []Part)", p))
		}
	}
	return m
}

// Text returns the concatenation of all TextPart contents in the message.
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if t, ok := p.(TextPart); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// Thinking returns the concatenation of all ThinkingPart contents.
func (m Message) Thinking() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if t, ok := p.(ThinkingPart); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// ToolCalls returns all ToolCallPart contents in order.
func (m Message) ToolCalls() []ToolCallPart {
	var calls []ToolCallPart
	for _, p := range m.Parts {
		if c, ok := p.(ToolCallPart); ok {
			calls = append(calls, c)
		}
	}
	return calls
}

// ToolResultsOf returns all ToolResultPart contents in order.
func (m Message) ToolResultsOf() []ToolResultPart {
	var results []ToolResultPart
	for _, p := range m.Parts {
		if r, ok := p.(ToolResultPart); ok {
			results = append(results, r)
		}
	}
	return results
}

// SetProviderExtra attaches a raw provider payload to the message. Providers
// use this to preserve wire-format data (e.g. OpenAI Responses output items)
// across turns. The provider parameter is a Provider.Name() value.
func (m *Message) SetProviderExtra(provider string, raw json.RawMessage) {
	if m.providerExtra == nil {
		m.providerExtra = map[string]json.RawMessage{}
	}
	m.providerExtra[provider] = raw
}

// ProviderExtra returns the raw payload previously attached for provider, or
// nil.
func (m Message) ProviderExtra(provider string) json.RawMessage {
	return m.providerExtra[provider]
}

// MarshalJSON encodes the message including provider round-trip payloads.
func (m Message) MarshalJSON() ([]byte, error) {
	out := struct {
		Role           Role                       `json:"role"`
		Parts          []Part                     `json:"parts"`
		ProviderExtras map[string]json.RawMessage `json:"provider_extra,omitempty"`
	}{
		Role:           m.Role,
		Parts:          m.Parts,
		ProviderExtras: m.providerExtra,
	}
	if out.Parts == nil {
		out.Parts = []Part{}
	}
	return json.Marshal(out)
}

// UnmarshalJSON decodes a message, restoring concrete part types and any
// provider round-trip payloads.
func (m *Message) UnmarshalJSON(data []byte) error {
	var in struct {
		Role           Role                       `json:"role"`
		Parts          []json.RawMessage          `json:"parts"`
		ProviderExtras map[string]json.RawMessage `json:"provider_extra"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("callable: decode message: %w", err)
	}
	m.Role = in.Role
	if in.Role == "" {
		m.Role = RoleUser
	}
	m.Parts = make([]Part, 0, len(in.Parts))
	for _, raw := range in.Parts {
		p, err := UnmarshalPart(raw)
		if err != nil {
			return err
		}
		m.Parts = append(m.Parts, p)
	}
	m.providerExtra = in.ProviderExtras
	return nil
}
