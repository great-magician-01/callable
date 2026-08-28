package core

// Well-known endpoint base URLs. Pass one as the baseURL argument of the
// provider constructors instead of spelling out the address:
//
//	NewAnthropicProvider(apiKey, DeepSeekAnthropicURL)
//	NewOpenAIProvider(apiKey, DeepSeekURL)
//
// detectCompat recognizes these URLs, so endpoint dialects (thinking controls
// etc.) work out of the box. Any other OpenAI- or Anthropic-compatible
// endpoint can still be passed as a literal URL.
const (
	// OpenAIURL is the official OpenAI API root (Chat Completions and
	// Responses).
	OpenAIURL = "https://api.openai.com/v1"
	// AnthropicURL is the official Anthropic API root.
	AnthropicURL = "https://api.anthropic.com"

	// DeepSeekURL is DeepSeek's OpenAI-compatible endpoint. (DeepSeek also
	// accepts a /v1 suffix purely for OpenAI SDK convention; the canonical
	// documented base URL omits it.)
	DeepSeekURL = "https://api.deepseek.com"
	// GLMURL is Zhipu GLM's (bigmodel.cn) OpenAI-compatible endpoint.
	GLMURL = "https://open.bigmodel.cn/api/paas/v4"
	// ZAIURL is Z.AI's OpenAI-compatible endpoint.
	ZAIURL = "https://api.z.ai/api/paas/v4"
	// QwenURL is Alibaba DashScope's OpenAI-compatible endpoint.
	QwenURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	// ArkURL is Volcano Ark's OpenAI-compatible endpoint.
	ArkURL = "https://ark.cn-beijing.volces.com/api/v3"
	// KimiURL is Moonshot AI's (Kimi) OpenAI-compatible endpoint for the
	// China platform. The international platform mirrors it at
	// https://api.moonshot.ai/v1.
	KimiURL = "https://api.moonshot.cn/v1"

	// DeepSeekAnthropicURL is DeepSeek's Anthropic-compatible endpoint.
	DeepSeekAnthropicURL = "https://api.deepseek.com/anthropic"
	// GLMAnthropicURL is Zhipu GLM's (bigmodel.cn) Anthropic-compatible
	// endpoint.
	GLMAnthropicURL = "https://open.bigmodel.cn/api/anthropic"
	// ZAIAnthropicURL is Z.AI's Anthropic-compatible endpoint.
	ZAIAnthropicURL = "https://api.z.ai/api/anthropic"
	// KimiAnthropicURL is Moonshot AI's (Kimi) Anthropic-compatible endpoint
	// for the China platform. The international platform mirrors it at
	// https://api.moonshot.ai/anthropic.
	KimiAnthropicURL = "https://api.moonshot.cn/anthropic"
)
