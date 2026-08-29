package core

import (
	"context"
	"encoding/json"

	model "github.com/great-magician-01/callable/internal/model"
)

// The unified message model lives in internal/model. These aliases and thin
// wrappers keep the core code that has not been split out yet (providers,
// client, agent loop) and its tests compiling unchanged.

type (
	Role    = model.Role
	Message = model.Message

	Part           = model.Part
	TextPart       = model.TextPart
	ImagePart      = model.ImagePart
	ThinkingPart   = model.ThinkingPart
	ToolCallPart   = model.ToolCallPart
	ToolResultPart = model.ToolResultPart

	Request        = model.Request
	Response       = model.Response
	StopReason     = model.StopReason
	ToolCall       = model.ToolCall
	Usage          = model.Usage
	ResponseFormat = model.ResponseFormat

	Event               = model.Event
	MessageStartEvent   = model.MessageStartEvent
	ThinkingDeltaEvent  = model.ThinkingDeltaEvent
	TextDeltaEvent      = model.TextDeltaEvent
	ToolCallDeltaEvent  = model.ToolCallDeltaEvent
	MessageDoneEvent    = model.MessageDoneEvent
	TurnStartEvent      = model.TurnStartEvent
	TurnEndEvent        = model.TurnEndEvent
	ToolCallEvent       = model.ToolCallEvent
	ToolResultEvent     = model.ToolResultEvent
	AgentDoneEvent      = model.AgentDoneEvent
	SubAgentEvent       = model.SubAgentEvent
	SessionCompactEvent = model.SessionCompactEvent

	Effort   = model.Effort
	Thinking = model.Thinking

	ToolDefinition = model.ToolDefinition
	ToolResult     = model.ToolResult
	Tool           = model.Tool

	AgentResult = model.AgentResult

	resolvedImage = model.ResolvedImage
)

type eventSink = model.EventSink

const (
	RoleSystem    = model.RoleSystem
	RoleUser      = model.RoleUser
	RoleAssistant = model.RoleAssistant
	RoleTool      = model.RoleTool

	StopReasonEndTurn   = model.StopReasonEndTurn
	StopReasonToolCalls = model.StopReasonToolCalls
	StopReasonMaxTokens = model.StopReasonMaxTokens
	StopReasonOther     = model.StopReasonOther

	EffortOff    = model.EffortOff
	EffortLow    = model.EffortLow
	EffortMedium = model.EffortMedium
	EffortHigh   = model.EffortHigh
)

var resolveImage = model.ResolveImage

func System(text string) Message { return model.System(text) }

func User(parts ...any) Message { return model.User(parts...) }

func Assistant(parts ...any) Message { return model.Assistant(parts...) }

func ToolResults(results ...ToolResultPart) Message { return model.ToolResults(results...) }

func Text(text string) TextPart { return model.Text(text) }

func Image(ref string) ImagePart { return model.Image(ref) }

func ImageURL(url string) ImagePart { return model.ImageURL(url) }

func ImageBytes(data []byte, mediaType string) ImagePart { return model.ImageBytes(data, mediaType) }

func UnmarshalPart(data []byte) (Part, error) { return model.UnmarshalPart(data) }

func NewRequest(messages ...Message) *Request { return model.NewRequest(messages...) }

func JSONMode() ResponseFormat { return model.JSONMode() }

func JSONSchema(name string, schema map[string]any, strict bool) ResponseFormat {
	return model.JSONSchema(name, schema, strict)
}

func TextResult(content string) ToolResult { return model.TextResult(content) }

func ErrorResult(err error) ToolResult { return model.ErrorResult(err) }

func NewRawTool(name, description, parametersJSON string, fn func(ctx context.Context, rawArgs string) (any, error)) Tool {
	return model.NewRawTool(name, description, parametersJSON, fn)
}

// Generic functions cannot be aliased with a var; wrap them instead.

func NewTool[A any](name, description string, fn func(ctx context.Context, args A) (any, error)) Tool {
	return model.NewTool[A](name, description, fn)
}

func JSONSchemaFor[T any](name string, strict bool) ResponseFormat {
	return model.JSONSchemaFor[T](name, strict)
}

// Small helpers that lived in the moved model files but are still used by the
// provider adapters.

// schemaName returns the schema name providers require, defaulting blanks.
func schemaName(f *ResponseFormat) string {
	if f.Name != "" {
		return f.Name
	}
	return "output"
}

// mustJSON marshals v to a JSON string, returning "{}" on error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
