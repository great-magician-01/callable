package core

import "context"

// Client sends unified requests through a Provider, filling in defaults.
// Create and Stream are the low-level single-turn entry points; for the
// automatic tool-calling loop use Agent.
type Client struct {
	provider    Provider
	model       string
	maxTokens   int
	temperature *float64
}

// ClientOption configures a Client.
type ClientOption func(*Client)

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

// NewClient creates a Client over the given provider.
func NewClient(provider Provider, opts ...ClientOption) *Client {
	c := &Client{provider: provider}
	for _, o := range opts {
		o(c)
	}
	return c
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
	return c.provider.Create(ctx, c.applyDefaults(req))
}

// Stream performs a single streaming model call, forwarding every event to
// onEvent and returning the assembled response.
func (c *Client) Stream(ctx context.Context, req *Request, onEvent eventSink) (*Response, error) {
	return c.provider.Stream(ctx, c.applyDefaults(req), onEvent)
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
	return &out
}
