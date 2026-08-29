// Package callable is a unified Go client library for LLM APIs with a built-in
// agent loop.
//
// It speaks three wire formats behind one provider-agnostic message model:
//
//   - OpenAI Chat Completions (including OpenAI-compatible endpoints such as
//     GLM, DeepSeek and Qwen)
//   - OpenAI Responses
//   - Anthropic Messages
//
// On top of that sits an Agent that runs the full tool-calling loop
// automatically: model -> tool execution -> model -> ... until a final answer,
// with streaming events, thinking/reasoning support, skills (progressive
// disclosure), sub-agent delegation, and image input.
//
// A minimal example:
//
//	client := callable.NewClient(
//		callable.NewAnthropicProvider(apiKey, callable.AnthropicURL),
//		callable.WithModel("claude-sonnet-5"),
//	)
//	agent := callable.NewAgent(client,
//		callable.WithTools(myTool),
//		callable.WithThinking(callable.Thinking{Effort: callable.EffortMedium}),
//	)
//	result, err := agent.Run(ctx, callable.User("Hello!"))
//
// This file is the single public entry point of the module; the
// implementation lives in internal/core and is re-exported here.
package callable

import (
	"context"
	"net/http"
	"time"

	core "github.com/great-magician-01/callable/internal/core"
)

// ── Messages and content parts ─────────────────────────────────────────────

type (
	// Role identifies who authored a message.
	Role = core.Role
	// Message is a provider-agnostic chat message; its Parts may mix text,
	// images, thinking, tool calls and tool results.
	Message = core.Message

	// Part is one piece of message content (sealed interface).
	Part = core.Part
	// TextPart is a piece of text.
	TextPart = core.TextPart
	// ImagePart is an image given by path, URL or raw bytes.
	ImagePart = core.ImagePart
	// ThinkingPart is a piece of model reasoning.
	ThinkingPart = core.ThinkingPart
	// ToolCallPart is a tool invocation requested by the model.
	ToolCallPart = core.ToolCallPart
	// ToolResultPart carries the output of a tool execution back to the model.
	ToolResultPart = core.ToolResultPart
)

// Message roles.
const (
	RoleSystem    = core.RoleSystem
	RoleUser      = core.RoleUser
	RoleAssistant = core.RoleAssistant
	RoleTool      = core.RoleTool
)

// System builds a system message.
func System(text string) Message { return core.System(text) }

// User builds a user message from strings and/or Parts.
func User(parts ...any) Message { return core.User(parts...) }

// Assistant builds an assistant message from strings and/or Parts.
func Assistant(parts ...any) Message { return core.Assistant(parts...) }

// ToolResults builds a message carrying tool execution results.
func ToolResults(results ...ToolResultPart) Message { return core.ToolResults(results...) }

// Text builds a TextPart.
func Text(text string) TextPart { return core.Text(text) }

// Image references an image by file path or http(s) URL; local files are read
// and converted to the provider's native image format at send time.
func Image(ref string) ImagePart { return core.Image(ref) }

// ImageURL references an image by http(s) URL.
func ImageURL(url string) ImagePart { return core.ImageURL(url) }

// ImageBytes wraps raw image bytes with an explicit media type.
func ImageBytes(data []byte, mediaType string) ImagePart { return core.ImageBytes(data, mediaType) }

// UnmarshalPart decodes a single serialized Part.
func UnmarshalPart(data []byte) (Part, error) { return core.UnmarshalPart(data) }

// ── Requests, responses, usage ─────────────────────────────────────────────

type (
	// Request is a single provider-agnostic model call.
	Request = core.Request
	// Response is the assembled result of a model call.
	Response = core.Response
	// StopReason explains why generation stopped.
	StopReason = core.StopReason
	// ToolCall is a single tool invocation requested by the model.
	ToolCall = core.ToolCall
	// Usage reports token consumption of a call (or an accumulated run).
	Usage = core.Usage
	// ResponseFormat constrains the model's output format (structured
	// output); see JSONMode, JSONSchema and JSONSchemaFor.
	ResponseFormat = core.ResponseFormat
)

// Stop reasons.
const (
	StopReasonEndTurn   = core.StopReasonEndTurn
	StopReasonToolCalls = core.StopReasonToolCalls
	StopReasonMaxTokens = core.StopReasonMaxTokens
	StopReasonOther     = core.StopReasonOther
)

// NewRequest builds a Request from messages; configure it with its With* methods.
func NewRequest(messages ...Message) *Request { return core.NewRequest(messages...) }

// JSONMode requests free-form JSON output: the model answers with a JSON
// value of any shape. Set it with Request.WithResponseFormat.
func JSONMode() ResponseFormat { return core.JSONMode() }

// JSONSchema requests output conforming to the given JSON Schema. Set it with
// Request.WithResponseFormat.
func JSONSchema(name string, schema map[string]any, strict bool) ResponseFormat {
	return core.JSONSchema(name, schema, strict)
}

// JSONSchemaFor requests output conforming to the JSON Schema reflected from
// the Go type T, using the same reflection (and `jsonschema` struct tags) as
// NewTool:
//
//	type Recipe struct {
//		Name  string   `json:"name" jsonschema:"description=Dish name"`
//		Steps []string `json:"steps"`
//	}
//
//	resp, err := client.Create(ctx, callable.NewRequest(
//		callable.User("Give me a pancake recipe"),
//	).WithResponseFormat(callable.JSONSchemaFor[Recipe]("recipe", true)))
//	var recipe Recipe
//	err = resp.DecodeJSON(&recipe)
func JSONSchemaFor[T any](name string, strict bool) ResponseFormat {
	return core.JSONSchemaFor[T](name, strict)
}

// ── Streaming events ───────────────────────────────────────────────────────

type (
	// Event is a streaming event (sealed interface); handle with a type switch.
	Event = core.Event
	// MessageStartEvent marks the beginning of an assistant message.
	MessageStartEvent = core.MessageStartEvent
	// ThinkingDeltaEvent carries an increment of reasoning text.
	ThinkingDeltaEvent = core.ThinkingDeltaEvent
	// TextDeltaEvent carries an increment of the answer text.
	TextDeltaEvent = core.TextDeltaEvent
	// ToolCallDeltaEvent carries an increment of a streamed tool call.
	ToolCallDeltaEvent = core.ToolCallDeltaEvent
	// MessageDoneEvent marks the end of an assistant message.
	MessageDoneEvent = core.MessageDoneEvent
	// TurnStartEvent marks the beginning of an agent loop turn.
	TurnStartEvent = core.TurnStartEvent
	// TurnEndEvent marks the end of a turn.
	TurnEndEvent = core.TurnEndEvent
	// ToolCallEvent is emitted just before an approved tool executes.
	ToolCallEvent = core.ToolCallEvent
	// ToolResultEvent is emitted after a tool finished executing.
	ToolResultEvent = core.ToolResultEvent
	// AgentDoneEvent is emitted when the agent loop finished.
	AgentDoneEvent = core.AgentDoneEvent
	// SubAgentEvent wraps an event emitted inside a delegated sub-agent's
	// loop; only produced when WithSubAgentEvents is enabled.
	SubAgentEvent = core.SubAgentEvent
	// SessionCompactEvent is emitted after a session auto-compacts its
	// history.
	SessionCompactEvent = core.SessionCompactEvent
)

// ── Thinking / reasoning ───────────────────────────────────────────────────

type (
	// Effort is a unified thinking-effort level mapped to each provider's
	// native controls.
	Effort = core.Effort
	// Thinking configures thinking/reasoning for a request or agent.
	Thinking = core.Thinking
)

// Effort levels.
const (
	EffortOff    = core.EffortOff
	EffortLow    = core.EffortLow
	EffortMedium = core.EffortMedium
	EffortHigh   = core.EffortHigh
)

// ── Errors ─────────────────────────────────────────────────────────────────

type (
	// APIError is a non-2xx provider response or a transport failure.
	APIError = core.APIError
	// MaxTurnsError is returned when an agent run exceeds its turn limit; its
	// Partial field carries the partial result.
	MaxTurnsError = core.MaxTurnsError
)

// ── Tools ──────────────────────────────────────────────────────────────────

type (
	// ToolDefinition is the schema advertised to the model.
	ToolDefinition = core.ToolDefinition
	// ToolResult is the outcome of a tool execution.
	ToolResult = core.ToolResult
	// Tool is a callable exposed to the model.
	Tool = core.Tool
)

// NewTool creates a Tool from a handler whose argument struct is reflected
// into a JSON Schema. Describe parameters with `jsonschema` struct tags.
func NewTool[A any](name, description string, fn func(ctx context.Context, args A) (any, error)) Tool {
	return core.NewTool[A](name, description, fn)
}

// NewRawTool creates a Tool with a hand-written JSON Schema; the handler
// receives the raw JSON arguments string.
func NewRawTool(name, description, parametersJSON string, fn func(ctx context.Context, rawArgs string) (any, error)) Tool {
	return core.NewRawTool(name, description, parametersJSON, fn)
}

// TextResult wraps a plain-text tool output.
func TextResult(content string) ToolResult { return core.TextResult(content) }

// ErrorResult wraps an error as a failed tool result.
func ErrorResult(err error) ToolResult { return core.ErrorResult(err) }

// ── Web search ─────────────────────────────────────────────────────────────

// DefaultWebSearchToolName is the name of the Tavily-backed fallback
// web-search tool.
const DefaultWebSearchToolName = core.DefaultWebSearchToolName

// WithWebSearch explicitly enables or disables the web-search tool.
//
// The default (option not given) is "auto": web search is enabled when the
// provider endpoint has built-in server-side search (Kimi, GLM/Z.AI, Qwen,
// api.anthropic.com, OpenAI Responses) or when a Tavily API key is configured
// via WithTavilyAPIKey, and disabled otherwise. A provider's built-in search
// is always preferred over the Tavily fallback. Enabling explicitly with no
// built-in support and no Tavily key exposes no tool.
func WithWebSearch(enabled bool) AgentOption { return core.WithWebSearch(enabled) }

// WithTavilyAPIKey configures a Tavily API key used for the fallback
// web-search tool (https://tavily.com). It is only used when the provider
// endpoint has no built-in web search.
func WithTavilyAPIKey(key string) AgentOption { return core.WithTavilyAPIKey(key) }

// ── Skills (progressive disclosure) ────────────────────────────────────────

type (
	// Skill is a named instruction set the model can load on demand.
	Skill = core.Skill
	// SkillReadHook can rewrite skill instructions before they reach the model.
	SkillReadHook = core.SkillReadHook
)

// DefaultSkillToolName is the name of the built-in skill-loading tool.
const DefaultSkillToolName = core.DefaultSkillToolName

// NewSkill builds a Skill.
func NewSkill(name, description, instructions string) Skill {
	return core.NewSkill(name, description, instructions)
}

// ── Sub-agents (delegation with progressive disclosure) ────────────────────

type (
	// SubAgent is a named agent definition the parent agent can delegate
	// subtasks to. It is not exposed as a tool until the model loads it via
	// the built-in load_agent tool.
	SubAgent = core.SubAgent
	// SubAgentOption configures a SubAgent.
	SubAgentOption = core.SubAgentOption
)

// DefaultSubAgentLoadToolName is the name of the built-in sub-agent-loading
// tool.
const DefaultSubAgentLoadToolName = core.DefaultSubAgentLoadToolName

// NewSubAgent builds a SubAgent definition; register it with WithSubAgents.
func NewSubAgent(name, description string, opts ...SubAgentOption) SubAgent {
	return core.NewSubAgent(name, description, opts...)
}

// WithSubAgentClient gives the sub-agent its own client (e.g. a different
// provider) instead of inheriting the parent agent's client.
func WithSubAgentClient(client *Client) SubAgentOption { return core.WithSubAgentClient(client) }

// WithSubAgentModel overrides the sub-agent's model while reusing the parent
// agent's client. Ignored when WithSubAgentClient supplies a custom client.
func WithSubAgentModel(model string) SubAgentOption { return core.WithSubAgentModel(model) }

// WithSubAgentPrompt sets the sub-agent's system prompt.
func WithSubAgentPrompt(prompt string) SubAgentOption { return core.WithSubAgentPrompt(prompt) }

// WithSubAgentTools registers tools available inside the sub-agent loop.
func WithSubAgentTools(tools ...Tool) SubAgentOption { return core.WithSubAgentTools(tools...) }

// WithSubAgentSkills registers skills available inside the sub-agent loop.
func WithSubAgentSkills(skills ...Skill) SubAgentOption { return core.WithSubAgentSkills(skills...) }

// WithSubAgentThinking configures thinking/reasoning for the sub-agent.
func WithSubAgentThinking(t Thinking) SubAgentOption { return core.WithSubAgentThinking(t) }

// WithSubAgentMaxTurns caps the number of model calls per sub-agent run.
func WithSubAgentMaxTurns(n int) SubAgentOption { return core.WithSubAgentMaxTurns(n) }

// ── Providers ──────────────────────────────────────────────────────────────

type (
	// Provider converts unified requests to a specific wire format.
	Provider = core.Provider
	// Compat is a bitmask of OpenAI-compatible endpoint dialects.
	Compat = core.Compat
	// ProviderOption configures a provider.
	ProviderOption = core.ProviderOption

	// OpenAIProvider talks the OpenAI Chat Completions format.
	OpenAIProvider = core.OpenAIProvider
	// OpenAIResponsesProvider talks the OpenAI Responses format.
	OpenAIResponsesProvider = core.OpenAIResponsesProvider
	// AnthropicProvider talks the Anthropic Messages format.
	AnthropicProvider = core.AnthropicProvider
)

// Endpoint dialects (auto-detected from the base URL, or set with WithCompat).
const (
	CompatNone     = core.CompatNone
	CompatGLM      = core.CompatGLM
	CompatQwen     = core.CompatQwen
	CompatDeepSeek = core.CompatDeepSeek
	CompatArk      = core.CompatArk
)

// Well-known endpoint base URLs. Pass one as the baseURL argument of the
// provider constructors instead of spelling out the address:
//
//	callable.NewAnthropicProvider(apiKey, callable.DeepSeekAnthropicURL)
//	callable.NewOpenAIProvider(apiKey, callable.DeepSeekURL)
//
// Endpoint dialects are auto-detected from these URLs. Any other
// OpenAI- or Anthropic-compatible endpoint can still be passed as a literal
// baseURL.
const (
	// OpenAIURL is the official OpenAI API root (Chat Completions and
	// Responses).
	OpenAIURL = core.OpenAIURL
	// AnthropicURL is the official Anthropic API root.
	AnthropicURL = core.AnthropicURL

	// DeepSeekURL is DeepSeek's OpenAI-compatible endpoint.
	DeepSeekURL = core.DeepSeekURL
	// GLMURL is Zhipu GLM's (bigmodel.cn) OpenAI-compatible endpoint.
	GLMURL = core.GLMURL
	// ZAIURL is Z.AI's OpenAI-compatible endpoint.
	ZAIURL = core.ZAIURL
	// QwenURL is Alibaba DashScope's OpenAI-compatible endpoint.
	QwenURL = core.QwenURL
	// ArkURL is Volcano Ark's OpenAI-compatible endpoint.
	ArkURL = core.ArkURL
	// KimiURL is Moonshot AI's (Kimi) OpenAI-compatible endpoint for the
	// China platform. The international platform mirrors it at
	// https://api.moonshot.ai/v1.
	KimiURL = core.KimiURL

	// DeepSeekAnthropicURL is DeepSeek's Anthropic-compatible endpoint.
	DeepSeekAnthropicURL = core.DeepSeekAnthropicURL
	// GLMAnthropicURL is Zhipu GLM's (bigmodel.cn) Anthropic-compatible
	// endpoint.
	GLMAnthropicURL = core.GLMAnthropicURL
	// ZAIAnthropicURL is Z.AI's Anthropic-compatible endpoint.
	ZAIAnthropicURL = core.ZAIAnthropicURL
	// KimiAnthropicURL is Moonshot AI's (Kimi) Anthropic-compatible endpoint
	// for the China platform. The international platform mirrors it at
	// https://api.moonshot.ai/anthropic.
	KimiAnthropicURL = core.KimiAnthropicURL
)

// NewOpenAIProvider creates an OpenAI Chat Completions provider for the given
// endpoint. baseURL is the API root including any version prefix; any
// OpenAI-compatible endpoint (GLM, DeepSeek, Qwen, ...) works. Well-known
// endpoints are available as constants, e.g. OpenAIURL or DeepSeekURL.
func NewOpenAIProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIProvider {
	return core.NewOpenAIProvider(apiKey, baseURL, opts...)
}

// NewOpenAIResponsesProvider creates an OpenAI Responses provider for the
// given endpoint. baseURL is the API root including any version prefix, e.g.
// OpenAIURL.
func NewOpenAIResponsesProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIResponsesProvider {
	return core.NewOpenAIResponsesProvider(apiKey, baseURL, opts...)
}

// NewAnthropicProvider creates an Anthropic Messages provider for the given
// endpoint, e.g. AnthropicURL. A baseURL already ending in /v1 is tolerated.
// Anthropic-compatible third-party endpoints are available as constants, e.g.
// DeepSeekAnthropicURL.
func NewAnthropicProvider(apiKey, baseURL string, opts ...ProviderOption) *AnthropicProvider {
	return core.NewAnthropicProvider(apiKey, baseURL, opts...)
}

// WithHTTPClient supplies a custom *http.Client.
func WithHTTPClient(client *http.Client) ProviderOption { return core.WithHTTPClient(client) }

// WithHeader adds a header to every provider request.
func WithHeader(key, value string) ProviderOption { return core.WithHeader(key, value) }

// WithRetries sets how many times transient failures (429, 5xx, network
// errors) are retried, waiting 3s, 10s, then 30s between attempts (see
// WithRetryBackoff to change the schedule). Default 3; pass 0 to disable.
func WithRetries(n int) ProviderOption { return core.WithRetries(n) }

// WithRetryBackoff replaces the default retry wait schedule (3s, 10s, 30s):
// delays[i] is the wait before retry i+1, and attempts beyond the schedule
// reuse the last delay.
func WithRetryBackoff(delays ...time.Duration) ProviderOption {
	return core.WithRetryBackoff(delays...)
}

// WithCompat overrides the auto-detected endpoint dialect.
func WithCompat(c Compat) ProviderOption { return core.WithCompat(c) }

// ── Client ─────────────────────────────────────────────────────────────────

type (
	// Client is a thin wrapper around a Provider applying model defaults.
	Client = core.Client
	// ClientOption configures a Client.
	ClientOption = core.ClientOption
	// RequestHook observes every request right before it is sent (after
	// client defaults are applied). Use it for logging, tracing or metrics.
	RequestHook = core.RequestHook
	// ResponseHook observes every finished request with exactly what the
	// provider returned. Use it for logging, tracing or cost accounting.
	ResponseHook = core.ResponseHook
)

// NewClient builds a Client on top of a provider.
func NewClient(provider Provider, opts ...ClientOption) *Client {
	return core.NewClient(provider, opts...)
}

// NewOpenAIClient is a shortcut for NewClient(NewOpenAIProvider(...)) with
// the model set: the common case folded into one call.
//
//	client := callable.NewOpenAIClient(apiKey, callable.DeepSeekURL, "deepseek-v4")
//
// Use the two-step form (NewClient + NewOpenAIProvider) when you need
// ProviderOptions such as WithRetries or WithHTTPClient.
func NewOpenAIClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return core.NewOpenAIClient(apiKey, baseURL, model, opts...)
}

// NewOpenAIResponsesClient is NewOpenAIClient for the OpenAI Responses
// format.
func NewOpenAIResponsesClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return core.NewOpenAIResponsesClient(apiKey, baseURL, model, opts...)
}

// NewAnthropicClient is NewOpenAIClient for the Anthropic Messages format.
func NewAnthropicClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return core.NewAnthropicClient(apiKey, baseURL, model, opts...)
}

// WithModel sets the default model.
func WithModel(model string) ClientOption { return core.WithModel(model) }

// WithMaxTokens sets the default max output tokens.
func WithMaxTokens(n int) ClientOption { return core.WithMaxTokens(n) }

// WithTemperature sets the default sampling temperature.
func WithTemperature(v float64) ClientOption { return core.WithTemperature(v) }

// WithTopP sets the default nucleus-sampling probability mass.
func WithTopP(v float64) ClientOption { return core.WithTopP(v) }

// WithStopSequences sets default stop sequences that end generation.
// Providers without stop-sequence support (OpenAI Responses) ignore them.
func WithStopSequences(seq ...string) ClientOption { return core.WithStopSequences(seq...) }

// WithResponseFormat sets the default output format (structured output).
func WithResponseFormat(f ResponseFormat) ClientOption { return core.WithResponseFormat(f) }

// WithExtra merges a provider-specific top-level field into every request
// body this client sends (e.g. a gateway dialect flag). Request-level
// Request.WithExtra wins on key conflicts.
func WithExtra(key string, value any) ClientOption { return core.WithExtra(key, value) }

// WithRequestHook registers a hook invoked before every request is sent.
// Multiple hooks run in registration order.
func WithRequestHook(hooks ...RequestHook) ClientOption { return core.WithRequestHook(hooks...) }

// WithResponseHook registers a hook invoked after every request finishes.
// Multiple hooks run in registration order.
func WithResponseHook(hooks ...ResponseHook) ClientOption { return core.WithResponseHook(hooks...) }

// ── Agent ──────────────────────────────────────────────────────────────────

type (
	// Agent runs the full tool-calling loop automatically.
	Agent = core.Agent
	// AgentOption configures an Agent.
	AgentOption = core.AgentOption
	// AgentResult is the outcome of an agent run.
	AgentResult = core.AgentResult
	// ToolDecision is the verdict of a ToolCallHook.
	ToolDecision = core.ToolDecision
	// ToolCallHook can approve, deny or rewrite a tool call before execution.
	ToolCallHook = core.ToolCallHook
	// Session keeps conversation history across multiple agent runs.
	Session = core.Session
)

// Agent result stop reasons.
const (
	// AgentCompleted means the model produced a final answer.
	AgentCompleted = core.AgentCompleted
	// AgentMaxTurns means the loop hit the turn limit without a final answer.
	AgentMaxTurns = core.AgentMaxTurns
)

// NewAgent builds an Agent around a Client.
func NewAgent(client *Client, opts ...AgentOption) *Agent {
	return core.NewAgent(client, opts...)
}

// WithSystemPrompt sets the agent's system prompt.
func WithSystemPrompt(prompt string) AgentOption { return core.WithSystemPrompt(prompt) }

// WithTools registers tools the agent may call.
func WithTools(tools ...Tool) AgentOption { return core.WithTools(tools...) }

// WithSkills registers skills for progressive disclosure.
func WithSkills(skills ...Skill) AgentOption { return core.WithSkills(skills...) }

// WithSubAgents registers sub-agent definitions. They are not exposed as
// tools by default: the system prompt only lists name/description, and the
// model must call the built-in load_agent tool to register a call_<name>
// tool before delegating to one.
func WithSubAgents(subs ...SubAgent) AgentOption { return core.WithSubAgents(subs...) }

// WithThinking configures thinking/reasoning for the agent.
func WithThinking(t Thinking) AgentOption { return core.WithThinking(t) }

// WithMaxTurns bounds the number of model<->tool loop turns.
func WithMaxTurns(n int) AgentOption { return core.WithMaxTurns(n) }

// WithToolCallHook installs a hook invoked before every tool execution.
func WithToolCallHook(h ToolCallHook) AgentOption { return core.WithToolCallHook(h) }

// WithParallelToolExecution allows concurrent tool execution within a turn.
func WithParallelToolExecution(enabled bool) AgentOption {
	return core.WithParallelToolExecution(enabled)
}

// WithSkillReadHook installs a hook that can rewrite skill instructions.
func WithSkillReadHook(h SkillReadHook) AgentOption { return core.WithSkillReadHook(h) }

// WithSkillToolName renames the built-in skill-loading tool.
func WithSkillToolName(name string) AgentOption { return core.WithSkillToolName(name) }

// WithSkillToolDisabled removes the built-in skill-loading tool.
func WithSkillToolDisabled() AgentOption { return core.WithSkillToolDisabled() }

// WithSubAgentToolName renames the built-in sub-agent-loading tool.
func WithSubAgentToolName(name string) AgentOption { return core.WithSubAgentToolName(name) }

// WithSubAgentToolDisabled removes the built-in sub-agent-loading tool.
func WithSubAgentToolDisabled() AgentOption { return core.WithSubAgentToolDisabled() }

// WithSubAgentEvents enables forwarding of sub-agent loop events: every event
// inside a delegated sub-agent's run is wrapped in a SubAgentEvent (with the
// sub-agent's name) and sent to the parent agent's event sink. Default off.
func WithSubAgentEvents(enabled bool) AgentOption { return core.WithSubAgentEvents(enabled) }

// Approve lets the tool call execute as requested.
func Approve() ToolDecision { return core.Approve() }

// Deny blocks the tool call; the reason is fed back to the model.
func Deny(reason string) ToolDecision { return core.Deny(reason) }

// ReplaceArgs approves the tool call with rewritten JSON arguments.
func ReplaceArgs(argsJSON string) ToolDecision { return core.ReplaceArgs(argsJSON) }

// ── Session (context window & compaction) ───────────────────────────────────

type (
	// SessionOption configures a Session.
	SessionOption = core.SessionOption
)

const (
	// DefaultContextWindow is the default context window size (in tokens) a
	// session measures context fill against.
	DefaultContextWindow = core.DefaultContextWindow
	// DefaultAutoCompactThreshold is the default context fill ratio at which
	// an auto-compacting session compacts its history.
	DefaultAutoCompactThreshold = core.DefaultAutoCompactThreshold
)

// WithContextWindow sets the context window size (in tokens) the session
// measures context fill against. Default DefaultContextWindow.
func WithContextWindow(tokens int) SessionOption { return core.WithContextWindow(tokens) }

// WithAutoCompact enables automatic history compaction once the context fill
// ratio reaches WithAutoCompactThreshold after an Ask. Default off. It never
// applies to delegated sub-agents, which run without a session.
func WithAutoCompact(enabled bool) SessionOption { return core.WithAutoCompact(enabled) }

// WithAutoCompactThreshold sets the context fill ratio (0, 1] at which
// auto-compact triggers. Default DefaultAutoCompactThreshold.
func WithAutoCompactThreshold(ratio float64) SessionOption {
	return core.WithAutoCompactThreshold(ratio)
}
