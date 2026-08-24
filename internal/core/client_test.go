package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientApplyDefaults(t *testing.T) {
	client := NewClient(NewOpenAIProvider("k"),
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

func TestClientRetryOn500(t *testing.T) {
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

	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL), WithRetries(2)), WithModel("m"))
	resp, err := client.Create(context.Background(), NewRequest(User("hi")))
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

func TestClientNoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"nope"}}`)
	}))
	t.Cleanup(srv.Close)

	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL), WithRetries(2)), WithModel("m"))
	_, err := client.Create(context.Background(), NewRequest(User("hi")))
	apiErr, ok := err.(*APIError)
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
	client := NewClient(NewOpenAIProvider("k", WithBaseURL(srv.URL), WithRetries(5)), WithModel("m"))
	_, err := client.Create(ctx, NewRequest(User("hi")))
	if err == nil {
		t.Fatal("expected context error")
	}
	if atomic.LoadInt32(&calls) > 2 {
		t.Errorf("calls = %d, retries should stop when context expires", atomic.LoadInt32(&calls))
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
		"https://ark.cn-beijing.volces.com/api/v3":          CompatGLM,
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
