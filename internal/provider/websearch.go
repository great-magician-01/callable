package provider

import (
	"net/url"
	"strings"
)

// WebSearchSupport classifies a provider's built-in (server-side) web-search
// capability.
type WebSearchSupport int

const (
	// WebSearchNone means the endpoint has no built-in web search; the agent
	// falls back to the Tavily tool when a key is configured.
	WebSearchNone WebSearchSupport = iota
	// WebSearchServer means the endpoint executes searches server-side
	// (GLM/Z.AI, Qwen, Anthropic, OpenAI Responses); the provider adapter
	// injects the right wire fields and no client-side round trip is needed.
	WebSearchServer
	// WebSearchEcho is Kimi's builtin-function protocol: the model calls
	// $web_search and the client echoes the arguments back as the tool
	// result; the server performs the search on the next request.
	WebSearchEcho
)

// SupportsWebSearch reports the built-in (server-side) web-search
// capability of a provider. Only the built-in providers have one;
// any other Provider implementation reports WebSearchNone.
func SupportsWebSearch(p Provider) WebSearchSupport {
	if c, ok := p.(interface{ supportsWebSearch() WebSearchSupport }); ok {
		return c.supportsWebSearch()
	}
	return WebSearchNone
}

// supportsWebSearch reports the built-in web-search support of a Chat
// Completions endpoint: Kimi (echo protocol), GLM/Z.AI and Qwen (server-side,
// detected via the Compat dialect).
func (p *OpenAIProvider) supportsWebSearch() WebSearchSupport {
	if isMoonshotHost(p.api.cfg.baseURL) {
		return WebSearchEcho
	}
	if p.compat&(CompatGLM|CompatQwen) != 0 {
		return WebSearchServer
	}
	return WebSearchNone
}

// supportsWebSearch reports built-in web search for the official OpenAI
// Responses endpoint ({"type":"web_search"} hosted tool).
func (p *OpenAIResponsesProvider) supportsWebSearch() WebSearchSupport {
	if isOfficialOpenAI(p.api.cfg.baseURL) {
		return WebSearchServer
	}
	return WebSearchNone
}

// supportsWebSearch reports built-in web search for the official Anthropic
// endpoint (web_search_20250305 server tool). Anthropic-compatible
// third-party endpoints are not assumed to support it.
func (p *AnthropicProvider) supportsWebSearch() WebSearchSupport {
	if isOfficialAnthropic(p.api.cfg.baseURL) {
		return WebSearchServer
	}
	return WebSearchNone
}

func isMoonshotHost(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(u.Host), "moonshot")
}

func isOfficialAnthropic(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, "api.anthropic.com")
}

// kimiWebSearchToolName is Kimi's built-in web-search function on the wire.
// The agent-side echo stub (internal/agent) registers a tool under the same
// name; the two packages cannot share a constant (agent imports provider).
const kimiWebSearchToolName = "$web_search"

// defaultWebSearchToolName is the wire name of Anthropic's server-side
// web-search tool. It matches agent.DefaultWebSearchToolName (the Tavily
// fallback tool is named after it); see the note above on sharing constants.
const defaultWebSearchToolName = "web_search"
