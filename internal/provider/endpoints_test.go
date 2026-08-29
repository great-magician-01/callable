package provider

import (
	"net/url"
	"testing"
)

// TestEndpointURLs pins the exact well-known endpoint URLs (a typo here breaks
// real calls) and verifies each is an absolute https URL.
func TestEndpointURLs(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"OpenAIURL", OpenAIURL, "https://api.openai.com/v1"},
		{"AnthropicURL", AnthropicURL, "https://api.anthropic.com"},
		{"DeepSeekURL", DeepSeekURL, "https://api.deepseek.com"},
		{"DeepSeekAnthropicURL", DeepSeekAnthropicURL, "https://api.deepseek.com/anthropic"},
		{"GLMURL", GLMURL, "https://open.bigmodel.cn/api/paas/v4"},
		{"GLMAnthropicURL", GLMAnthropicURL, "https://open.bigmodel.cn/api/anthropic"},
		{"ZAIURL", ZAIURL, "https://api.z.ai/api/paas/v4"},
		{"ZAIAnthropicURL", ZAIAnthropicURL, "https://api.z.ai/api/anthropic"},
		{"QwenURL", QwenURL, "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"ArkURL", ArkURL, "https://ark.cn-beijing.volces.com/api/v3"},
		{"KimiURL", KimiURL, "https://api.moonshot.cn/v1"},
		{"KimiAnthropicURL", KimiAnthropicURL, "https://api.moonshot.cn/anthropic"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
		u, err := url.Parse(c.got)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("%s = %q is not an absolute https URL", c.name, c.got)
		}
	}
}

// TestEndpointCompatDetection ensures every well-known URL is recognized by
// detectCompat, so endpoint dialects (thinking controls etc.) work out of the
// box when a built-in constant is passed to a constructor.
func TestEndpointCompatDetection(t *testing.T) {
	cases := []struct {
		name string
		base string
		want Compat
	}{
		{"OpenAIURL", OpenAIURL, CompatNone},
		{"AnthropicURL", AnthropicURL, CompatNone},
		{"DeepSeekURL", DeepSeekURL, CompatDeepSeek},
		{"DeepSeekAnthropicURL", DeepSeekAnthropicURL, CompatDeepSeek},
		{"GLMURL", GLMURL, CompatGLM},
		{"GLMAnthropicURL", GLMAnthropicURL, CompatGLM},
		{"ZAIURL", ZAIURL, CompatGLM},
		{"ZAIAnthropicURL", ZAIAnthropicURL, CompatGLM},
		{"QwenURL", QwenURL, CompatQwen},
		{"ArkURL", ArkURL, CompatArk},
		{"KimiURL", KimiURL, CompatNone},
		{"KimiAnthropicURL", KimiAnthropicURL, CompatNone},
	}
	for _, c := range cases {
		if got := detectCompat(c.base); got != c.want {
			t.Errorf("detectCompat(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestEndpointOpenAIOfficial ensures the built-in OpenAI URL is treated as the
// official endpoint (newer parameter names, stream usage options).
func TestEndpointOpenAIOfficial(t *testing.T) {
	if !isOfficialOpenAI(OpenAIURL) {
		t.Error("isOfficialOpenAI(OpenAIURL) = false, want true")
	}
}

// TestAnthropicProviderWellKnownEndpoint ensures an Anthropic-compatible
// third-party constant assembles the correct messages URL.
func TestAnthropicProviderWellKnownEndpoint(t *testing.T) {
	p := NewAnthropicProvider("key", DeepSeekAnthropicURL)
	got := p.api.cfg.baseURL + p.endpoint()
	want := "https://api.deepseek.com/anthropic/v1/messages"
	if got != want {
		t.Errorf("messages URL = %q, want %q", got, want)
	}
}
