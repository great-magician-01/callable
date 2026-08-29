package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	provider "github.com/great-magician-01/callable/internal/provider"
)

// DefaultWebSearchToolName is the name of the Tavily-backed fallback
// web-search tool.
const DefaultWebSearchToolName = "web_search"

// kimiWebSearchToolName is Kimi's built-in web-search function. The server
// performs the actual search when the client echoes the call arguments back
// as the tool result.
const kimiWebSearchToolName = "$web_search"

// ── Agent options ──────────────────────────────────────────────────────────

// WithWebSearch explicitly enables or disables the web-search tool.
//
// The default (option not given) is "auto": web search is enabled when the
// provider endpoint has built-in server-side search (Kimi, GLM/Z.AI, Qwen,
// api.anthropic.com, OpenAI Responses) or when a Tavily API key is configured
// via WithTavilyAPIKey, and disabled otherwise. A provider's built-in search
// is always preferred over the Tavily fallback. Enabling explicitly with no
// built-in support and no Tavily key exposes no tool.
func WithWebSearch(enabled bool) AgentOption {
	return func(a *Agent) { a.webSearch = &enabled }
}

// WithTavilyAPIKey configures a Tavily API key used for the fallback
// web-search tool (https://tavily.com). It is only used when the provider
// endpoint has no built-in web search.
func WithTavilyAPIKey(key string) AgentOption {
	return func(a *Agent) { a.tavilyKey = key }
}

// resolveWebSearch wires web search into the agent: provider built-in search
// first, the Tavily fallback second, nothing otherwise. Built-in server-side
// search only sets webSearchBuiltin (the provider adapter injects the wire
// fields); the echo protocol additionally registers the $web_search stub.
func (a *Agent) resolveWebSearch() {
	enabled := a.webSearch == nil || *a.webSearch
	if !enabled {
		return
	}
	support := provider.SupportsWebSearch(a.client.Provider())
	switch support {
	case provider.WebSearchEcho:
		a.tools.add(newKimiWebSearchTool())
		a.webSearchBuiltin = true
	case provider.WebSearchServer:
		a.webSearchBuiltin = true
	default:
		if a.tavilyKey != "" {
			a.tools.add(newTavilyWebSearchTool(a.tavilyKey))
		}
	}
}

// ── Kimi echo tool ─────────────────────────────────────────────────────────

// newKimiWebSearchTool builds the $web_search stub for Kimi's builtin
// function protocol: the tool result is the call arguments echoed back
// verbatim, which is the signal for the server to run the search.
func newKimiWebSearchTool() Tool {
	return NewRawTool(kimiWebSearchToolName,
		"Search the web for current information.",
		`{"type":"object","properties":{"query":{"type":"string","description":"The search query"}}}`,
		func(_ context.Context, rawArgs string) (any, error) {
			return rawArgs, nil
		})
}

// ── Tavily fallback tool ───────────────────────────────────────────────────

// tavilySearchURL is the Tavily search endpoint; a package-level variable so
// tests can point it at a mock server.
var tavilySearchURL = "https://api.tavily.com/search"

// tavilyHTTPClient bounds fallback searches; agent-run cancellation still
// applies via the request context.
var tavilyHTTPClient = &http.Client{Timeout: 30 * time.Second}

type tavilySearchArgs struct {
	Query      string `json:"query" jsonschema:"description=The search query"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum number of results to return (1-10, default 5)"`
}

// newTavilyWebSearchTool builds the web_search function tool backed by the
// Tavily search API.
func newTavilyWebSearchTool(apiKey string) Tool {
	return NewTool(DefaultWebSearchToolName,
		"Search the web for current information. Returns a short answer and a ranked list of results with titles, URLs and content snippets.",
		func(ctx context.Context, args tavilySearchArgs) (any, error) {
			return tavilySearch(ctx, apiKey, args)
		})
}

// tavilySearch performs one Tavily search and formats the outcome as plain
// text for the model.
func tavilySearch(ctx context.Context, apiKey string, args tavilySearchArgs) (string, error) {
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}
	body, err := json.Marshal(map[string]any{
		"query":          args.Query,
		"max_results":    maxResults,
		"search_depth":   "basic",
		"include_answer": true,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilySearchURL, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := tavilyHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("tavily search: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
		Detail struct {
			Error string `json:"error"`
		} `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("tavily search: decode response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := out.Detail.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("tavily search: %s", msg)
	}

	var b strings.Builder
	if out.Answer != "" {
		fmt.Fprintf(&b, "Answer: %s\n\n", out.Answer)
	}
	for i, r := range out.Results {
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n", i+1, r.Title, r.URL, r.Content)
		if i < len(out.Results)-1 {
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 {
		return "No results found.", nil
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
