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
// implementation lives in the internal sub-packages (model, provider, client,
// skill, agent) and is re-exported here.
package callable

import (
	"context"
	"net/http"
	"time"

	agent "github.com/great-magician-01/callable/internal/agent"
	client "github.com/great-magician-01/callable/internal/client"
	model "github.com/great-magician-01/callable/internal/model"
	provider "github.com/great-magician-01/callable/internal/provider"
	skill "github.com/great-magician-01/callable/internal/skill"
)

// ── Messages and content parts ─────────────────────────────────────────────

type (
	// Role identifies who authored a message.
	Role = model.Role
	// Message is a provider-agnostic chat message; its Parts may mix text,
	// images, thinking, tool calls and tool results.
	Message = model.Message

	// Part is one piece of message content (sealed interface).
	Part = model.Part
	// TextPart is a piece of text.
	TextPart = model.TextPart
	// ImagePart is an image given by path, URL or raw bytes.
	ImagePart = model.ImagePart
	// ThinkingPart is a piece of model reasoning.
	ThinkingPart = model.ThinkingPart
	// ToolCallPart is a tool invocation requested by the model.
	ToolCallPart = model.ToolCallPart
	// ToolResultPart carries the output of a tool execution back to the model.
	ToolResultPart = model.ToolResultPart
	// RawPart preserves a provider content block the unified model does not
	// understand, in its original wire format; Anthropic and OpenAI
	// Responses replay it verbatim on the next request.
	RawPart = model.RawPart
)

// Message roles.
const (
	RoleSystem    = model.RoleSystem
	RoleUser      = model.RoleUser
	RoleAssistant = model.RoleAssistant
	RoleTool      = model.RoleTool
)

// System builds a system message.
func System(text string) Message { return model.System(text) }

// User builds a user message from strings and/or Parts.
func User(parts ...any) Message { return model.User(parts...) }

// Assistant builds an assistant message from strings and/or Parts.
func Assistant(parts ...any) Message { return model.Assistant(parts...) }

// ToolResults builds a message carrying tool execution results.
func ToolResults(results ...ToolResultPart) Message { return model.ToolResults(results...) }

// Text builds a TextPart.
func Text(text string) TextPart { return model.Text(text) }

// Image references an image by file path or http(s) URL; local files are read
// and converted to the provider's native image format at send time.
func Image(ref string) ImagePart { return model.Image(ref) }

// ImageURL references an image by http(s) URL.
func ImageURL(url string) ImagePart { return model.ImageURL(url) }

// ImageBytes wraps raw image bytes with an explicit media type.
func ImageBytes(data []byte, mediaType string) ImagePart { return model.ImageBytes(data, mediaType) }

// UnmarshalPart decodes a single serialized Part.
func UnmarshalPart(data []byte) (Part, error) { return model.UnmarshalPart(data) }

// ── Requests, responses, usage ─────────────────────────────────────────────

type (
	// Request is a single provider-agnostic model call.
	Request = model.Request
	// Response is the assembled result of a model call.
	Response = model.Response
	// StopReason explains why generation stopped.
	StopReason = model.StopReason
	// ToolCall is a single tool invocation requested by the model.
	ToolCall = model.ToolCall
	// Usage reports token consumption of a call (or an accumulated run).
	Usage = model.Usage
	// ResponseFormat constrains the model's output format (structured
	// output); see JSONMode, JSONSchema and JSONSchemaFor.
	ResponseFormat = model.ResponseFormat
)

// Stop reasons.
const (
	StopReasonEndTurn   = model.StopReasonEndTurn
	StopReasonToolCalls = model.StopReasonToolCalls
	StopReasonMaxTokens = model.StopReasonMaxTokens
	StopReasonOther     = model.StopReasonOther
)

// NewRequest builds a Request from messages; configure it with its With* methods.
func NewRequest(messages ...Message) *Request { return model.NewRequest(messages...) }

// JSONMode requests free-form JSON output: the model answers with a JSON
// value of any shape. Set it with Request.WithResponseFormat.
func JSONMode() ResponseFormat { return model.JSONMode() }

// JSONSchema requests output conforming to the given JSON Schema. Set it with
// Request.WithResponseFormat.
func JSONSchema(name string, schema map[string]any, strict bool) ResponseFormat {
	return model.JSONSchema(name, schema, strict)
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
	return model.JSONSchemaFor[T](name, strict)
}

// ── Streaming events ───────────────────────────────────────────────────────

type (
	// Event is a streaming event (sealed interface); handle with a type switch.
	Event = model.Event
	// MessageStartEvent marks the beginning of an assistant message.
	MessageStartEvent = model.MessageStartEvent
	// ThinkingDeltaEvent carries an increment of reasoning text.
	ThinkingDeltaEvent = model.ThinkingDeltaEvent
	// TextDeltaEvent carries an increment of the answer text.
	TextDeltaEvent = model.TextDeltaEvent
	// ToolCallDeltaEvent carries an increment of a streamed tool call.
	ToolCallDeltaEvent = model.ToolCallDeltaEvent
	// MessageDoneEvent marks the end of an assistant message.
	MessageDoneEvent = model.MessageDoneEvent
	// TurnStartEvent marks the beginning of an agent loop turn.
	TurnStartEvent = model.TurnStartEvent
	// TurnEndEvent marks the end of a turn.
	TurnEndEvent = model.TurnEndEvent
	// ToolCallEvent is emitted just before an approved tool executes.
	ToolCallEvent = model.ToolCallEvent
	// ToolResultEvent is emitted after a tool finished executing.
	ToolResultEvent = model.ToolResultEvent
	// AgentDoneEvent is emitted when the agent loop finished.
	AgentDoneEvent = model.AgentDoneEvent
	// SubAgentEvent wraps an event emitted inside a delegated sub-agent's
	// loop; only produced when WithSubAgentEvents is enabled.
	SubAgentEvent = model.SubAgentEvent
	// SessionCompactEvent is emitted after a session auto-compacts its
	// history.
	SessionCompactEvent = model.SessionCompactEvent
)

// ── Thinking / reasoning ───────────────────────────────────────────────────

type (
	// Effort is a unified thinking-effort level mapped to each provider's
	// native controls.
	Effort = model.Effort
	// Thinking configures thinking/reasoning for a request or agent.
	Thinking = model.Thinking
)

// Effort levels.
const (
	EffortOff    = model.EffortOff
	EffortLow    = model.EffortLow
	EffortMedium = model.EffortMedium
	EffortHigh   = model.EffortHigh
)

// ── Errors ─────────────────────────────────────────────────────────────────

type (
	// APIError is a non-2xx provider response or a transport failure.
	APIError = provider.APIError
	// MaxTurnsError is returned when an agent run exceeds its turn limit; its
	// Partial field carries the partial result.
	MaxTurnsError = agent.MaxTurnsError
)

// ── Tools ──────────────────────────────────────────────────────────────────

type (
	// ToolDefinition is the schema advertised to the model.
	ToolDefinition = model.ToolDefinition
	// ToolResult is the outcome of a tool execution.
	ToolResult = model.ToolResult
	// Tool is a callable exposed to the model.
	Tool = model.Tool
)

// NewTool creates a Tool from a handler whose argument struct is reflected
// into a JSON Schema. Describe parameters with `jsonschema` struct tags.
func NewTool[A any](name, description string, fn func(ctx context.Context, args A) (any, error)) Tool {
	return model.NewTool[A](name, description, fn)
}

// NewRawTool creates a Tool with a hand-written JSON Schema; the handler
// receives the raw JSON arguments string.
func NewRawTool(name, description, parametersJSON string, fn func(ctx context.Context, rawArgs string) (any, error)) Tool {
	return model.NewRawTool(name, description, parametersJSON, fn)
}

// TextResult wraps a plain-text tool output.
func TextResult(content string) ToolResult { return model.TextResult(content) }

// ErrorResult wraps an error as a failed tool result.
func ErrorResult(err error) ToolResult { return model.ErrorResult(err) }

// ── Web search ─────────────────────────────────────────────────────────────

// DefaultWebSearchToolName is the name of the Tavily-backed fallback
// web-search tool.
const DefaultWebSearchToolName = agent.DefaultWebSearchToolName

// WithWebSearch explicitly enables or disables the web-search tool.
//
// The default (option not given) is "auto": web search is enabled when the
// provider endpoint has built-in server-side search (Kimi, GLM/Z.AI, Qwen,
// api.anthropic.com, OpenAI Responses) or when a Tavily API key is configured
// via WithTavilyAPIKey, and disabled otherwise. A provider's built-in search
// is always preferred over the Tavily fallback. Enabling explicitly with no
// built-in support and no Tavily key exposes no tool.
func WithWebSearch(enabled bool) AgentOption { return agent.WithWebSearch(enabled) }

// WithTavilyAPIKey configures a Tavily API key used for the fallback
// web-search tool (https://tavily.com). It is only used when the provider
// endpoint has no built-in web search.
func WithTavilyAPIKey(key string) AgentOption { return agent.WithTavilyAPIKey(key) }

// ── Skills (progressive disclosure) ────────────────────────────────────────

type (
	// Skill is a named instruction set the model can load on demand.
	Skill = skill.Skill
	// SkillReadHook can rewrite skill instructions before they reach the model.
	SkillReadHook = skill.SkillReadHook
)

// DefaultSkillToolName is the name of the built-in skill-loading tool.
const DefaultSkillToolName = skill.DefaultSkillToolName

// NewSkill builds a Skill.
func NewSkill(name, description, instructions string) Skill {
	return skill.NewSkill(name, description, instructions)
}

// ── Sub-agents (delegation with progressive disclosure) ────────────────────

type (
	// SubAgent is a named agent definition the parent agent can delegate
	// subtasks to. It is not exposed as a tool until the model loads it via
	// the built-in load_agent tool.
	SubAgent = agent.SubAgent
	// SubAgentOption configures a SubAgent.
	SubAgentOption = agent.SubAgentOption
)

// DefaultSubAgentLoadToolName is the name of the built-in sub-agent-loading
// tool.
const DefaultSubAgentLoadToolName = agent.DefaultSubAgentLoadToolName

// NewSubAgent builds a SubAgent definition; register it with WithSubAgents.
func NewSubAgent(name, description string, opts ...SubAgentOption) SubAgent {
	return agent.NewSubAgent(name, description, opts...)
}

// WithSubAgentClient gives the sub-agent its own client (e.g. a different
// provider) instead of inheriting the parent agent's client.
func WithSubAgentClient(client *Client) SubAgentOption { return agent.WithSubAgentClient(client) }

// WithSubAgentModel overrides the sub-agent's model while reusing the parent
// agent's client. Ignored when WithSubAgentClient supplies a custom client.
func WithSubAgentModel(model string) SubAgentOption { return agent.WithSubAgentModel(model) }

// WithSubAgentPrompt sets the sub-agent's system prompt.
func WithSubAgentPrompt(prompt string) SubAgentOption { return agent.WithSubAgentPrompt(prompt) }

// WithSubAgentTools registers tools available inside the sub-agent loop.
func WithSubAgentTools(tools ...Tool) SubAgentOption { return agent.WithSubAgentTools(tools...) }

// WithSubAgentSkills registers skills available inside the sub-agent loop.
func WithSubAgentSkills(skills ...Skill) SubAgentOption { return agent.WithSubAgentSkills(skills...) }

// WithSubAgentThinking configures thinking/reasoning for the sub-agent.
func WithSubAgentThinking(t Thinking) SubAgentOption { return agent.WithSubAgentThinking(t) }

// WithSubAgentMaxTurns caps the number of model calls per sub-agent run.
func WithSubAgentMaxTurns(n int) SubAgentOption { return agent.WithSubAgentMaxTurns(n) }

// ── Providers ──────────────────────────────────────────────────────────────

type (
	// Provider converts unified requests to a specific wire format.
	Provider = provider.Provider
	// Compat is a bitmask of OpenAI-compatible endpoint dialects.
	Compat = provider.Compat
	// ProviderOption configures a provider.
	ProviderOption = provider.ProviderOption

	// OpenAIProvider talks the OpenAI Chat Completions format.
	OpenAIProvider = provider.OpenAIProvider
	// OpenAIResponsesProvider talks the OpenAI Responses format.
	OpenAIResponsesProvider = provider.OpenAIResponsesProvider
	// AnthropicProvider talks the Anthropic Messages format.
	AnthropicProvider = provider.AnthropicProvider
)

// Endpoint dialects (auto-detected from the base URL, or set with WithCompat).
const (
	CompatNone     = provider.CompatNone
	CompatGLM      = provider.CompatGLM
	CompatQwen     = provider.CompatQwen
	CompatDeepSeek = provider.CompatDeepSeek
	CompatArk      = provider.CompatArk
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
	OpenAIURL = provider.OpenAIURL
	// AnthropicURL is the official Anthropic API root.
	AnthropicURL = provider.AnthropicURL

	// DeepSeekURL is DeepSeek's OpenAI-compatible endpoint.
	DeepSeekURL = provider.DeepSeekURL
	// GLMURL is Zhipu GLM's (bigmodel.cn) OpenAI-compatible endpoint.
	GLMURL = provider.GLMURL
	// ZAIURL is Z.AI's OpenAI-compatible endpoint.
	ZAIURL = provider.ZAIURL
	// QwenURL is Alibaba DashScope's OpenAI-compatible endpoint.
	QwenURL = provider.QwenURL
	// ArkURL is Volcano Ark's OpenAI-compatible endpoint.
	ArkURL = provider.ArkURL
	// KimiURL is Moonshot AI's (Kimi) OpenAI-compatible endpoint for the
	// China platform. The international platform mirrors it at
	// https://api.moonshot.ai/v1.
	KimiURL = provider.KimiURL

	// DeepSeekAnthropicURL is DeepSeek's Anthropic-compatible endpoint.
	DeepSeekAnthropicURL = provider.DeepSeekAnthropicURL
	// GLMAnthropicURL is Zhipu GLM's (bigmodel.cn) Anthropic-compatible
	// endpoint.
	GLMAnthropicURL = provider.GLMAnthropicURL
	// ZAIAnthropicURL is Z.AI's Anthropic-compatible endpoint.
	ZAIAnthropicURL = provider.ZAIAnthropicURL
	// KimiAnthropicURL is Moonshot AI's (Kimi) Anthropic-compatible endpoint
	// for the China platform. The international platform mirrors it at
	// https://api.moonshot.ai/anthropic.
	KimiAnthropicURL = provider.KimiAnthropicURL
)

// NewOpenAIProvider creates an OpenAI Chat Completions provider for the given
// endpoint. baseURL is the API root including any version prefix; any
// OpenAI-compatible endpoint (GLM, DeepSeek, Qwen, ...) works. Well-known
// endpoints are available as constants, e.g. OpenAIURL or DeepSeekURL.
func NewOpenAIProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIProvider {
	return provider.NewOpenAIProvider(apiKey, baseURL, opts...)
}

// NewOpenAIResponsesProvider creates an OpenAI Responses provider for the
// given endpoint. baseURL is the API root including any version prefix, e.g.
// OpenAIURL.
func NewOpenAIResponsesProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIResponsesProvider {
	return provider.NewOpenAIResponsesProvider(apiKey, baseURL, opts...)
}

// NewAnthropicProvider creates an Anthropic Messages provider for the given
// endpoint, e.g. AnthropicURL. A baseURL already ending in /v1 is tolerated.
// Anthropic-compatible third-party endpoints are available as constants, e.g.
// DeepSeekAnthropicURL.
func NewAnthropicProvider(apiKey, baseURL string, opts ...ProviderOption) *AnthropicProvider {
	return provider.NewAnthropicProvider(apiKey, baseURL, opts...)
}

// WithHTTPClient supplies a custom *http.Client.
func WithHTTPClient(client *http.Client) ProviderOption { return provider.WithHTTPClient(client) }

// WithHeader adds a header to every provider request (applied after
// authentication; request-level Request.WithHeader wins on key conflicts).
func WithHeader(key, value string) ProviderOption { return provider.WithHeader(key, value) }

// WithRetries sets how many times transient failures (429, 5xx, network
// errors) are retried, waiting 3s, 10s, then 30s between attempts (see
// WithRetryBackoff to change the schedule). Default 3; pass 0 to disable.
func WithRetries(n int) ProviderOption { return provider.WithRetries(n) }

// WithRetryBackoff replaces the default retry wait schedule (3s, 10s, 30s):
// delays[i] is the wait before retry i+1, and attempts beyond the schedule
// reuse the last delay.
func WithRetryBackoff(delays ...time.Duration) ProviderOption {
	return provider.WithRetryBackoff(delays...)
}

// WithCompat overrides the auto-detected endpoint dialect.
func WithCompat(c Compat) ProviderOption { return provider.WithCompat(c) }

// ── Client ─────────────────────────────────────────────────────────────────

type (
	// Client is a thin wrapper around a Provider applying model defaults.
	Client = client.Client
	// ClientOption configures a Client.
	ClientOption = client.ClientOption
	// RequestHook observes every request right before it is sent (after
	// client defaults are applied). Use it for logging, tracing or metrics.
	RequestHook = client.RequestHook
	// ResponseHook observes every finished request with exactly what the
	// provider returned. Use it for logging, tracing or cost accounting.
	ResponseHook = client.ResponseHook
)

// NewClient builds a Client on top of a provider.
func NewClient(provider Provider, opts ...ClientOption) *Client {
	return client.NewClient(provider, opts...)
}

// NewOpenAIClient is a shortcut for NewClient(NewOpenAIProvider(...)) with
// the model set: the common case folded into one call.
//
//	client := callable.NewOpenAIClient(apiKey, callable.DeepSeekURL, "deepseek-v4")
//
// Use the two-step form (NewClient + NewOpenAIProvider) when you need
// ProviderOptions such as WithRetries or WithHTTPClient.
func NewOpenAIClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return client.NewOpenAIClient(apiKey, baseURL, model, opts...)
}

// NewOpenAIResponsesClient is NewOpenAIClient for the OpenAI Responses
// format.
func NewOpenAIResponsesClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return client.NewOpenAIResponsesClient(apiKey, baseURL, model, opts...)
}

// NewAnthropicClient is NewOpenAIClient for the Anthropic Messages format.
func NewAnthropicClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return client.NewAnthropicClient(apiKey, baseURL, model, opts...)
}

// WithModel sets the default model.
func WithModel(model string) ClientOption { return client.WithModel(model) }

// WithMaxTokens sets the default max output tokens.
func WithMaxTokens(n int) ClientOption { return client.WithMaxTokens(n) }

// WithTemperature sets the default sampling temperature.
func WithTemperature(v float64) ClientOption { return client.WithTemperature(v) }

// WithTopP sets the default nucleus-sampling probability mass.
func WithTopP(v float64) ClientOption { return client.WithTopP(v) }

// WithStopSequences sets default stop sequences that end generation.
// Providers without stop-sequence support (OpenAI Responses) ignore them.
func WithStopSequences(seq ...string) ClientOption { return client.WithStopSequences(seq...) }

// WithResponseFormat sets the default output format (structured output).
func WithResponseFormat(f ResponseFormat) ClientOption { return client.WithResponseFormat(f) }

// WithExtra merges a provider-specific top-level field into every request
// body this client sends (e.g. a gateway dialect flag). Request-level
// Request.WithExtra wins on key conflicts.
func WithExtra(key string, value any) ClientOption { return client.WithExtra(key, value) }

// WithClientHeader adds an HTTP header to every request this client sends
// (e.g. a gateway tenant tag), including the Agent loop's internal calls.
// Request-level Request.WithHeader wins on key conflicts, and both win over
// the provider-level WithHeader ProviderOption. (Named WithClientHeader
// because WithHeader is the provider-level option.)
func WithClientHeader(key, value string) ClientOption { return client.WithHeader(key, value) }

// WithRequestHook registers a hook invoked before every request is sent.
// Multiple hooks run in registration order.
func WithRequestHook(hooks ...RequestHook) ClientOption { return client.WithRequestHook(hooks...) }

// WithResponseHook registers a hook invoked after every request finishes.
// Multiple hooks run in registration order.
func WithResponseHook(hooks ...ResponseHook) ClientOption { return client.WithResponseHook(hooks...) }

// ── Agent ──────────────────────────────────────────────────────────────────

type (
	// Agent runs the full tool-calling loop automatically.
	Agent = agent.Agent
	// AgentOption configures an Agent.
	AgentOption = agent.AgentOption
	// AgentResult is the outcome of an agent run.
	AgentResult = model.AgentResult
	// ToolDecision is the verdict of a ToolCallHook.
	ToolDecision = agent.ToolDecision
	// ToolCallHook can approve, deny or rewrite a tool call before execution.
	ToolCallHook = agent.ToolCallHook
	// Session keeps conversation history across multiple agent runs.
	Session = agent.Session
)

// Agent result stop reasons.
const (
	// AgentCompleted means the model produced a final answer.
	AgentCompleted = agent.AgentCompleted
	// AgentMaxTurns means the loop hit the turn limit without a final answer.
	AgentMaxTurns = agent.AgentMaxTurns
)

// NewAgent builds an Agent around a Client.
func NewAgent(client *Client, opts ...AgentOption) *Agent {
	return agent.NewAgent(client, opts...)
}

// WithSystemPrompt sets the agent's system prompt.
func WithSystemPrompt(prompt string) AgentOption { return agent.WithSystemPrompt(prompt) }

// WithTools registers tools the agent may call.
func WithTools(tools ...Tool) AgentOption { return agent.WithTools(tools...) }

// WithSkills registers skills for progressive disclosure.
func WithSkills(skills ...Skill) AgentOption { return agent.WithSkills(skills...) }

// WithSubAgents registers sub-agent definitions. They are not exposed as
// tools by default: the system prompt only lists name/description, and the
// model must call the built-in load_agent tool to register a call_<name>
// tool before delegating to one.
func WithSubAgents(subs ...SubAgent) AgentOption { return agent.WithSubAgents(subs...) }

// WithThinking configures thinking/reasoning for the agent.
func WithThinking(t Thinking) AgentOption { return agent.WithThinking(t) }

// WithMaxTurns bounds the number of model<->tool loop turns.
func WithMaxTurns(n int) AgentOption { return agent.WithMaxTurns(n) }

// WithToolCallHook installs a hook invoked before every tool execution.
func WithToolCallHook(h ToolCallHook) AgentOption { return agent.WithToolCallHook(h) }

// WithParallelToolExecution allows concurrent tool execution within a turn.
func WithParallelToolExecution(enabled bool) AgentOption {
	return agent.WithParallelToolExecution(enabled)
}

// WithSkillReadHook installs a hook that can rewrite skill instructions.
func WithSkillReadHook(h SkillReadHook) AgentOption { return agent.WithSkillReadHook(h) }

// WithSkillToolName renames the built-in skill-loading tool.
func WithSkillToolName(name string) AgentOption { return agent.WithSkillToolName(name) }

// WithSkillToolDisabled removes the built-in skill-loading tool.
func WithSkillToolDisabled() AgentOption { return agent.WithSkillToolDisabled() }

// WithSubAgentToolName renames the built-in sub-agent-loading tool.
func WithSubAgentToolName(name string) AgentOption { return agent.WithSubAgentToolName(name) }

// WithSubAgentToolDisabled removes the built-in sub-agent-loading tool.
func WithSubAgentToolDisabled() AgentOption { return agent.WithSubAgentToolDisabled() }

// WithSubAgentEvents enables forwarding of sub-agent loop events: every event
// inside a delegated sub-agent's run is wrapped in a SubAgentEvent (with the
// sub-agent's name) and sent to the parent agent's event sink. Default off.
func WithSubAgentEvents(enabled bool) AgentOption { return agent.WithSubAgentEvents(enabled) }

// Approve lets the tool call execute as requested.
func Approve() ToolDecision { return agent.Approve() }

// Deny blocks the tool call; the reason is fed back to the model.
func Deny(reason string) ToolDecision { return agent.Deny(reason) }

// ReplaceArgs approves the tool call with rewritten JSON arguments.
func ReplaceArgs(argsJSON string) ToolDecision { return agent.ReplaceArgs(argsJSON) }

// ── Session (context window & compaction) ───────────────────────────────────

type (
	// SessionOption configures a Session.
	SessionOption = agent.SessionOption
)

const (
	// DefaultContextWindow is the default context window size (in tokens) a
	// session measures context fill against.
	DefaultContextWindow = agent.DefaultContextWindow
	// DefaultAutoCompactThreshold is the default context fill ratio at which
	// an auto-compacting session compacts its history.
	DefaultAutoCompactThreshold = agent.DefaultAutoCompactThreshold
)

// WithContextWindow sets the context window size (in tokens) the session
// measures context fill against. Default DefaultContextWindow.
func WithContextWindow(tokens int) SessionOption { return agent.WithContextWindow(tokens) }

// WithAutoCompact enables automatic history compaction once the context fill
// ratio reaches WithAutoCompactThreshold after an Ask. Default off. It never
// applies to delegated sub-agents, which run without a session.
func WithAutoCompact(enabled bool) SessionOption { return agent.WithAutoCompact(enabled) }

// WithAutoCompactThreshold sets the context fill ratio (0, 1] at which
// auto-compact triggers. Default DefaultAutoCompactThreshold.
func WithAutoCompactThreshold(ratio float64) SessionOption {
	return agent.WithAutoCompactThreshold(ratio)
}
