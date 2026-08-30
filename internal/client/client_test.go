package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/great-magician-01/callable/internal/model"
	provider "github.com/great-magician-01/callable/internal/provider"
)

func TestClientApplyDefaults(t *testing.T) {
	client := NewClient(provider.NewOpenAIProvider("k", "https://chat.example.com/v1"),
		WithModel("default-model"),
		WithMaxTokens(100),
		WithTemperature(0.3),
	)

	req := NewRequest(User("hi"))
	got := client.applyDefaults(req)
	if got.Model != "default-model" || got.MaxTokens != 100 {
		t.Errorf("defaults not applied: %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != 0.3 {
		t.Errorf("temperature = %v", got.Temperature)
	}

	// Request values win over defaults.
	req = NewRequest(User("hi")).WithModel("other").WithMaxTokens(5).WithTemperature(0.9)
	got = client.applyDefaults(req)
	if got.Model != "other" || got.MaxTokens != 5 || *got.Temperature != 0.9 {
		t.Errorf("request overrides not respected: %+v", got)
	}
	// Original request must not be mutated.
	if req.Model != "other" || req.MaxTokens != 5 {
		t.Errorf("original request mutated: %+v", req)
	}
}

func TestClientNoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"nope"}}`)
	}))
	t.Cleanup(srv.Close)

	client := NewClient(provider.NewOpenAIProvider("k", srv.URL, provider.WithRetries(2)), WithModel("m"))
	_, err := client.Create(context.Background(), NewRequest(User("hi")))
	apiErr, ok := err.(*provider.APIError)
	if !ok {
		t.Fatalf("err = %T (%v)", err, err)
	}
	if apiErr.StatusCode != 400 || apiErr.Type != "invalid_request_error" {
		t.Errorf("api error = %+v", apiErr)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 4xx)", atomic.LoadInt32(&calls))
	}
}

func TestClientContextCancelNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	client := NewClient(provider.NewOpenAIProvider("k", srv.URL, provider.WithRetries(5)), WithModel("m"))
	_, err := client.Create(ctx, NewRequest(User("hi")))
	if err == nil {
		t.Fatal("expected context error")
	}
	if atomic.LoadInt32(&calls) > 2 {
		t.Errorf("calls = %d, retries should stop when context expires", atomic.LoadInt32(&calls))
	}
}
