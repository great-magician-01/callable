package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/great-magician-01/callable/internal/model"
)

// withFastBackoff swaps the retry backoff schedule for near-instant waits so
// tests that expect retries to complete stay fast.
func withFastBackoff(t *testing.T) {
	t.Helper()
	orig := backoffDelays
	backoffDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { backoffDelays = orig })
}

func TestBackoffDelay(t *testing.T) {
	// Default schedule: wait 3s, 10s, 30s before retries 1..3; attempts beyond
	// the schedule reuse the last delay.
	want := []time.Duration{3 * time.Second, 10 * time.Second, 30 * time.Second}
	if len(backoffDelays) != len(want) {
		t.Fatalf("backoffDelays = %v, want %v", backoffDelays, want)
	}
	for i, w := range want {
		if backoffDelays[i] != w {
			t.Errorf("backoffDelays[%d] = %v, want %v", i, backoffDelays[i], w)
		}
		if got := backoffDelay(nil, i+1); got != w {
			t.Errorf("backoffDelay(nil, %d) = %v, want %v", i+1, got, w)
		}
	}
	if got := backoffDelay(nil, len(want)+5); got != want[len(want)-1] {
		t.Errorf("backoffDelay beyond schedule = %v, want %v (last delay)", got, want[len(want)-1])
	}
	// A per-provider schedule (WithRetryBackoff) overrides the default.
	custom := []time.Duration{50 * time.Millisecond}
	if got := backoffDelay(custom, 1); got != custom[0] {
		t.Errorf("backoffDelay(custom, 1) = %v, want %v", got, custom[0])
	}
	if got := backoffDelay(custom, 4); got != custom[0] {
		t.Errorf("backoffDelay(custom, 4) = %v, want %v (last delay)", got, custom[0])
	}
}

func TestClientRetryOn500(t *testing.T) {
	withFastBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider("k", srv.URL, WithRetries(2))
	resp, err := p.Create(context.Background(), NewRequest(User("hi")).WithModel("m"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("text = %q", resp.Text)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("calls = %d, want 3", atomic.LoadInt32(&calls))
	}
}

func TestClientDefaultRetries(t *testing.T) {
	withFastBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	t.Cleanup(srv.Close)

	// No WithRetries: the default is 3 retries (4 attempts total).
	p := NewOpenAIProvider("k", srv.URL)
	_, err := p.Create(context.Background(), NewRequest(User("hi")).WithModel("m"))
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Errorf("calls = %d, want 4 (1 + 3 default retries)", got)
	}
}

func TestClientRetriesDisabled(t *testing.T) {
	withFastBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider("k", srv.URL, WithRetries(0))
	_, err := p.Create(context.Background(), NewRequest(User("hi")).WithModel("m"))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (retries disabled)", got)
	}
}

// TestRequestHeadersPassThrough verifies request-level Request.Headers are
// sent on the wire: they are applied after provider-level WithHeader (request
// wins on conflicts) and after authentication (they can override auth).
func TestRequestHeadersPassThrough(t *testing.T) {
	var got []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider("k", srv.URL,
		WithHeader("X-Team", "blue"),
		WithHeader("X-Shared", "provider"))
	req := NewRequest(User("hi")).WithModel("m").
		WithHeader("X-Request-Id", "r-1").
		WithHeader("X-Shared", "request")
	if _, err := p.Create(context.Background(), req); err != nil {
		t.Fatalf("create: %v", err)
	}
	h := got[0]
	if v := h.Get("X-Request-Id"); v != "r-1" {
		t.Errorf("X-Request-Id = %q, want r-1", v)
	}
	if v := h.Get("X-Team"); v != "blue" {
		t.Errorf("X-Team = %q, want provider-level blue", v)
	}
	if v := h.Get("X-Shared"); v != "request" {
		t.Errorf("X-Shared = %q, want request-level value to win", v)
	}
	if v := h.Get("Authorization"); v != "Bearer k" {
		t.Errorf("Authorization = %q, want Bearer k", v)
	}

	// A request-level header may deliberately override authentication.
	if _, err := p.Create(context.Background(),
		NewRequest(User("hi")).WithModel("m").WithHeader("Authorization", "Bearer other")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if v := got[1].Get("Authorization"); v != "Bearer other" {
		t.Errorf("Authorization = %q, want request-level override", v)
	}
}

// TestStreamRequestHeaders verifies headers also pass through on streaming
// requests, while the SSE Accept header is still enforced last.
func TestStreamRequestHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider("k", srv.URL)
	_, err := p.Stream(context.Background(),
		NewRequest(User("hi")).WithModel("m").WithHeader("X-Request-Id", "r-2"),
		func(Event) {})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if v := got.Get("X-Request-Id"); v != "r-2" {
		t.Errorf("X-Request-Id = %q, want r-2", v)
	}
	if v := got.Get("Accept"); v != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", v)
	}
}

func TestScanSSE(t *testing.T) {
	input := "event: one\n" +
		"data: {\"a\":1}\n" +
		"\n" +
		": keep-alive comment\n" +
		"\n" +
		"event: two\n" +
		"data: line1\n" +
		"data: line2\n" +
		"\r\n" +
		"data: trailing\n" +
		"\n"

	var got []sseMessage
	err := scanSSE(strings.NewReader(input), func(m sseMessage) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("events = %d: %+v", len(got), got)
	}
	if got[0].event != "one" || got[0].data != `{"a":1}` {
		t.Errorf("event 0 = %+v", got[0])
	}
	if got[1].event != "two" || got[1].data != "line1\nline2" {
		t.Errorf("event 1 = %+v", got[1])
	}
	if got[2].event != "" || got[2].data != "trailing" {
		t.Errorf("event 2 = %+v", got[2])
	}
}

func TestScanSSEAbort(t *testing.T) {
	input := "data: a\n\ndata: b\n\ndata: c\n\n"
	var count int
	err := scanSSE(strings.NewReader(input), func(m sseMessage) error {
		count++
		if count == 2 {
			return errStopScan
		}
		return nil
	})
	if err != errStopScan {
		t.Fatalf("err = %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d", count)
	}
}

func TestDetectCompat(t *testing.T) {
	cases := map[string]Compat{
		"https://api.openai.com/v1":                         CompatNone,
		"https://open.bigmodel.cn/api/paas/v4":              CompatGLM,
		"https://api.z.ai/api/paas/v4":                      CompatGLM,
		"https://ark.cn-beijing.volces.com/api/v3":          CompatArk,
		"https://dashscope.aliyuncs.com/compatible-mode/v1": CompatQwen,
		"https://api.deepseek.com":                          CompatDeepSeek,
	}
	for base, want := range cases {
		if got := detectCompat(base); got != want {
			t.Errorf("detectCompat(%q) = %v, want %v", base, got, want)
		}
	}
}

func TestAPIErrorIsRetryable(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{0, true}, {429, true}, {500, true}, {503, true},
		{400, false}, {401, false}, {404, false},
	}
	for _, c := range cases {
		e := &APIError{StatusCode: c.status}
		if got := e.IsRetryable(); got != c.want {
			t.Errorf("IsRetryable(%d) = %v", c.status, got)
		}
	}
}

// TestWithRetryBackoffEmpty verifies an empty schedule keeps the default.
func TestWithRetryBackoffEmpty(t *testing.T) {
	p := NewOpenAIProvider("k", "https://api.example.com", WithRetryBackoff())
	if p.api.cfg.backoff != nil {
		t.Errorf("backoff = %v, want nil (default schedule)", p.api.cfg.backoff)
	}
}

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
