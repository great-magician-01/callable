# Skills: Progressive Disclosure

[中文](../zh/skills.md) | **English**

A skill is a named instruction package (usually a Markdown document describing a convention, workflow, or template). callable hands skills to the model through **progressive disclosure**: the system prompt only carries an index of each skill's name and one-line description; when the model decides a task matches a skill, it loads the full instructions on demand through the built-in `read_skill` tool and then follows them.

## Why progressive disclosure

Inlining every instruction set into the system prompt has two problems:

- **Wasted tokens**: irrelevant instructions are billed on every request, and the cost multiplies with every turn of the conversation.
- **Diluted attention**: an overlong system prompt distracts the model from the task at hand and hurts instruction following.

Progressive disclosure turns this into pay-per-use: the index is a few lines, and the full text is loaded exactly once, only when needed. The cost is one extra tool round-trip — almost always a good trade for non-trivial instruction sets.

## Quick start

```go
skill := callable.NewSkill("pdf-export", "Export data as a PDF file",
    "# PDF export rules\n\n(Full instructions... only read when the model calls read_skill)")

agent := callable.NewAgent(client,
    callable.WithSkills(skill),
)
```

The model decides on its own to call `read_skill({"name": "pdf-export"})`, receives the full text, and follows it — no business code involved.

## API overview

| API | Description |
| --- | --- |
| `callable.NewSkill(name, description, instructions string) Skill` | Build a skill (an in-memory value type) |
| `callable.WithSkills(skills ...Skill) AgentOption` | Register skills on an agent |
| `callable.WithSkillReadHook(h SkillReadHook) AgentOption` | Install a hook that can rewrite content before it reaches the model |
| `callable.WithSkillToolName(name string) AgentOption` | Rename the built-in loading tool (default `read_skill`) |
| `callable.WithSkillToolDisabled() AgentOption` | Skip the built-in loading tool so you can register your own |
| `callable.DefaultSkillToolName` | Constant, equal to `"read_skill"` |

Related types:

```go
type Skill struct {
    Name         string // short identifier the model passes to read_skill
    Description  string // one-line summary shown in the system prompt index; the model's only basis for deciding whether to load
    Instructions string // full skill body (usually Markdown), served on demand
}

type SkillReadHook func(ctx context.Context, name, content string) (string, error)
```

## How it works

### The index injected into the system prompt

When skills are registered, the agent appends an index block to the system prompt (after your own `WithSystemPrompt` text and before the sub-agent index, separated by a blank line). The index contains **names and descriptions only, never the full text**. With `WithSkills(pdfExportSkill, webSearchSkill)`, the verbatim injected block is:

```
<available_skills>
The following skills are available. When a task may benefit from a skill, first call the read_skill tool to load its full instructions, then follow them.

- pdf-export: Export data as a PDF file
- web-search: Search the internet
</available_skills>
```

(The `read_skill` mention follows whatever name you set via `WithSkillToolName`.)

### The built-in read_skill tool

As soon as at least one skill is registered and the tool is not disabled, the agent registers a built-in tool:

- Name: `read_skill` (`DefaultSkillToolName`)
- Description (sent to the model verbatim): `Load the full instructions of a skill by name. Call this before attempting a task that matches one of the available skills, then follow the loaded instructions.`
- Parameters: `{"name": "..."}` (string, required)

Behavior details:

- **Hit**: returns the skill's `Instructions` in full (after the read hook, if any).
- **Miss**: does not abort the agent loop. The model gets a **failed tool result** like `skill "foo" not found. Available skills: pdf-export, web-search` and can retry with a corrected name.
- **Stateless**: the built-in tool does not track "already loaded"; every call returns the full text. Loaded instructions stay in the conversation history, so the model can keep referencing them and normally never needs to reload.

## The read hook: WithSkillReadHook

Hook signature:

```go
type SkillReadHook func(ctx context.Context, name, content string) (string, error)
```

It fires on every `read_skill` call, right **before** the content is returned to the model. The returned string replaces the original; returning an error produces a failed tool result the model can react to (again without breaking the loop). Typical uses:

- **Inject runtime context**: time, tenant, environment — anything the model needs but that would go stale if baked into the text.
- **Rewrite content**: trim or extend the instructions per caller.
- **Lazy loading**: leave `Instructions` empty (or a placeholder) in `NewSkill` and fetch the real body from disk or a remote store inside the hook.
- **Auditing**: record which model loaded which skill in which task.

Runtime-context injection plus auditing:

```go
agent := callable.NewAgent(client,
    callable.WithSkills(weatherSkill),
    callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
        log.Printf("skill loaded: %s", name) // audit
        return content + "\n\n(Report generated at: " + time.Now().Format("2006-01-02 15:04") + ")", nil
    }),
)
```

Lazy loading (body read from disk on first access, not at construction time):

```go
skill := callable.NewSkill("release-checklist", "Pre-release checklist", "") // Instructions left empty

callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
    if content == "" {
        data, err := os.ReadFile("skills/" + name + ".md")
        if err != nil {
            return "", err // surfaced to the model as a failed tool result
        }
        content = string(data)
    }
    return content, nil
})
```

Note the hook fires on **every read**, so cache the content yourself in a pure lazy-loading scenario (the example above only reads from disk when `content == ""` because a `Skill` is an immutable value; for hook-side caching use a closure variable).

## Customizing the loading tool

### Renaming: WithSkillToolName

```go
agent := callable.NewAgent(client,
    callable.WithSkills(skill),
    callable.WithSkillToolName("load_skill"), // renames the tool; the index text updates too
)
```

An empty string is ignored (the default name stays). The tool name in the system prompt index changes in lockstep, so the model never notices.

### Disabling and replacing: WithSkillToolDisabled

```go
// A hand-rolled loading tool with access control.
loadSkill := callable.NewTool("read_skill",
    "Load the full instructions of a skill by name.",
    func(ctx context.Context, args struct {
        Name string `json:"name" jsonschema:"description=Name of the skill to load"`
    }) (any, error) {
        if !allowed(args.Name) {
            return callable.ErrorResult(fmt.Errorf("skill %q is not available in this tenant", args.Name)), nil
        }
        return skillBody(args.Name), nil
    })

agent := callable.NewAgent(client,
    callable.WithSkills(skill),
    callable.WithSkillToolDisabled(), // turn off the built-in tool
    callable.WithTools(loadSkill),    // register your own replacement
)
```

Two pitfalls:

- **The `<available_skills>` index is still injected after disabling the built-in tool**, and it still points at the default name `read_skill`. If your replacement uses a different name, call `WithSkillToolName` as well so the index matches — otherwise the model will call a tool that does not exist and get an unknown-tool error.
- **On name conflicts, first registration wins**: user tools are registered before built-ins, so you can also override the built-in by registering a user tool named `read_skill` *without* disabling it. Disabling explicitly is clearer and recommended.

## Complete example

This registers a "weather report" skill and injects the generation time through the read hook (a runnable version lives in `examples/skills/main.go`):

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	callable "github.com/great-magician-01/callable"
)

func main() {
	ctx := context.Background()
	key := os.Getenv("ANTHROPIC_API_KEY")

	client := callable.NewClient(
		callable.NewAnthropicProvider(key, callable.AnthropicURL),
		callable.WithModel("claude-sonnet-5"),
	)

	// Only name + description enter the system prompt; the body loads on demand.
	weatherSkill := callable.NewSkill("weather-report",
		"Produce a formal weather report document",
		`# Weather report format

Once you receive weather data, output the report as follows:

1. Title: "<City> Weather Report"
2. Overview table: date, temperature, conditions, wind
3. One paragraph of lifestyle advice

Rules:
- Round temperatures to integers
- Use Celsius
- Keep the advice under 100 words`)

	agent := callable.NewAgent(client,
		callable.WithSkills(weatherSkill),
		// The read hook fires every time the model loads a skill;
		// here it injects runtime context.
		callable.WithSkillReadHook(func(ctx context.Context, name, content string) (string, error) {
			return content + "\n\n(Report generated at: " + time.Now().Format("2006-01-02 15:04") + ")", nil
		}),
	)

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		if d, ok := ev.(callable.TextDeltaEvent); ok {
			fmt.Print(d.Delta)
		}
	}, callable.User("It's 26 degrees, sunny with a light breeze in Beijing today. Write me a weather report."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	_ = result
	fmt.Println()
}
```

A typical run: the model sees the `<available_skills>` index → decides the task matches `weather-report` → calls `read_skill` → receives the full text (with the hook-appended timestamp) → produces the report per the spec.

## Skills and sub-agents

Skills also work inside sub-agents: skills registered via `WithSubAgentSkills` only take effect within the sub-agent's own agent loop, which gets its own independent `read_skill` tool invisible to the parent. See [Sub-Agents](subagents.md).

## Caveats and pitfalls

- **Description is the model's only decision signal**: state clearly *when* the skill applies. If the description is vague, the model will never load the skill no matter how good the instructions are.
- **Instructions are a static string**: immutable after `NewSkill`. For dynamic content (current time, user identity, configuration) use `WithSkillReadHook`; skills carry no variables themselves.
- **The hook runs on every read**: keep it idempotent and fast; cache yourself if it does I/O. The hook's `ctx` is tied to the current agent run, so cancellation propagates into it.
- **Skills are keyed by Name**: registering two skills with the same name means the later one wins (the built-in tool looks them up in a map); for tools in the tool list, the first registration wins.
- **Skills are not tools, but close**: each agent with skills gets one extra `read_skill` tool in its tool list — count its schema when budgeting tokens.
- **Full text enters the history**: loaded instructions stay in the conversation history as a tool result and are billed on every subsequent turn — which is exactly why "load once" matters.

## See also

- [Agent Loop](agent.md) — read_skill is an ordinary tool call inside the agent loop
- [Tools](tools.md) — how to define your own replacement loading tool
- [Sub-Agents](subagents.md) — progressive disclosure of sub-agents and skill scoping
