package core

import "context"

// Client sends unified requests through a Provider, filling in defaults.
// Create and Stream are the low-level single-turn entry points; for the
// automatic tool-calling loop use Agent.
type Client struct {
	provider Provider

	model       string
	maxTokens   int
	temperature *float64
	topP        *float64
	stop        []string
	format      *ResponseFormat
	extra       map[string]any

	requestHooks  []RequestHook
	responseHooks []ResponseHook
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// RequestHook observes every request right before it is sent to the provider
// (after client defaults have been applied). Use it for logging, tracing or
// metrics. Hooks must not mutate the request.
type RequestHook func(ctx context.Context, req *Request)

// ResponseHook observes every finished request: resp and err are exactly what
// the provider returned (for a Stream call, resp is the assembled final
// response). Use it for logging, tracing or cost accounting. Hooks run after
// the call completes and must not mutate resp.
type ResponseHook func(ctx context.Context, req *Request, resp *Response, err error)

// WithModel sets the default model id.
func WithModel(model string) ClientOption {
	return func(c *Client) { c.model = model }
}

// WithMaxTokens sets the default output token limit.
func WithMaxTokens(n int) ClientOption {
	return func(c *Client) { c.maxTokens = n }
}

// WithTemperature sets the default sampling temperature.
func WithTemperature(v float64) ClientOption {
	return func(c *Client) { c.temperature = &v }
}

// WithTopP sets the default nucleus-sampling probability mass.
func WithTopP(v float64) ClientOption {
	return func(c *Client) { c.topP = &v }
}

// WithStopSequences sets default stop sequences that end generation.
// Providers without stop-sequence support (OpenAI Responses) ignore them.
func WithStopSequences(seq ...string) ClientOption {
	return func(c *Client) { c.stop = append([]string{}, seq...) }
}

// WithResponseFormat sets the default output format (structured output).
func WithResponseFormat(f ResponseFormat) ClientOption {
	return func(c *Client) { cp := f; c.format = &cp }
}

// WithExtra merges a provider-specific top-level field into every request
// body this client sends (e.g. a gateway dialect flag). Request-level
// Request.WithExtra wins on key conflicts; the client-level map is copied, so
// later WithExtra calls on the client do not affect in-flight requests.
func WithExtra(key string, value any) ClientOption {
	return func(c *Client) {
		if c.extra == nil {
			c.extra = map[string]any{}
		}
		c.extra[key] = value
	}
}

// WithRequestHook registers a hook invoked before every request is sent
// (see RequestHook). Multiple hooks run in registration order.
func WithRequestHook(hooks ...RequestHook) ClientOption {
	return func(c *Client) { c.requestHooks = append(c.requestHooks, hooks...) }
}

// WithResponseHook registers a hook invoked after every request finishes
// (see ResponseHook). Multiple hooks run in registration order.
func WithResponseHook(hooks ...ResponseHook) ClientOption {
	return func(c *Client) { c.responseHooks = append(c.responseHooks, hooks...) }
}

// NewClient creates a Client over the given provider.
func NewClient(provider Provider, opts ...ClientOption) *Client {
	c := &Client{provider: provider}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewOpenAIClient is a shortcut for NewClient(NewOpenAIProvider(...)) with
// the model set: the common case folded into one call.
//
//	client := callable.NewOpenAIClient(apiKey, callable.DeepSeekURL, "deepseek-v4")
//
// Use the two-step form (NewClient + NewOpenAIProvider) when you need
// ProviderOptions such as WithRetries or WithHTTPClient.
func NewOpenAIClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return NewClient(NewOpenAIProvider(apiKey, baseURL), append([]ClientOption{WithModel(model)}, opts...)...)
}

// NewOpenAIResponsesClient is NewOpenAIClient for the OpenAI Responses
// format.
func NewOpenAIResponsesClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return NewClient(NewOpenAIResponsesProvider(apiKey, baseURL), append([]ClientOption{WithModel(model)}, opts...)...)
}

// NewAnthropicClient is NewOpenAIClient for the Anthropic Messages format.
func NewAnthropicClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return NewClient(NewAnthropicProvider(apiKey, baseURL), append([]ClientOption{WithModel(model)}, opts...)...)
}

// Provider returns the underlying provider.
func (c *Client) Provider() Provider { return c.provider }

// derive returns a copy of the client with a different default model, keeping
// the provider and all other defaults. Used by sub-agents with a model
// override.
func (c *Client) derive(model string) *Client {
	cp := *c
	cp.model = model
	return &cp
}

// Create performs a single non-streaming model call.
func (c *Client) Create(ctx context.Context, req *Request) (*Response, error) {
	req = c.applyDefaults(req)
	c.runRequestHooks(ctx, req)
	resp, err := c.provider.Create(ctx, req)
	c.runResponseHooks(ctx, req, resp, err)
	return resp, err
}

// Stream performs a single streaming model call, forwarding every event to
// onEvent and returning the assembled response.
func (c *Client) Stream(ctx context.Context, req *Request, onEvent eventSink) (*Response, error) {
	req = c.applyDefaults(req)
	c.runRequestHooks(ctx, req)
	resp, err := c.provider.Stream(ctx, req, onEvent)
	c.runResponseHooks(ctx, req, resp, err)
	return resp, err
}

func (c *Client) runRequestHooks(ctx context.Context, req *Request) {
	for _, h := range c.requestHooks {
		h(ctx, req)
	}
}

func (c *Client) runResponseHooks(ctx context.Context, req *Request, resp *Response, err error) {
	for _, h := range c.responseHooks {
		h(ctx, req, resp, err)
	}
}

// applyDefaults returns a copy of req with unset fields filled from client
// defaults. The original request is not mutated.
func (c *Client) applyDefaults(req *Request) *Request {
	out := *req
	if out.Model == "" {
		out.Model = c.model
	}
	if out.MaxTokens == 0 {
		out.MaxTokens = c.maxTokens
	}
	if out.Temperature == nil {
		out.Temperature = c.temperature
	}
	if out.TopP == nil {
		out.TopP = c.topP
	}
	if out.Stop == nil {
		out.Stop = c.stop
	}
	if out.Format == nil {
		out.Format = c.format
	}
	if len(c.extra) > 0 {
		// Merge client-level extras under request-level ones (request wins),
		// into a fresh map so the caller's Request is never mutated.
		merged := make(map[string]any, len(c.extra)+len(out.Extra))
		for k, v := range c.extra {
			merged[k] = v
		}
		for k, v := range out.Extra {
			merged[k] = v
		}
		out.Extra = merged
	}
	return &out
}
