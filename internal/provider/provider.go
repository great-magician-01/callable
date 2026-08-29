// Package provider implements the three built-in provider adapters (OpenAI
// Chat Completions, OpenAI Responses, Anthropic Messages) behind the unified
// Provider interface, plus the shared HTTP/SSE/retry infrastructure and
// well-known endpoint constants. The message model lives in internal/model.
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	model "github.com/great-magician-01/callable/internal/model"
)

// Provider converts unified requests to a specific wire format and parses
// responses back. The three built-in implementations are NewOpenAIProvider
// (Chat Completions), NewOpenAIResponsesProvider (Responses) and
// NewAnthropicProvider (Anthropic Messages).
type Provider interface {
	// Name identifies the provider; it keys Message provider round-trip data.
	Name() string
	// Create performs a non-streaming request.
	Create(ctx context.Context, req *model.Request) (*model.Response, error)
	// Stream performs a streaming request, invoking onEvent for every event
	// and returning the assembled response at the end. If the context is
	// canceled mid-stream, Stream returns the partially assembled response
	// together with the context error (errors.Is matches context.Canceled or
	// context.DeadlineExceeded); the upstream connection is closed
	// immediately, which stops the server from generating further tokens.
	Stream(ctx context.Context, req *model.Request, onEvent model.EventSink) (*model.Response, error)
}

// Compat is a bitmask of OpenAI-compatible endpoint dialects. Third-party
// endpoints expose thinking controls with non-standard fields; the right bits
// are auto-detected from the base URL and can be overridden with WithCompat.
type Compat uint

const (
	CompatNone     Compat = 0
	CompatGLM      Compat = 1 << 0 // thinking:{type:"enabled"} + reasoning_effort (medium→high) — GLM/Zhipu, Z.AI
	CompatQwen     Compat = 1 << 1 // enable_thinking:true + thinking_budget — Alibaba DashScope
	CompatDeepSeek Compat = 1 << 2 // thinking:{type:"enabled"} + reasoning_effort; reasoning_content output — DeepSeek
	CompatArk      Compat = 1 << 3 // thinking:{type:"enabled"} + reasoning_effort (direct) — Volcano Ark
)

// detectCompat guesses the endpoint dialect from a base URL.
func detectCompat(baseURL string) Compat {
	host := ""
	if parsed, err := url.Parse(baseURL); err == nil {
		host = strings.ToLower(parsed.Host)
	}
	var c Compat
	switch {
	case strings.Contains(host, "bigmodel.cn"),
		strings.Contains(host, "zhipuai"),
		host == "z.ai" || strings.HasSuffix(host, ".z.ai"):
		c |= CompatGLM
	case strings.Contains(host, "volces.com"):
		c |= CompatArk
	case strings.Contains(host, "dashscope"):
		c |= CompatQwen
	case strings.Contains(host, "deepseek"):
		c |= CompatDeepSeek
	}
	return c
}

// ProviderOption configures a provider (base URL, HTTP client, retries,
// endpoint compatibility).
type ProviderOption func(*providerConfig)

type providerConfig struct {
	baseURL    string
	httpClient *http.Client
	headers    map[string]string
	maxRetries int
	backoff    []time.Duration // nil = backoffDelays
	compat     Compat
}

// defaultProviderConfig builds the base config; baseURL is always supplied
// explicitly by the provider constructor (the library ships no default
// endpoint). Trailing slashes are trimmed so base + endpoint concatenate
// cleanly.
func defaultProviderConfig(baseURL string) providerConfig {
	return providerConfig{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{},
		// No global timeout: long streams must not be cut off. Use the
		// context for cancellation.
		maxRetries: 3,
	}
}

// WithHTTPClient supplies a custom *http.Client.
func WithHTTPClient(client *http.Client) ProviderOption {
	return func(c *providerConfig) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithHeader adds a header to every request (applied after authentication).
func WithHeader(key, value string) ProviderOption {
	return func(c *providerConfig) {
		if c.headers == nil {
			c.headers = map[string]string{}
		}
		c.headers[key] = value
	}
}

// WithRetries sets how many times transient failures (transport errors, 429,
// 5xx) are retried. Retries wait 3s, 10s, then 30s between attempts (attempts
// beyond the schedule reuse the last delay). Default 3; pass 0 to disable.
func WithRetries(n int) ProviderOption {
	return func(c *providerConfig) {
		if n < 0 {
			n = 0
		}
		c.maxRetries = n
	}
}

// WithCompat overrides the auto-detected OpenAI-compatible endpoint dialect.
func WithCompat(c Compat) ProviderOption {
	return func(cfg *providerConfig) { cfg.compat = c }
}

// WithRetryBackoff replaces the default retry wait schedule (3s, 10s, 30s):
// delays[i] is the wait before retry i+1, and attempts beyond the schedule
// reuse the last delay. Ignored when empty; combine with WithRetries to also
// change the attempt count.
func WithRetryBackoff(delays ...time.Duration) ProviderOption {
	return func(cfg *providerConfig) {
		if len(delays) > 0 {
			cfg.backoff = append([]time.Duration{}, delays...)
		}
	}
}

// httpAPI is the shared transport used by all providers: JSON POSTs with
// retry/backoff, header decoration, and SSE streaming.
type httpAPI struct {
	name     string
	cfg      providerConfig
	decorate func(*http.Request)
}

func (a *httpAPI) newRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if a.decorate != nil {
		a.decorate(req)
	}
	for k, v := range a.cfg.headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// postJSON sends a JSON payload and returns the response status and body.
// Non-2xx responses are returned as *APIError after exhausting retries.
func (a *httpAPI) postJSON(ctx context.Context, endpoint string, payload []byte) ([]byte, error) {
	resp, err := a.post(ctx, endpoint, payload, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			// Canceled or timed out while reading: surface the context
			// error, not the transport detail.
			return nil, ctx.Err()
		}
		return nil, a.transportError(fmt.Errorf("read response: %w", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newAPIError(a.name, resp.StatusCode, body)
	}
	return body, nil
}

// postJSONStream sends a JSON payload and, on success, returns the response
// body for incremental SSE reading.
func (a *httpAPI) postJSONStream(ctx context.Context, endpoint string, payload []byte) (io.ReadCloser, error) {
	resp, err := a.post(ctx, endpoint, payload, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, newAPIError(a.name, resp.StatusCode, body)
	}
	return resp.Body, nil
}

func (a *httpAPI) post(ctx context.Context, endpoint string, payload []byte, stream bool) (*http.Response, error) {
	fullURL := a.cfg.baseURL + endpoint
	attempts := a.cfg.maxRetries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := a.sleepBackoff(ctx, attempt); err != nil {
				return nil, err
			}
		}
		req, err := a.newRequest(ctx, http.MethodPost, fullURL, payload)
		if err != nil {
			return nil, err
		}
		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}
		resp, err := a.cfg.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = a.transportError(err)
			continue
		}
		// Retry transient HTTP failures; the response body of the failed
		// attempt is discarded without reading.
		if isRetryableStatus(resp.StatusCode) && attempt < attempts-1 {
			resp.Body.Close()
			lastErr = newAPIError(a.name, resp.StatusCode, nil)
			continue
		}
		// Hand the response (stream or not) to the caller; anything
		// non-retryable, including non-2xx, is turned into an APIError there.
		return resp, nil
	}
	return nil, lastErr
}

func isRetryableStatus(code int) bool {
	return code == 429 || code >= 500
}

// transportError normalizes network failures into *APIError with status 0.
func (a *httpAPI) transportError(err error) *APIError {
	return &APIError{Provider: a.name, Message: err.Error()}
}

// backoffDelays is the default wait schedule before each retry: 3s before the
// first retry, then 10s, then 30s. Attempts beyond the schedule reuse the
// last delay. It is a package-level variable so tests can swap in a fast
// schedule; per-provider schedules are configured with WithRetryBackoff.
var backoffDelays = []time.Duration{3 * time.Second, 10 * time.Second, 30 * time.Second}

// backoffDelay returns how long to wait before retry attempt (1-based),
// preferring the provider's configured schedule over backoffDelays.
func backoffDelay(schedule []time.Duration, attempt int) time.Duration {
	if len(schedule) == 0 {
		schedule = backoffDelays
	}
	i := attempt - 1
	if i >= len(schedule) {
		i = len(schedule) - 1
	}
	return schedule[i]
}

func (a *httpAPI) sleepBackoff(ctx context.Context, attempt int) error {
	timer := time.NewTimer(backoffDelay(a.cfg.backoff, attempt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// sseMessage is one parsed server-sent event.
type sseMessage struct {
	event string
	data  string
}

// scanSSE reads a text/event-stream body and invokes fn per event. fn may
// return an error to abort the scan.
func scanSSE(r io.Reader, fn func(sseMessage) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var event string
	var dataLines []string
	flush := func() error {
		if event == "" && len(dataLines) == 0 {
			return nil
		}
		err := fn(sseMessage{event: event, data: strings.Join(dataLines, "\n")})
		event = ""
		dataLines = dataLines[:0]
		return err
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ") // SSE strips one leading space
			dataLines = append(dataLines, data)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

// isOfficialOpenAI reports whether the base URL points at api.openai.com, in
// which case newer parameter names (max_completion_tokens,
// stream_options.include_usage) are used.
func isOfficialOpenAI(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, "api.openai.com")
}

// mergeExtraJSON marshals payload and overlays req.Extra on top of the
// resulting object.
func mergeExtraJSON(payload any, extra map[string]any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return b, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("callable: merge extra fields: %w", err)
	}
	for k, v := range extra {
		m[k] = v
	}
	return json.Marshal(m)
}

// splitSystem separates system messages from the conversation. Multiple
// system messages are joined with blank lines.
func splitSystem(messages []model.Message) (string, []model.Message) {
	var systemTexts []string
	var rest []model.Message
	for _, m := range messages {
		if m.Role == model.RoleSystem {
			systemTexts = append(systemTexts, m.Text())
			continue
		}
		rest = append(rest, m)
	}
	return strings.Join(systemTexts, "\n\n"), rest
}

// bearerAuth returns a decorator setting the Authorization header.
func bearerAuth(apiKey string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

// compactJSON canonicalizes a JSON string (removes insignificant whitespace).
// Invalid input is returned unchanged.
func compactJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}
