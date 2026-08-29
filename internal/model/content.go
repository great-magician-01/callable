package model

import (
	"encoding/json"
	"fmt"
)

// Part is one piece of content inside a Message. The concrete types are
// TextPart, ImagePart, ThinkingPart, ToolCallPart and ToolResultPart. Parts
// serialize to JSON with a discriminating "type" field, so whole
// conversations can be persisted and restored.
type Part interface {
	partType() string
}

// TextPart is plain text content.
type TextPart struct {
	Text string `json:"text"`
}

// Text creates a TextPart.
func Text(text string) TextPart {
	return TextPart{Text: text}
}

func (TextPart) partType() string { return "text" }

// MarshalJSON adds the type discriminator. The local type alias strips the
// MarshalJSON method so encoding does not recurse.
func (p TextPart) MarshalJSON() ([]byte, error) {
	type alias TextPart
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{"text", alias(p)})
}

// ImagePart references an image. Exactly one source should be set; see the
// Image, ImageURL and ImageBytes constructors. The image is resolved (read,
// typed, base64-encoded) lazily at request time by each provider, so the same
// message history can be sent to any provider.
type ImagePart struct {
	// Path is a local file path.
	Path string `json:"path,omitempty"`
	// URL is a remote image URL, passed through to the API untouched.
	URL string `json:"url,omitempty"`
	// Data is raw image bytes.
	Data []byte `json:"data,omitempty"`
	// MediaType is the MIME type (e.g. "image/png"). Detected automatically
	// when empty.
	MediaType string `json:"media_type,omitempty"`
}

func (ImagePart) partType() string { return "image" }

// MarshalJSON adds the type discriminator (see TextPart.MarshalJSON).
func (p ImagePart) MarshalJSON() ([]byte, error) {
	type alias ImagePart
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{"image", alias(p)})
}

// ThinkingPart is model reasoning output ("thinking" / "reasoning"). It is
// preserved in conversation history because providers require it to be sent
// back for correct multi-turn tool use: Anthropic needs the signature,
// OpenAI Responses the reasoning item id, and DeepSeek/GLM the
// reasoning_content text.
type ThinkingPart struct {
	Text string `json:"text"`
	// Signature is Anthropic's thinking block signature (round-trip only).
	Signature string `json:"signature,omitempty"`
	// ID is the OpenAI Responses reasoning item id (round-trip only).
	ID string `json:"id,omitempty"`
}

func (ThinkingPart) partType() string { return "thinking" }

// MarshalJSON adds the type discriminator (see TextPart.MarshalJSON).
func (p ThinkingPart) MarshalJSON() ([]byte, error) {
	type alias ThinkingPart
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{"thinking", alias(p)})
}

// ToolCallPart is a tool invocation requested by the model.
type ToolCallPart struct {
	// ID is the provider-assigned call id ("tool_use" id / "call_id").
	ID string `json:"id"`
	// Name is the tool name.
	Name string `json:"name"`
	// Arguments is the raw JSON arguments object as produced by the model.
	Arguments string `json:"arguments"`
}

func (ToolCallPart) partType() string { return "tool_call" }

// MarshalJSON adds the type discriminator (see TextPart.MarshalJSON).
func (p ToolCallPart) MarshalJSON() ([]byte, error) {
	type alias ToolCallPart
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{"tool_call", alias(p)})
}

// ToolResultPart is the outcome of a ToolCallPart, produced by the Agent loop
// or built manually when replaying a conversation.
type ToolResultPart struct {
	// ToolCallID matches the corresponding ToolCallPart.ID.
	ToolCallID string `json:"tool_call_id"`
	// Name is the tool name.
	Name string `json:"name"`
	// Content is the tool output as text (usually JSON).
	Content string `json:"content"`
	// IsError marks the result as a failed execution so the model can react.
	IsError bool `json:"is_error,omitempty"`
}

func (ToolResultPart) partType() string { return "tool_result" }

// MarshalJSON adds the type discriminator (see TextPart.MarshalJSON).
func (p ToolResultPart) MarshalJSON() ([]byte, error) {
	type alias ToolResultPart
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{"tool_result", alias(p)})
}

// UnmarshalPart decodes a JSON part object back into the concrete Part type.
func UnmarshalPart(data []byte) (Part, error) {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("callable: decode message part: %w", err)
	}
	unwrap := func(target any) error {
		return json.Unmarshal(data, target)
	}
	switch env.Type {
	case "text":
		var p TextPart
		return p, unwrap(&p)
	case "image":
		var p ImagePart
		return p, unwrap(&p)
	case "thinking":
		var p ThinkingPart
		return p, unwrap(&p)
	case "tool_call":
		var p ToolCallPart
		return p, unwrap(&p)
	case "tool_result":
		var p ToolResultPart
		return p, unwrap(&p)
	default:
		return nil, fmt.Errorf("callable: unknown message part type %q", env.Type)
	}
}
