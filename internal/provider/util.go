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

// extraFields returns the top-level fields of the JSON object data that are
// not listed in known, or nil when there are none. Response parsing uses it
// to preserve unmodeled fields — gateway extensions or fields the provider
// added after this library version — verbatim.
func extraFields(data []byte, known ...string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	for _, k := range known {
		delete(m, k)
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// mergeExtra overlays src onto dst (src wins per key), allocating dst when
// nil. Streaming uses it to accumulate unmodeled fields across chunks.
func mergeExtra(dst, src map[string]json.RawMessage) map[string]json.RawMessage {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]json.RawMessage{}
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// setRawField returns the JSON object raw with field overlaid as value. It
// folds streamed fragments (e.g. input_json_delta) back into the raw copy of
// an unknown content block. Invalid input is returned unchanged.
func setRawField(raw json.RawMessage, field string, value json.RawMessage) json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return raw
		}
	}
	m[field] = value
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
