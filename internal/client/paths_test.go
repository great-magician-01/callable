package client

import (
	"context"
	"testing"

	. "github.com/great-magician-01/callable/internal/model"
	provider "github.com/great-magician-01/callable/internal/provider"
	. "github.com/great-magician-01/callable/internal/testutil"
)

// TestAntCreatePath exercises the non-streaming Anthropic code path.
func TestAntCreatePath(t *testing.T) {
	jsonResp := `{"content":[{"type":"thinking","thinking":"deep","signature":"sigX"},
		{"type":"text","text":"done"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":5,"output_tokens":7}}`
	server := NewMockJSONServer(t, []string{jsonResp}, nil)
	client := NewClient(provider.NewAnthropicProvider("k", server.URL), WithModel("claude-x"))

	resp, err := client.Create(context.Background(), NewRequest(User("hi")).WithThinking(Thinking{Effort: EffortLow}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "done" {
		t.Errorf("text = %q", resp.Text)
	}
	tp, ok := resp.Message.Parts[0].(ThinkingPart)
	if !ok || tp.Signature != "sigX" {
		t.Errorf("thinking part = %+v", resp.Message.Parts[0])
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 7 ||
		resp.Usage.ContextTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

// TestResponsesCreatePath exercises the non-streaming Responses code path.
func TestResponsesCreatePath(t *testing.T) {
	jsonResp := `{"id":"resp_1","status":"completed","output":[
		{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"pondered"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}
	],"usage":{"input_tokens":5,"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2}}}`
	server := NewMockJSONServer(t, []string{jsonResp}, nil)
	client := NewClient(provider.NewOpenAIResponsesProvider("k", server.URL), WithModel("gpt-x"))

	resp, err := client.Create(context.Background(), NewRequest(User("hi")).WithThinking(Thinking{Effort: EffortLow}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "done" {
		t.Errorf("text = %q", resp.Text)
	}
	if got := resp.Message.Thinking(); got != "pondered" {
		t.Errorf("thinking = %q", got)
	}
	if resp.Message.ProviderExtra(client.Provider().Name()) == nil {
		t.Errorf("reasoning item not preserved for replay")
	}
	if resp.Usage.ReasoningTokens != 2 || resp.Usage.ContextTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}
