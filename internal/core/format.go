package core

// ResponseFormat constrains the model's output format (structured output).
// Set it on a request with Request.WithResponseFormat, or as a client default
// with WithResponseFormat.
//
// The format is mapped to each provider's native control:
//
//   - OpenAI Chat Completions: response_format (json_object / json_schema).
//     DeepSeek and GLM/Z.AI endpoints accept only json_object, so schema mode
//     is automatically downgraded there with the schema spelled out in the
//     prompt (Compat dialects are auto-detected from the base URL).
//   - OpenAI Responses: text.format (json_object / json_schema)
//   - Anthropic Messages: output_config.format (json_schema; a free-form JSON
//     request becomes a permissive {"type":"object"} schema)
//
// Schema-capable providers reject unsupported JSON Schema keywords with an
// APIError (Anthropic accepts only a strict subset: no minimum/maximum,
// minLength, etc.).
type ResponseFormat struct {
	// Name identifies the schema. Some providers require it; an empty Name
	// defaults to "output".
	Name string
	// Schema constrains the output JSON. nil requests free-form JSON mode
	// ("give me JSON, any shape").
	Schema map[string]any
	// Strict asks providers supporting it to guarantee schema compliance.
	Strict bool
}

// JSONMode requests free-form JSON output: the model answers with a JSON
// value of any shape.
func JSONMode() ResponseFormat { return ResponseFormat{} }

// JSONSchema requests output conforming to the given JSON Schema. Describe
// the schema as a decoded JSON object (see JSONSchemaFor to reflect a Go
// struct instead).
func JSONSchema(name string, schema map[string]any, strict bool) ResponseFormat {
	return ResponseFormat{Name: name, Schema: schema, Strict: strict}
}

// JSONSchemaFor requests output conforming to the JSON Schema reflected from
// the Go type T, using the same reflection (and `jsonschema` struct tags) as
// NewTool:
//
//	type Recipe struct {
//	    Name  string   `json:"name" jsonschema:"description=Dish name"`
//	    Steps []string `json:"steps"`
//	}
//
//	resp, err := client.Create(ctx, callable.NewRequest(
//	    callable.User("Give me a pancake recipe"),
//	).WithResponseFormat(callable.JSONSchemaFor[Recipe]("recipe", true)))
//	var recipe Recipe
//	err = resp.DecodeJSON(&recipe)
func JSONSchemaFor[T any](name string, strict bool) ResponseFormat {
	return ResponseFormat{Name: name, Schema: schemaFor[T](), Strict: strict}
}

// schemaName returns the schema name providers require, defaulting blanks.
func (f ResponseFormat) schemaName() string {
	if f.Name != "" {
		return f.Name
	}
	return "output"
}
