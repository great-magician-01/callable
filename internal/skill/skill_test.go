package skill

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSkillIndexBlock(t *testing.T) {
	block := IndexBlock("read_skill", []Skill{
		NewSkill("pdf", "Export PDFs", "instructions A"),
		NewSkill("search", "Search the web", "instructions B"),
	})
	for _, want := range []string{"read_skill", "pdf: Export PDFs", "search: Search the web"} {
		if !strings.Contains(block, want) {
			t.Errorf("index block missing %q:\n%s", want, block)
		}
	}
	// Full instructions must NOT be in the index (progressive disclosure).
	if strings.Contains(block, "instructions A") {
		t.Errorf("index block leaked full instructions:\n%s", block)
	}
}

func TestSkillTool(t *testing.T) {
	skills := []Skill{NewSkill("pdf", "Export PDFs", "# Full PDF instructions")}
	tool := NewReadTool(DefaultSkillToolName, skills, nil)

	res := tool.Execute(context.Background(), `{"name":"pdf"}`)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "# Full PDF instructions" {
		t.Errorf("content = %q", res.Content)
	}

	// Unknown skill -> error listing available skills.
	res = tool.Execute(context.Background(), `{"name":"nope"}`)
	if !res.IsError || !strings.Contains(res.Content, "pdf") {
		t.Errorf("unknown skill result = %+v", res)
	}
}

func TestSkillToolHook(t *testing.T) {
	skills := []Skill{NewSkill("pdf", "Export PDFs", "base instructions")}
	hook := func(ctx context.Context, name, content string) (string, error) {
		return content + " +extra", nil
	}
	tool := NewReadTool("load_skill", skills, hook)

	def := tool.Definition()
	if def.Name != "load_skill" {
		t.Errorf("tool name = %q", def.Name)
	}
	res := tool.Execute(context.Background(), `{"name":"pdf"}`)
	if res.Content != "base instructions +extra" {
		t.Errorf("content = %q, want hook-modified content", res.Content)
	}
}

func TestSkillToolHookError(t *testing.T) {
	skills := []Skill{NewSkill("pdf", "Export PDFs", "base")}
	hook := func(ctx context.Context, name, content string) (string, error) {
		return "", errors.New("backend down")
	}
	tool := NewReadTool(DefaultSkillToolName, skills, hook)
	res := tool.Execute(context.Background(), `{"name":"pdf"}`)
	if !res.IsError || !strings.Contains(res.Content, "backend down") {
		t.Errorf("hook error result = %+v", res)
	}
}
