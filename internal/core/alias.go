package core

import (
	"context"
	"net/http"
	"time"

	client "github.com/great-magician-01/callable/internal/client"
	model "github.com/great-magician-01/callable/internal/model"
	provider "github.com/great-magician-01/callable/internal/provider"
)

// The unified message model lives in internal/model and the providers in
// internal/provider. These aliases and thin wrappers keep the core code that
// has not been split out yet (client, agent loop) and its tests compiling
// unchanged.

type (
	Role    = model.Role
	Message = model.Message

	Part           = model.Part
	TextPart       = model.TextPart
	ImagePart      = model.ImagePart
	ThinkingPart   = model.ThinkingPart
	ToolCallPart   = model.ToolCallPart
	ToolResultPart = model.ToolResultPart

	Request        = model.Request
	Response       = model.Response
	StopReason     = model.StopReason
	ToolCall       = model.ToolCall
	Usage          = model.Usage
	ResponseFormat = model.ResponseFormat

	Event               = model.Event
	MessageStartEvent   = model.MessageStartEvent
	ThinkingDeltaEvent  = model.ThinkingDeltaEvent
	TextDeltaEvent      = model.TextDeltaEvent
	ToolCallDeltaEvent  = model.ToolCallDeltaEvent
	MessageDoneEvent    = model.MessageDoneEvent
	TurnStartEvent      = model.TurnStartEvent
	TurnEndEvent        = model.TurnEndEvent
	ToolCallEvent       = model.ToolCallEvent
	ToolResultEvent     = model.ToolResultEvent
	AgentDoneEvent      = model.AgentDoneEvent
	SubAgentEvent       = model.SubAgentEvent
	SessionCompactEvent = model.SessionCompactEvent

	Effort   = model.Effort
	Thinking = model.Thinking

	ToolDefinition = model.ToolDefinition
	ToolResult     = model.ToolResult
	Tool           = model.Tool

	AgentResult = model.AgentResult

	resolvedImage = model.ResolvedImage
)

type eventSink = model.EventSink

const (
	RoleSystem    = model.RoleSystem
	RoleUser      = model.RoleUser
	RoleAssistant = model.RoleAssistant
	RoleTool      = model.RoleTool

	StopReasonEndTurn   = model.StopReasonEndTurn
	StopReasonToolCalls = model.StopReasonToolCalls
	StopReasonMaxTokens = model.StopReasonMaxTokens
	StopReasonOther     = model.StopReasonOther

	EffortOff    = model.EffortOff
	EffortLow    = model.EffortLow
	EffortMedium = model.EffortMedium
	EffortHigh   = model.EffortHigh
)

var resolveImage = model.ResolveImage

func System(text string) Message { return model.System(text) }

func User(parts ...any) Message { return model.User(parts...) }

func Assistant(parts ...any) Message { return model.Assistant(parts...) }

func ToolResults(results ...ToolResultPart) Message { return model.ToolResults(results...) }

func Text(text string) TextPart { return model.Text(text) }

func Image(ref string) ImagePart { return model.Image(ref) }

func ImageURL(url string) ImagePart { return model.ImageURL(url) }

func ImageBytes(data []byte, mediaType string) ImagePart { return model.ImageBytes(data, mediaType) }

func UnmarshalPart(data []byte) (Part, error) { return model.UnmarshalPart(data) }

func NewRequest(messages ...Message) *Request { return model.NewRequest(messages...) }

func JSONMode() ResponseFormat { return model.JSONMode() }

func JSONSchema(name string, schema map[string]any, strict bool) ResponseFormat {
	return model.JSONSchema(name, schema, strict)
}

func TextResult(content string) ToolResult { return model.TextResult(content) }

func ErrorResult(err error) ToolResult { return model.ErrorResult(err) }

func NewRawTool(name, description, parametersJSON string, fn func(ctx context.Context, rawArgs string) (any, error)) Tool {
	return model.NewRawTool(name, description, parametersJSON, fn)
}

// Generic functions cannot be aliased with a var; wrap them instead.

func NewTool[A any](name, description string, fn func(ctx context.Context, args A) (any, error)) Tool {
	return model.NewTool[A](name, description, fn)
}

func JSONSchemaFor[T any](name string, strict bool) ResponseFormat {
	return model.JSONSchemaFor[T](name, strict)
}

// ── Providers (internal/provider) ─────────────────────────────────────────

type (
	Provider                = provider.Provider
	Compat                  = provider.Compat
	ProviderOption          = provider.ProviderOption
	OpenAIProvider          = provider.OpenAIProvider
	OpenAIResponsesProvider = provider.OpenAIResponsesProvider
	AnthropicProvider       = provider.AnthropicProvider

	APIError = provider.APIError
)

const (
	CompatNone     = provider.CompatNone
	CompatGLM      = provider.CompatGLM
	CompatQwen     = provider.CompatQwen
	CompatDeepSeek = provider.CompatDeepSeek
	CompatArk      = provider.CompatArk

	OpenAIURL    = provider.OpenAIURL
	AnthropicURL = provider.AnthropicURL

	DeepSeekURL = provider.DeepSeekURL
	GLMURL      = provider.GLMURL
	ZAIURL      = provider.ZAIURL
	QwenURL     = provider.QwenURL
	ArkURL      = provider.ArkURL
	KimiURL     = provider.KimiURL

	DeepSeekAnthropicURL = provider.DeepSeekAnthropicURL
	GLMAnthropicURL      = provider.GLMAnthropicURL
	ZAIAnthropicURL      = provider.ZAIAnthropicURL
	KimiAnthropicURL     = provider.KimiAnthropicURL
)

func NewOpenAIProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIProvider {
	return provider.NewOpenAIProvider(apiKey, baseURL, opts...)
}

func NewOpenAIResponsesProvider(apiKey, baseURL string, opts ...ProviderOption) *OpenAIResponsesProvider {
	return provider.NewOpenAIResponsesProvider(apiKey, baseURL, opts...)
}

func NewAnthropicProvider(apiKey, baseURL string, opts ...ProviderOption) *AnthropicProvider {
	return provider.NewAnthropicProvider(apiKey, baseURL, opts...)
}

func WithHTTPClient(client *http.Client) ProviderOption { return provider.WithHTTPClient(client) }

func WithHeader(key, value string) ProviderOption { return provider.WithHeader(key, value) }

func WithRetries(n int) ProviderOption { return provider.WithRetries(n) }

func WithRetryBackoff(delays ...time.Duration) ProviderOption {
	return provider.WithRetryBackoff(delays...)
}

func WithCompat(c Compat) ProviderOption { return provider.WithCompat(c) }

// ── Client (internal/client) ───────────────────────────────────────────────

type (
	Client       = client.Client
	ClientOption = client.ClientOption
	RequestHook  = client.RequestHook
	ResponseHook = client.ResponseHook
)

func NewClient(p Provider, opts ...ClientOption) *Client { return client.NewClient(p, opts...) }

func NewOpenAIClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return client.NewOpenAIClient(apiKey, baseURL, model, opts...)
}

func NewOpenAIResponsesClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return client.NewOpenAIResponsesClient(apiKey, baseURL, model, opts...)
}

func NewAnthropicClient(apiKey, baseURL, model string, opts ...ClientOption) *Client {
	return client.NewAnthropicClient(apiKey, baseURL, model, opts...)
}

func WithModel(model string) ClientOption { return client.WithModel(model) }

func WithMaxTokens(n int) ClientOption { return client.WithMaxTokens(n) }

func WithTemperature(v float64) ClientOption { return client.WithTemperature(v) }

func WithTopP(v float64) ClientOption { return client.WithTopP(v) }

func WithStopSequences(seq ...string) ClientOption { return client.WithStopSequences(seq...) }

func WithResponseFormat(f ResponseFormat) ClientOption { return client.WithResponseFormat(f) }

func WithExtra(key string, value any) ClientOption { return client.WithExtra(key, value) }

func WithRequestHook(hooks ...RequestHook) ClientOption { return client.WithRequestHook(hooks...) }

func WithResponseHook(hooks ...ResponseHook) ClientOption { return client.WithResponseHook(hooks...) }
