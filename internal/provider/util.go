package provider

import (
	"encoding/json"

	model "github.com/great-magician-01/callable/internal/model"
)

// Small helpers that lived in the model split but are used by the provider
// adapters.

// schemaName returns the schema name providers require, defaulting blanks.
func schemaName(f *model.ResponseFormat) string {
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
