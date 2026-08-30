package model

import "testing"

func TestRequestWithHeader(t *testing.T) {
	req := NewRequest(User("hi")).
		WithHeader("X-A", "1").
		WithHeader("X-B", "2").
		WithHeader("X-A", "3")
	if len(req.Headers) != 2 || req.Headers["X-A"] != "3" || req.Headers["X-B"] != "2" {
		t.Errorf("Headers = %v, want X-A=3 (last wins) and X-B=2", req.Headers)
	}

	// A request without WithHeader keeps a nil map.
	if got := NewRequest(User("hi")).Headers; got != nil {
		t.Errorf("Headers = %v, want nil", got)
	}
}
