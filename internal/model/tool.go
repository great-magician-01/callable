package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
)

// ToolDefinition is the schema a provider advertises to the model.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Parameters is a JSON Schema object describing the arguments. nil means
	// "no parameters".
	Parameters map[string]any `json:"parameters,omitempty"`
}

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	// Content is the output shown to the model, usually JSON or plain text.
	Content string
	// IsError tells the model the execution failed so it can adjust.
	IsError bool
}

// TextResult wraps a plain-text tool output.
func TextResult(content string) ToolResult {
	return ToolResult{Content: content}
}

// ErrorResult wraps an error as a failed tool result.
func ErrorResult(err error) ToolResult {
	return ToolResult{Content: err.Error(), IsError: true}
}

// Tool is a callable exposed to the model. Build one with NewTool (arguments
// struct reflected into a JSON Schema automatically) or NewRawTool (manual
// schema).
type Tool interface {
	// Definition returns the schema advertised to the model.
	Definition() ToolDefinition
	// Execute runs the tool with the raw JSON arguments produced by the model.
	// Returning a ToolResult with IsError=true feeds the error back to the
	// model instead of aborting the agent loop.
	Execute(ctx context.Context, rawArgs string) ToolResult
}

// NewTool creates a Tool from a handler whose arguments struct is reflected
// into a JSON Schema. Describe parameters with struct tags:
//
//	type WeatherArgs struct {
//	    City string `json:"city" jsonschema:"description=City name, e.g. Beijing"`
//	    Unit string `json:"unit,omitempty" jsonschema:"description=Temperature unit,enum=celsius,enum=fahrenheit"`
//	}
//
// Fields without "omitempty" are required. The handler may return a string, a
// ToolResult, nil, or any value (JSON-encoded). A returned error becomes an
// IsError tool result shown to the model.
func NewTool[A any](name, description string, fn func(ctx context.Context, args A) (any, error)) Tool {
	return &typedTool[A]{
		def: ToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  schemaFor[A](),
		},
		fn: fn,
	}
}

type typedTool[A any] struct {
	def ToolDefinition
	fn  func(ctx context.Context, args A) (any, error)
}

func (t *typedTool[A]) Definition() ToolDefinition { return t.def }

func (t *typedTool[A]) Execute(ctx context.Context, rawArgs string) ToolResult {
	var args A
	if s := strings.TrimSpace(rawArgs); s != "" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return ErrorResult(fmt.Errorf("invalid arguments for tool %q: %v (expected schema: %s)",
				t.def.Name, err, mustJSON(t.def.Parameters)))
		}
	}
	out, err := t.fn(ctx, args)
	if err != nil {
		return ErrorResult(err)
	}
	return coerceToolOutput(out)
}

// NewRawTool creates a Tool with a hand-written JSON Schema and a handler
// that receives the raw JSON arguments string.
func NewRawTool(name, description, parametersJSON string, fn func(ctx context.Context, rawArgs string) (any, error)) Tool {
	var params map[string]any
	if s := strings.TrimSpace(parametersJSON); s != "" {
		if err := json.Unmarshal([]byte(s), &params); err != nil {
			panic(fmt.Sprintf("callable: NewRawTool(%q): invalid parameters JSON: %v", name, err))
		}
	}
	return &rawTool{
		def: ToolDefinition{Name: name, Description: description, Parameters: params},
		fn:  fn,
	}
}

type rawTool struct {
	def ToolDefinition
	fn  func(ctx context.Context, rawArgs string) (any, error)
}

func (t *rawTool) Definition() ToolDefinition { return t.def }

func (t *rawTool) Execute(ctx context.Context, rawArgs string) ToolResult {
	out, err := t.fn(ctx, rawArgs)
	if err != nil {
		return ErrorResult(err)
	}
	return coerceToolOutput(out)
}

// coerceToolOutput normalizes handler return values into a ToolResult.
func coerceToolOutput(out any) ToolResult {
	switch v := out.(type) {
	case nil:
		return ToolResult{}
	case string:
		return ToolResult{Content: v}
	case ToolResult:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ErrorResult(fmt.Errorf("marshal tool result: %w", err))
		}
		return ToolResult{Content: string(b)}
	}
}

// schemaFor reflects a Go type into an inlined JSON Schema object.
func schemaFor[A any]() map[string]any {
	reflector := &jsonschema.Reflector{
		// Inline all $refs: providers dislike external references in tool
		// parameter schemas.
		DoNotReference: true,
	}
	schema := reflector.Reflect(new(A))
	b, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("callable: reflect JSON schema for %T: %v", new(A), err))
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic(fmt.Sprintf("callable: decode reflected JSON schema for %T: %v", new(A), err))
	}
	// The root of a tool schema is always an object; providers reject other
	// shapes.
	if m == nil {
		m = map[string]any{"type": "object"}
	}
	return m
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
