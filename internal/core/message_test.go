package core

import "testing"

func TestSplitSystem(t *testing.T) {
	sys, rest := splitSystem([]Message{
		System("first"),
		User("hello"),
		System("second"),
		Assistant("hi"),
	})
	if sys != "first\n\nsecond" {
		t.Errorf("system = %q", sys)
	}
	if len(rest) != 2 {
		t.Fatalf("rest = %d, want 2", len(rest))
	}
	if rest[0].Role != RoleUser || rest[1].Role != RoleAssistant {
		t.Errorf("roles = %v, %v", rest[0].Role, rest[1].Role)
	}
}
