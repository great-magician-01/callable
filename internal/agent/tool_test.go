package agent

import (
	"context"
	"testing"

	. "github.com/great-magician-01/callable/internal/model"
)

type weatherArgs struct {
	City string `json:"city" jsonschema:"description=City name"`
	Unit string `json:"unit,omitempty" jsonschema:"description=Temperature unit,enum=celsius,enum=fahrenheit"`
}

func TestToolSet(t *testing.T) {
	mk := func(name string) Tool {
		return NewTool(name, "", func(ctx context.Context, args struct{}) (any, error) { return nil, nil })
	}
	s := newToolSet()
	s.add(mk("a"), mk("b"))
	s.add(mk("a")) // duplicate skipped
	if s.len() != 2 {
		t.Fatalf("len = %d, want 2", s.len())
	}
	if _, ok := s.get("b"); !ok {
		t.Error("b missing")
	}
	if _, ok := s.get("c"); ok {
		t.Error("c should not exist")
	}
}
