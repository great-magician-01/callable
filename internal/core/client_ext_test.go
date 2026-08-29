package core

import (
	"context"
	"errors"
	. "github.com/great-magician-01/callable/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestClientHooksAndDefaults verifies request/response hooks observe the
// defaulted request and the provider result, and that the new defaults land
// on the wire.
func TestClientHooksAndDefaults(t *testing.T) {
	var bodies []string
	server := NewMockJSONServer(t, []string{ChatJSON("ok"), ChatJSON("ok2")}, &bodies)

	var hookReq *Request
	var hookResp *Response
	var hookErr error
	client := NewClient(NewOpenAIProvider("k", server.URL),
		WithModel("m"),
		WithTopP(0.3),
		WithStopSequences("END"),
		WithResponseFormat(JSONMode()),
		WithRequestHook(func(ctx context.Context, req *Request) { hookReq = req }),
		WithResponseHook(func(ctx context.Context, req *Request, resp *Response, err error) {
			hookResp, hookErr = resp, err
		}),
	)

	resp, err := client.Create(context.Background(), NewRequest(User("hi")))
	if err != nil {
		t.Fatal(err)
	}
	if hookReq == nil || hookReq.Model != "m" || hookReq.TopP == nil || *hookReq.TopP != 0.3 {
		t.Errorf("request hook saw %+v", hookReq)
	}
	if hookResp != resp || hookErr != nil {
		t.Errorf("response hook saw resp=%p err=%v", hookResp, hookErr)
	}
	m := DecodeMap(t, []byte(bodies[0]))
	if got := AsFloat(t, m["top_p"]); got != 0.3 {
		t.Errorf("top_p = %v", got)
	}
	if got := AsSlice(t, m["stop"]); len(got) != 1 || got[0] != "END" {
		t.Errorf("stop = %v", got)
	}
	if got := AsString(t, AsMap(t, m["response_format"])["type"]); got != "json_object" {
		t.Errorf("response_format.type = %q", got)
	}

	// Request-level values win over client defaults; hooks fire on Stream too.
	hookReq = nil
	_, err = client.Stream(context.Background(),
		NewRequest(User("hi")).WithTopP(0.9), noopEvents)
	if err != nil {
		t.Fatal(err)
	}
	if hookReq == nil || hookReq.TopP == nil || *hookReq.TopP != 0.9 {
		t.Errorf("stream request hook saw %+v", hookReq)
	}
	m2 := DecodeMap(t, []byte(bodies[1]))
	if got := AsFloat(t, m2["top_p"]); got != 0.9 {
		t.Errorf("stream top_p = %v, want 0.9 (request overrides default)", got)
	}
}

// TestClientResponseHookSeesErrors verifies the response hook fires with the
// APIError on a failed call.
func TestClientResponseHookSeesErrors(t *testing.T) {
	server := NewMockJSONServer(t, nil, nil) // every request gets a 400
	var hookErr error
	client := NewClient(NewOpenAIProvider("k", server.URL, WithRetries(0)),
		WithModel("m"),
		WithResponseHook(func(ctx context.Context, req *Request, resp *Response, err error) {
			hookErr = err
		}),
	)
	_, err := client.Create(context.Background(), NewRequest(User("hi")))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want APIError", err)
	}
	if hookErr == nil || !errors.As(hookErr, &apiErr) {
		t.Errorf("response hook err = %v, want the APIError", hookErr)
	}
}

// TestConvenienceClients verifies the one-call constructors wire provider,
// endpoint and model correctly.
func TestConvenienceClients(t *testing.T) {
	ctx := context.Background()

	var chatBodies []string
	chatSrv := NewMockJSONServer(t, []string{ChatJSON("ok")}, &chatBodies)
	if _, err := NewOpenAIClient("k", chatSrv.URL, "gpt-x").Create(ctx, NewRequest(User("hi"))); err != nil {
		t.Fatal(err)
	}
	m := DecodeMap(t, []byte(chatBodies[0]))
	if got := AsString(t, m["model"]); got != "gpt-x" {
		t.Errorf("model = %q", got)
	}

	var antBodies []string
	antSrv := NewMockJSONServer(t, []string{
		`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
	}, &antBodies)
	if _, err := NewAnthropicClient("k", antSrv.URL, "claude-x").Create(ctx, NewRequest(User("hi"))); err != nil {
		t.Fatal(err)
	}
	if m := DecodeMap(t, []byte(antBodies[0])); AsString(t, m["model"]) != "claude-x" {
		t.Errorf("model = %q", m["model"])
	}

	var respBodies []string
	respSrv := NewMockJSONServer(t, []string{
		`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`,
	}, &respBodies)
	if _, err := NewOpenAIResponsesClient("k", respSrv.URL, "gpt-x").Create(ctx, NewRequest(User("hi"))); err != nil {
		t.Fatal(err)
	}
	if m := DecodeMap(t, []byte(respBodies[0])); AsString(t, m["model"]) != "gpt-x" {
		t.Errorf("model = %q", m["model"])
	}
}

// TestWithRetryBackoff verifies a custom backoff schedule replaces the
// default one.
func TestWithRetryBackoff(t *testing.T) {
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

	start := time.Now()
	client := NewClient(
		NewOpenAIProvider("k", srv.URL, WithRetries(2), WithRetryBackoff(time.Millisecond)),
		WithModel("m"),
	)
	if _, err := client.Create(context.Background(), NewRequest(User("hi"))); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures + 1 success)", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("retries took %v; the custom 1ms backoff was not applied", elapsed)
	}
}

// TestClientDefaultOverrides verifies request-level values win over client
// defaults for the new defaultable fields, and WithStopSequences() with no
// arguments clears the client default.
func TestClientDefaultOverrides(t *testing.T) {
	var bodies []string
	server := NewMockJSONServer(t, []string{ChatJSON("a"), ChatJSON("b")}, &bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL),
		WithModel("m"),
		WithStopSequences("CLIENT"),
		WithResponseFormat(JSONMode()),
	)

	// Request-level format wins over the client default.
	schema := map[string]any{"type": "object"}
	_, err := client.Create(context.Background(), NewRequest(User("hi")).
		WithResponseFormat(JSONSchema("recipe", schema, false)))
	if err != nil {
		t.Fatal(err)
	}
	m := DecodeMap(t, []byte(bodies[0]))
	if got := AsString(t, AsMap(t, m["response_format"])["type"]); got != "json_schema" {
		t.Errorf("response_format.type = %q, want json_schema (request overrides client)", got)
	}
	if got := AsSlice(t, m["stop"]); len(got) != 1 || got[0] != "CLIENT" {
		t.Errorf("stop = %v, want client default", got)
	}

	// WithStopSequences() explicitly clears the client default.
	_, err = client.Create(context.Background(), NewRequest(User("hi")).WithStopSequences())
	if err != nil {
		t.Fatal(err)
	}
	m2 := DecodeMap(t, []byte(bodies[1]))
	if _, ok := m2["stop"]; ok {
		t.Errorf("stop must be absent after explicit clear: %v", m2["stop"])
	}
}

// TestClientExtraMerge verifies client-level WithExtra lands on the wire and
// request-level WithExtra wins on conflicts, without mutating the caller's
// request.
func TestClientExtraMerge(t *testing.T) {
	var bodies []string
	server := NewMockJSONServer(t, []string{ChatJSON("a"), ChatJSON("b")}, &bodies)
	client := NewClient(NewOpenAIProvider("k", server.URL),
		WithModel("m"),
		WithExtra("gateway_flag", true),
		WithExtra("shared", "client"),
	)

	req := NewRequest(User("hi"))
	if _, err := client.Create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	m := DecodeMap(t, []byte(bodies[0]))
	if got := m["gateway_flag"]; got != true {
		t.Errorf("gateway_flag = %v", got)
	}
	if got := m["shared"]; got != "client" {
		t.Errorf("shared = %v", got)
	}
	if req.Extra != nil {
		t.Errorf("caller request mutated: %v", req.Extra)
	}

	// Request-level wins on conflicts.
	if _, err := client.Create(context.Background(), NewRequest(User("hi")).WithExtra("shared", "request")); err != nil {
		t.Fatal(err)
	}
	if got := DecodeMap(t, []byte(bodies[1]))["shared"]; got != "request" {
		t.Errorf("shared = %v, want request-level value", got)
	}
}

// TestWithRetryBackoffEmpty verifies an empty schedule keeps the default.
func TestWithRetryBackoffEmpty(t *testing.T) {
	p := NewOpenAIProvider("k", "https://api.example.com", WithRetryBackoff())
	if p.api.cfg.backoff != nil {
		t.Errorf("backoff = %v, want nil (default schedule)", p.api.cfg.backoff)
	}
}

func TestEventConversationIDHelperCoversAllEvents(t *testing.T) {
	events := []Event{
		MessageStartEvent{}, ThinkingDeltaEvent{}, TextDeltaEvent{},
		ToolCallDeltaEvent{}, MessageDoneEvent{}, TurnStartEvent{}, TurnEndEvent{},
		ToolCallEvent{}, ToolResultEvent{}, AgentDoneEvent{}, SubAgentEvent{},
		SessionCompactEvent{},
	}
	for _, ev := range events {
		stamped := withConversationID(ev, "id-1")
		if got := eventConversationID(stamped); got != "id-1" {
			t.Errorf("%T: conversation id = %q", ev, got)
		}
	}
}
