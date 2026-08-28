package core

// Request is a provider-agnostic model request. Zero-value fields fall back
// to Client and Agent defaults before the request reaches a provider.
type Request struct {
	// Model is the model id, e.g. "gpt-5" or "claude-sonnet-5".
	Model string
	// Messages is the full conversation, including system messages.
	Messages []Message
	// Tools lists tool definitions available to the model.
	Tools []Tool
	// Thinking enables reasoning mode. nil disables it.
	Thinking *Thinking
	// MaxTokens caps output tokens. 0 uses the client default (or a provider
	// minimum when thinking is enabled).
	MaxTokens int
	// Temperature is sampling temperature. nil uses the client default.
	Temperature *float64
	// WebSearch asks the provider to enable its built-in server-side web
	// search when the endpoint has one (see WithWebSearch). Providers
	// without built-in search ignore it.
	WebSearch bool
	// Extra holds arbitrary top-level fields merged into the provider request
	// body, as an escape hatch for provider-specific parameters.
	Extra map[string]any
}

// NewRequest creates a request for the given messages.
func NewRequest(messages ...Message) *Request {
	return &Request{Messages: messages}
}

// WithModel sets the model id.
func (r *Request) WithModel(model string) *Request {
	r.Model = model
	return r
}

// WithTools attaches tools to the request.
func (r *Request) WithTools(tools ...Tool) *Request {
	r.Tools = append(r.Tools, tools...)
	return r
}

// WithThinking enables reasoning mode with the given configuration.
func (r *Request) WithThinking(t Thinking) *Request {
	cp := t
	r.Thinking = &cp
	return r
}

// WithMaxTokens caps output tokens.
func (r *Request) WithMaxTokens(n int) *Request {
	r.MaxTokens = n
	return r
}

// WithTemperature sets sampling temperature.
func (r *Request) WithTemperature(v float64) *Request {
	cp := v
	r.Temperature = &cp
	return r
}

// WithExtra merges a provider-specific top-level field into the request body.
// It overwrites a field the library sets itself, so use with care.
func (r *Request) WithExtra(key string, value any) *Request {
	if r.Extra == nil {
		r.Extra = map[string]any{}
	}
	r.Extra[key] = value
	return r
}
