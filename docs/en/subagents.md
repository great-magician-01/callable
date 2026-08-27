# Sub-Agent Delegation

[中文](../zh/subagents.md) | **English**

callable's SubAgent mechanism lets a parent agent delegate subtasks to independently running sub-agents. Like [skill progressive disclosure](skills.md), sub-agents are **not exposed as tools by default**: the system prompt only carries a name/description index, and the model must first call the built-in `load_agent` tool to load one, which dynamically registers a `call_<name>` tool it can then invoke. Each delegation runs the sub-agent in a **fresh session** with its own [agent loop](agent.md), and its final answer is returned to the parent as the tool result.

## Defining a Sub-Agent: NewSubAgent

```go
func NewSubAgent(name, description string, opts ...SubAgentOption) SubAgent
```

- `name`: short identifier used as the argument to `load_agent`; once loaded, the callable tool is `call_<name>`. Use tool-name-safe characters (letters, digits, `_`, `-`).
- `description`: one-line summary injected into the system prompt index — this is the only thing the parent model sees when deciding whether to load and delegate to this sub-agent, so make it count.
- `opts`: see the table below.

### SubAgentOption Reference

| Option | Signature | Description |
|---|---|---|
| `WithSubAgentClient` | `func WithSubAgentClient(client *Client) SubAgentOption` | Gives the sub-agent its own client (e.g. a different provider, endpoint, or credentials). When unset, it inherits the parent agent's client. |
| `WithSubAgentModel` | `func WithSubAgentModel(model string) SubAgentOption` | Overrides only the model while reusing the parent client (endpoint, credentials, and other defaults). Ignored when `WithSubAgentClient` supplies a custom client. |
| `WithSubAgentPrompt` | `func WithSubAgentPrompt(prompt string) SubAgentOption` | The sub-agent's system prompt. |
| `WithSubAgentTools` | `func WithSubAgentTools(tools ...Tool) SubAgentOption` | [Tools](tools.md) available inside the sub-agent loop; invisible to the parent agent. |
| `WithSubAgentSkills` | `func WithSubAgentSkills(skills ...Skill) SubAgentOption` | Skills available inside the sub-agent loop; the sub-agent gets its own built-in `read_skill` tool. |
| `WithSubAgentThinking` | `func WithSubAgentThinking(t Thinking) SubAgentOption` | [Thinking mode](thinking.md) configuration for the sub-agent. |
| `WithSubAgentMaxTurns` | `func WithSubAgentMaxTurns(n int) SubAgentOption` | Caps the number of model calls per delegation; default 25. `n <= 0` is ignored (keeps the default). |

All options affect only the sub-agent's internal loop and never leak into the parent agent.

## Registering on the Parent: WithSubAgents

```go
func WithSubAgents(subs ...SubAgent) AgentOption
```

```go
agent := callable.NewAgent(client,
    callable.WithSystemPrompt("Delegate translation to translator and research to researcher"),
    callable.WithSubAgents(translator, researcher),
)
```

Registering sub-agents automatically gives the parent the built-in `load_agent` tool and injects the index block into its system prompt. If two definitions share a name, the **first registration wins**; later duplicates are silently ignored.

## The Two-Step Delegation Flow

The whole flow is model-driven — no manual calls are needed in your code:

1. **Load**: the model calls the built-in `load_agent({"name": "translator"})`. The registry **dynamically registers** the `call_translator` tool into the parent's tool set (visible to the model from the next request on) and returns a usage card containing the sub-agent's description, system prompt, capability list (tool names + skill indicator), model (if overridden), and how to invoke it.
2. **Delegate**: the model calls `call_translator({"task": "..."})`. `task` is a complete, self-contained subtask description — the sub-agent cannot see the parent's conversation, so the task must carry all the context it needs. The sub-agent runs its own agent loop in a fresh session (with its own client/model/prompt/tools/skills), and its final answer (`FinalText`) is returned to the parent as the result of `call_translator`, which the parent then continues from.

## The Index Block in the System Prompt

When sub-agents are registered, this index block is appended to the system prompt (`load_agent` is the default tool name; it changes in sync if you rename the tool):

```
<available_agents>
The following sub-agents are available for delegating subtasks. They are NOT
loaded as tools yet. When a subtask matches one of them, first call the
load_agent tool with the sub-agent's name to load it; this registers a
call_<name> tool, which you then call with a self-contained task description.

- translator: 把中文翻译成地道的英文
- researcher: 深度调研一个主题
</available_agents>
```

If skills are registered too, the `<available_skills>` and `<available_agents>` blocks are appended after the system prompt in order, separated by a blank line.

## Renaming or Disabling the Built-in Tool

```go
const DefaultSubAgentLoadToolName = "load_agent"

func WithSubAgentToolName(name string) AgentOption   // rename (empty string ignored)
func WithSubAgentToolDisabled() AgentOption          // don't register load_agent
```

- With the tool disabled, registered sub-agents still appear in the `<available_agents>` index, but the model cannot load them on its own — register a replacement loading tool (or the `call_<name>` tools directly) yourself in that case.
- When no sub-agents are registered, `load_agent` is not added regardless of these options.

## Behavior Details and Edge Cases

- **Idempotent loading**: loading the same sub-agent twice does not double-register; from the second call on, `load_agent` returns "Sub-agent \"xxx\" is already loaded." plus the same usage card. The registry is mutex-guarded, so parallel sessions of the same agent may load sub-agents independently.
- **Name conflicts with user tools**: if the parent already has a user tool named `call_<name>`, loading returns an error tool result (`IsError`: `tool "call_xxx" is already registered`) instead of overwriting your tool. Keep the `call_` prefix in mind when naming things.
- **Loading an unknown name**: `load_agent({"name": "nobody"})` returns an `IsError` result listing the available sub-agents, so the model can correct itself.
- **Calling before loading**: before it is loaded, `call_<name>` simply isn't in the tool list; invoking it yields an `unknown tool "call_xxx"` error result (fed back to the model without breaking the loop — see [Error Handling](errors.md)).
- **No nesting**: sub-agents do not inherit the parent's sub-agent list — there is no `load_agent` inside a sub-agent, so delegation cannot recurse.
- **No shared history**: every `call_<name>` invocation builds a brand-new Agent; multiple delegations know nothing about each other, and the sub-agent cannot see the parent's conversation. Put all required context into `task`.
- **Max-turns fallback**: when a sub-agent hits `WithSubAgentMaxTurns`, the delegation does not fail outright — the implementation recovers the last assistant text from the partial trajectory and returns it as the tool result with a `[sub-agent stopped: reached max turns]` note appended. Only if the partial trajectory contains no text at all is the error (`*MaxTurnsError`, see [Error Handling](errors.md)) propagated.
- **Empty final answer**: if the sub-agent finishes normally but `FinalText` is empty, the tool result is `(sub-agent "xxx" finished without a final text answer)`.
- **Event forwarding**: by default, whatever happens inside a sub-agent is invisible to the parent's event callback (a delegation looks like one ordinary long-running tool call). With `WithSubAgentEvents(true)`, the sub-agent runs in streaming mode and every event it emits is wrapped in a `SubAgentEvent` and forwarded to the event callback the parent passed to `RunStream` / `AskStream`:

  ```go
  type SubAgentEvent struct {
      SubAgent string // name of the sub-agent that emitted the event
      Event    Event  // the original event (TextDeltaEvent / ToolCallEvent / ...)
  }
  ```

  The forwarding sink is request-scoped (each `RunStream` call forwards to its own callback), so parallel sessions of the same agent never cross streams. Note that forwarding only takes effect when the parent runs through a streaming entry point with a non-nil callback; under `Run` / `Ask`, sub-agent events have nowhere to go.

## Complete Example

Two sub-agents — translator (model-override only) and researcher (with its own tool) — with the parent streaming and sub-agent events forwarded:

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
	client := callable.NewClient(
		callable.NewOpenAIProvider(os.Getenv("OPENAI_API_KEY"), callable.OpenAIURL),
		callable.WithModel("gpt-5"),
	)

	// Translator sub-agent: cheaper model, same endpoint as the parent.
	translator := callable.NewSubAgent("translator", "Translate Chinese into idiomatic English",
		callable.WithSubAgentModel("gpt-5-mini"),
		callable.WithSubAgentPrompt("You are a professional translator. Translate the given Chinese into natural, idiomatic English. Output the translation only."),
	)

	// Researcher sub-agent: own search tool (invisible to the parent) and skill.
	search := callable.NewTool("web_search", "Search the internet",
		func(ctx context.Context, args struct {
			Query string `json:"query" jsonschema:"description=search keywords"`
		}) (any, error) {
			return fmt.Sprintf("search results for %q ...", args.Query), nil
		})
	researcher := callable.NewSubAgent("researcher", "Research a topic in depth and conclude with citations",
		callable.WithSubAgentPrompt("You are a researcher. Gather material with web_search first, then conclude."),
		callable.WithSubAgentTools(search),
		callable.WithSubAgentSkills(callable.NewSkill("citing", "Citation conventions", "# Citation format\n...")),
		callable.WithSubAgentThinking(callable.Thinking{Effort: callable.EffortHigh}),
		callable.WithSubAgentMaxTurns(10),
	)

	agent := callable.NewAgent(client,
		callable.WithSystemPrompt("You are the orchestrator. Delegate translation to translator and research to researcher; never do the work yourself."),
		callable.WithSubAgents(translator, researcher),
		callable.WithMaxTurns(15),
		callable.WithSubAgentEvents(true), // wrap inner events as SubAgentEvent
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	result, err := agent.RunStream(ctx, func(ev callable.Event) {
		switch e := ev.(type) {
		case callable.TextDeltaEvent:
			fmt.Print(e.Delta) // parent agent output
		case callable.SubAgentEvent:
			// Inner event: e.SubAgent is the sub-agent name, e.Event the raw event.
			if d, ok := e.Event.(callable.TextDeltaEvent); ok {
				fmt.Printf("[%s] %s", e.SubAgent, d.Delta)
			}
		}
	}, callable.User("Research what's new in Go 1.26 and translate the key points into English."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("\n[turns=%d tokens: in=%d out=%d]\n",
		result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
}
```

A runnable version lives in `examples/subagents/main.go` in the repository.

## See Also

- [Agent Loop](agent.md): the loop mechanism shared by parent and sub-agents
- [Tools](tools.md): how sub-agent tools are registered
- [Skills](skills.md): the same progressive-disclosure design applied to skills
- [Streaming Events](streaming.md): all event types including `SubAgentEvent`
- [Error Handling](errors.md): `unknown tool`, `MaxTurnsError`, and more
