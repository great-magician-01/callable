package core

import (
	"context"
	"fmt"
	"strings"
)

// Skill is an instruction package exposed to the model through progressive
// disclosure: only the Name and Description are injected into the system
// prompt; the full Instructions are served on demand via the built-in
// read_skill tool when the model asks for them.
type Skill struct {
	// Name is the short identifier the model uses with read_skill.
	Name string
	// Description is a one-line summary shown in the system prompt index.
	// This is what the model uses to decide whether to load the skill.
	Description string
	// Instructions is the full skill body (usually Markdown).
	Instructions string
}

// NewSkill creates a Skill.
func NewSkill(name, description, instructions string) Skill {
	return Skill{Name: name, Description: description, Instructions: instructions}
}

// SkillReadHook is invoked before a skill's instructions are returned to the
// model by the built-in read_skill tool. It may rewrite the content (e.g.
// append runtime context, lazily load extra files) or return an error, which
// becomes a failed tool result the model can react to.
type SkillReadHook func(ctx context.Context, name, content string) (string, error)

// DefaultSkillToolName is the name of the built-in skill-loading tool.
const DefaultSkillToolName = "read_skill"

// skillReadArgs are the arguments of the built-in read_skill tool.
type skillReadArgs struct {
	Name string `json:"name" jsonschema:"description=Name of the skill to load"`
}

// skillToolDescription is the description of the built-in read_skill tool.
const skillToolDescription = "Load the full instructions of a skill by name. " +
	"Call this before attempting a task that matches one of the available skills, " +
	"then follow the loaded instructions."

// newSkillTool builds the built-in skill-loading tool for an agent.
func newSkillTool(name string, skills []Skill, hook SkillReadHook) Tool {
	byName := make(map[string]Skill, len(skills))
	for _, s := range skills {
		byName[s.Name] = s
	}
	return NewTool(name, skillToolDescription,
		func(ctx context.Context, args skillReadArgs) (any, error) {
			skill, ok := byName[args.Name]
			if !ok {
				names := make([]string, 0, len(byName))
				for n := range byName {
					names = append(names, n)
				}
				return ToolResult{
					Content: fmt.Sprintf("skill %q not found. Available skills: %s",
						args.Name, strings.Join(names, ", ")),
					IsError: true,
				}, nil
			}
			content := skill.Instructions
			if hook != nil {
				var err error
				content, err = hook(ctx, skill.Name, content)
				if err != nil {
					return nil, fmt.Errorf("read_skill(%q): %w", skill.Name, err)
				}
			}
			return content, nil
		})
}

// skillIndexBlock renders the progressive-disclosure index injected into the
// system prompt.
func skillIndexBlock(toolName string, skills []Skill) string {
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	b.WriteString("The following skills are available. When a task may benefit from a skill, ")
	b.WriteString("first call the " + toolName + " tool to load its full instructions, then follow them.\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "\n- %s: %s\n", s.Name, s.Description)
	}
	b.WriteString("</available_skills>")
	return b.String()
}
