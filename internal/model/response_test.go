package model

import (
	"encoding/json"
	"testing"
)

func TestUsageAddExtra(t *testing.T) {
	var u Usage
	u.Add(Usage{
		InputTokens: 1,
		Extra:       map[string]json.RawMessage{"total_tokens": json.RawMessage(`3`)},
	})
	u.Add(Usage{
		InputTokens: 2,
		Extra: map[string]json.RawMessage{
			"total_tokens": json.RawMessage(`9`),
			"cost":         json.RawMessage(`0.1`),
		},
	})
	if u.InputTokens != 3 {
		t.Errorf("input tokens = %d, want 3", u.InputTokens)
	}
	// Unmodeled fields merge; the later turn wins per key.
	if string(u.Extra["total_tokens"]) != `9` {
		t.Errorf("total_tokens = %s, want 9 (later turn wins)", u.Extra["total_tokens"])
	}
	if string(u.Extra["cost"]) != `0.1` {
		t.Errorf("cost = %s", u.Extra["cost"])
	}
}
