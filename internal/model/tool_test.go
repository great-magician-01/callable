package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type weatherArgs struct {
	City string `json:"city" jsonschema:"description=City name"`
	Unit string `json:"unit,omitempty" jsonschema:"description=Temperature unit,enum=celsius,enum=fahrenheit"`
}

func TestNewToolSchema(t *testing.T) {
	tool := NewTool("get_weather", "Get weather", func(ctx context.Context, args weatherArgs) (any, error) {
		return nil, nil
	})
	def := tool.Definition()
	if def.Name != "get_weather" || def.Description != "Get weather" {
		t.Fatalf("def = %+v", def)
	}
	params := def.Parameters
	if params["type"] != "object" {
		t.Errorf("root type = %v, want object", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing: %v", params)
	}
	city, ok := props["city"].(map[string]any)
	if !ok {
		t.Fatalf("city property missing: %v", props)
	}
	if city["type"] != "string" {
		t.Errorf("city.type = %v", city["type"])
	}
	if desc, _ := city["description"].(string); !strings.Contains(desc, "City name") {
		t.Errorf("city.description = %q", desc)
	}
	unit, _ := props["unit"].(map[string]any)
	if unit == nil {
		t.Fatalf("unit property missing")
	}
	enum, ok := unit["enum"].([]any)
	if !ok || len(enum) != 2 || enum[0] != "celsius" || enum[1] != "fahrenheit" {
		t.Errorf("unit.enum = %v, want [celsius fahrenheit]", unit["enum"])
	}
	// Fields without omitempty must be required.
	req, ok := params["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "city" {
		t.Errorf("required = %v, want [city]", params["required"])
	}
}

type nestedArgs struct {
	Name    string   `json:"name"`
	Tags    []string `json:"tags,omitempty"`
	Details struct {
		Age int `json:"age"`
	} `json:"details"`
}

func TestNewToolSchemaNested(t *testing.T) {
	tool := NewTool("nested", "", func(ctx context.Context, args nestedArgs) (any, error) {
		return nil, nil
	})
	b := mustJSON(tool.Definition().Parameters)
	for _, want := range []string{`"tags"`, `"items"`, `"details"`, `"age"`} {
		if !strings.Contains(b, want) {
			t.Errorf("schema missing %s: %s", want, b)
		}
	}
	// DoNotReference: no unresolved $ref at the top level.
	if strings.Contains(b, `"$ref"`) {
		t.Errorf("schema contains $ref (should be inlined): %s", b)
	}
}

func TestTypedToolExecute(t *testing.T) {
	var got weatherArgs
	tool := NewTool("get_weather", "", func(ctx context.Context, args weatherArgs) (any, error) {
		got = args
		return fmt.Sprintf("%s is sunny", args.City), nil
	})

	res := tool.Execute(context.Background(), `{"city":"Beijing"}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if got.City != "Beijing" {
		t.Errorf("args.City = %q", got.City)
	}
	if res.Content != "Beijing is sunny" {
		t.Errorf("content = %q", res.Content)
	}

	// Empty arguments unmarshal to the zero value.
	res = tool.Execute(context.Background(), "")
	if res.IsError {
		t.Errorf("empty args should not error: %s", res.Content)
	}

	// Invalid JSON becomes an error result for the model.
	res = tool.Execute(context.Background(), `{nope`)
	if !res.IsError {
		t.Errorf("invalid args should be an error result")
	}

	// Handler errors become error results.
	tool = NewTool("boom", "", func(ctx context.Context, args weatherArgs) (any, error) {
		return nil, errors.New("kaboom")
	})
	res = tool.Execute(context.Background(), `{}`)
	if !res.IsError || res.Content != "kaboom" {
		t.Errorf("handler error result = %+v", res)
	}

	// Struct results are JSON-encoded.
	tool = NewTool("obj", "", func(ctx context.Context, args weatherArgs) (any, error) {
		return map[string]int{"temp": 26}, nil
	})
	res = tool.Execute(context.Background(), `{}`)
	if res.Content != `{"temp":26}` {
		t.Errorf("object result = %q", res.Content)
	}
}

func TestRawTool(t *testing.T) {
	tool := NewRawTool("echo", "echo args", `{"type":"object","properties":{"x":{"type":"number"}}}`,
		func(ctx context.Context, rawArgs string) (any, error) {
			return rawArgs, nil
		})
	def := tool.Definition()
	if def.Parameters["type"] != "object" {
		t.Errorf("parameters = %v", def.Parameters)
	}
	res := tool.Execute(context.Background(), `{"x":1}`)
	if res.Content != `{"x":1}` {
		t.Errorf("content = %q", res.Content)
	}
}
