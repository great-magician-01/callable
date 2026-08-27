package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// SubAgent is a named, self-contained agent definition the parent agent can
// delegate subtasks to. Like skills, sub-agents use progressive disclosure:
// only the Name and Description are injected into the system prompt index and
// no tool is registered up front. The model must first call the built-in
// load_agent tool, which registers a dedicated call_<name> tool; only then
// can the sub-agent be invoked.
//
// A sub-agent runs its own fresh agent loop (own history, own tools, own
// skills) and its final answer is returned to the parent as the tool result.
type SubAgent struct {
	// Name is the short identifier used with load_agent; the callable tool
	// after loading is "call_<Name>". Use tool-name-safe characters
	// (letters, digits, '_' and '-').
	Name string
	// Description is a one-line summary shown in the system prompt index.
	// This is what the parent model uses to decide whether to load and
	// delegate to this sub-agent.
	Description string

	client   *Client // nil: inherit the parent agent's client
	model    string  // non-empty: override the model on the inherited client
	prompt   string  // system prompt of the sub-agent
	tools    []Tool  // tools available inside the sub-agent loop
	skills   []Skill // skills available inside the sub-agent loop
	thinking *Thinking
	maxTurns int
}

// SubAgentOption configures a SubAgent.
type SubAgentOption func(*SubAgent)

// NewSubAgent creates a sub-agent definition. Register it on a parent agent
// with WithSubAgents.
func NewSubAgent(name, description string, opts ...SubAgentOption) SubAgent {
	s := SubAgent{Name: name, Description: description}
	for _, o := range opts {
		o(&s)
	}
	return s
}

// WithSubAgentClient gives the sub-agent its own client, e.g. a different
// provider or endpoint than the parent's. When unset, the sub-agent inherits
// the parent agent's client (see WithSubAgentModel for a cheap override).
func WithSubAgentClient(client *Client) SubAgentOption {
	return func(s *SubAgent) { s.client = client }
}

// WithSubAgentModel overrides the model the sub-agent runs on while reusing
// the parent agent's client (provider, credentials and other defaults). It is
// ignored when WithSubAgentClient supplies a custom client.
func WithSubAgentModel(model string) SubAgentOption {
	return func(s *SubAgent) { s.model = model }
}

// WithSubAgentPrompt sets the sub-agent's system prompt.
func WithSubAgentPrompt(prompt string) SubAgentOption {
	return func(s *SubAgent) { s.prompt = prompt }
}

// WithSubAgentTools registers tools available inside the sub-agent loop. They
// are not visible to the parent agent.
func WithSubAgentTools(tools ...Tool) SubAgentOption {
	return func(s *SubAgent) { s.tools = append(s.tools, tools...) }
}

// WithSubAgentSkills registers skills available inside the sub-agent loop
// (with its own built-in read_skill tool).
func WithSubAgentSkills(skills ...Skill) SubAgentOption {
	return func(s *SubAgent) { s.skills = append(s.skills, skills...) }
}

// WithSubAgentThinking configures thinking/reasoning for the sub-agent.
func WithSubAgentThinking(t Thinking) SubAgentOption {
	return func(s *SubAgent) { cp := t; s.thinking = &cp }
}

// WithSubAgentMaxTurns caps the number of model calls per sub-agent run.
// Default 25.
func WithSubAgentMaxTurns(n int) SubAgentOption {
	return func(s *SubAgent) {
		if n > 0 {
			s.maxTurns = n
		}
	}
}

// DefaultSubAgentLoadToolName is the name of the built-in sub-agent-loading
// tool.
const DefaultSubAgentLoadToolName = "load_agent"

// subAgentCallToolName is the tool name a loaded sub-agent is invoked through.
func subAgentCallToolName(subName string) string { return "call_" + subName }

// subAgentLoadArgs are the arguments of the built-in load_agent tool.
type subAgentLoadArgs struct {
	Name string `json:"name" jsonschema:"description=Name of the sub-agent to load (from the available_agents list)"`
}

// subAgentCallArgs are the arguments of every dynamically registered
// call_<name> tool.
type subAgentCallArgs struct {
	Task string `json:"task" jsonschema:"description=Complete, self-contained description of the subtask for the sub-agent to accomplish"`
}

// subAgentLoadToolDescription is the description of the built-in load_agent
// tool.
const subAgentLoadToolDescription = "Load a sub-agent by name so it can be called. " +
	"Sub-agents are not available as tools until loaded. After loading, a call_<name> " +
	"tool becomes available; use it to delegate a subtask to the sub-agent."

// subAgentRegistry tracks the sub-agent definitions of one parent agent and
// materializes their call tools on load. It is safe for concurrent use, so
// parallel sessions of the same agent may load sub-agents independently.
type subAgentRegistry struct {
	parent *Client
	tools  *toolSet

	mu     sync.Mutex
	defs   map[string]SubAgent
	order  []string // definition order, first registration wins on duplicates
	loaded map[string]bool
}

func newSubAgentRegistry(parent *Client, tools *toolSet, subs []SubAgent) *subAgentRegistry {
	r := &subAgentRegistry{
		parent: parent,
		tools:  tools,
		defs:   make(map[string]SubAgent, len(subs)),
		loaded: make(map[string]bool, len(subs)),
	}
	for _, s := range subs {
		if _, exists := r.defs[s.Name]; exists {
			continue
		}
		r.defs[s.Name] = s
		r.order = append(r.order, s.Name)
	}
	return r
}

// list returns the definitions in registration order, for the index block.
func (r *subAgentRegistry) list() []SubAgent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SubAgent, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.defs[n])
	}
	return out
}

// empty reports whether no sub-agents are registered.
func (r *subAgentRegistry) empty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.order) == 0
}

// newSubAgentLoadTool builds the built-in load_agent tool for a parent agent.
func newSubAgentLoadTool(name string, reg *subAgentRegistry) Tool {
	return NewTool(name, subAgentLoadToolDescription,
		func(ctx context.Context, args subAgentLoadArgs) (any, error) {
			return reg.load(args.Name)
		})
}

// load registers the call_<name> tool for a sub-agent (idempotently) and
// returns its usage card for the model. The registry lock is held across the
// conflict check and registration so concurrent loads cannot double-register
// or race a same-named user tool.
func (r *subAgentRegistry) load(name string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub, ok := r.defs[name]
	if !ok {
		return ToolResult{
			Content: fmt.Sprintf("sub-agent %q not found. Available sub-agents: %s",
				name, strings.Join(r.order, ", ")),
			IsError: true,
		}, nil
	}

	callName := subAgentCallToolName(name)
	already := r.loaded[name]
	if !already {
		if _, taken := r.tools.get(callName); taken {
			return ToolResult{
				Content: fmt.Sprintf("cannot load sub-agent %q: tool %q is already registered", name, callName),
				IsError: true,
			}, nil
		}
		r.tools.add(newSubAgentCallTool(sub, r.parent))
		r.loaded[name] = true
	}
	return sub.usageCard(callName, already), nil
}

// usageCard renders the instructions returned to the model after a sub-agent
// is loaded.
func (s *SubAgent) usageCard(callName string, already bool) string {
	var b strings.Builder
	if already {
		fmt.Fprintf(&b, "Sub-agent %q is already loaded.\n", s.Name)
	} else {
		fmt.Fprintf(&b, "Sub-agent %q loaded.\n", s.Name)
	}
	fmt.Fprintf(&b, "\nDescription: %s\n", s.Description)
	if s.prompt != "" {
		fmt.Fprintf(&b, "System prompt: %s\n", s.prompt)
	}
	caps := make([]string, 0, len(s.tools)+1)
	for _, t := range s.tools {
		caps = append(caps, t.Definition().Name)
	}
	if len(s.skills) > 0 {
		caps = append(caps, DefaultSkillToolName)
	}
	if len(caps) > 0 {
		fmt.Fprintf(&b, "Its tools: %s\n", strings.Join(caps, ", "))
	}
	if s.model != "" {
		fmt.Fprintf(&b, "Model: %s\n", s.model)
	}
	fmt.Fprintf(&b, "\nDelegate work by calling the %s tool with a `task` string. "+
		"The sub-agent runs autonomously and its final answer is returned as the tool result.", callName)
	return b.String()
}

// subAgentCallToolDescription renders the description of a call_<name> tool.
func subAgentCallToolDescription(sub SubAgent) string {
	return fmt.Sprintf("Delegate a subtask to the %q sub-agent: %s "+
		"The sub-agent runs autonomously with its own configuration and returns its final answer.",
		sub.Name, sub.Description)
}

// subAgentEventSinkKey carries the parent agent's event sink through the
// context, so a call_<name> tool can forward its sub-agent's events. It is
// request-scoped (set per RunStream call), so parallel sessions of the same
// agent each forward to their own sink.
type subAgentEventSinkKey struct{}

// newSubAgentCallTool builds the tool that runs one delegation to the
// sub-agent. A fresh Agent is built per call, so calls never share history.
func newSubAgentCallTool(sub SubAgent, parent *Client) Tool {
	return NewTool(subAgentCallToolName(sub.Name), subAgentCallToolDescription(sub),
		func(ctx context.Context, args subAgentCallArgs) (any, error) {
			subAgent := sub.build(parent)
			var (
				res *AgentResult
				err error
			)
			if sink, _ := ctx.Value(subAgentEventSinkKey{}).(eventSink); sink != nil {
				// Event forwarding is on: stream the sub-agent's loop and wrap
				// every event with the sub-agent's name.
				res, err = subAgent.RunStream(ctx, func(ev Event) {
					sink(SubAgentEvent{SubAgent: sub.Name, Event: ev})
				}, User(args.Task))
			} else {
				res, err = subAgent.Run(ctx, User(args.Task))
			}
			if err != nil {
				// A sub-agent that hit its turn limit may still have produced
				// a usable partial answer; hand it back instead of failing.
				// Note: AgentResult.FinalText stays empty on a max-turns stop
				// (the run never completed), so recover the text of the last
				// assistant message from the partial trajectory.
				var mte *MaxTurnsError
				if errors.As(err, &mte) {
					if partial := lastAssistantText(mte.Partial); partial != "" {
						return partial + "\n\n[sub-agent stopped: reached max turns]", nil
					}
				}
				return nil, err
			}
			if res.FinalText == "" {
				return fmt.Sprintf("(sub-agent %q finished without a final text answer)", sub.Name), nil
			}
			return res.FinalText, nil
		})
}

// build materializes the sub-agent's Agent. The client resolution order is:
// explicit client > parent client with model override > parent client.
func (s *SubAgent) build(parent *Client) *Agent {
	client := s.client
	if client == nil {
		client = parent
		if s.model != "" {
			client = parent.derive(s.model)
		}
	}
	opts := []AgentOption{WithSystemPrompt(s.prompt)}
	if len(s.tools) > 0 {
		opts = append(opts, WithTools(s.tools...))
	}
	if len(s.skills) > 0 {
		opts = append(opts, WithSkills(s.skills...))
	}
	if s.thinking != nil {
		opts = append(opts, WithThinking(*s.thinking))
	}
	if s.maxTurns > 0 {
		opts = append(opts, WithMaxTurns(s.maxTurns))
	}
	return NewAgent(client, opts...)
}

// lastAssistantText returns the text of the last assistant message in a
// (possibly partial) agent trajectory, or "" if none carries text.
func lastAssistantText(result *AgentResult) string {
	if result == nil {
		return ""
	}
	for i := len(result.Messages) - 1; i >= 0; i-- {
		if m := result.Messages[i]; m.Role == RoleAssistant {
			if text := strings.TrimSpace(m.Text()); text != "" {
				return text
			}
		}
	}
	return ""
}

// subAgentIndexBlock renders the progressive-disclosure index injected into
// the system prompt.
func subAgentIndexBlock(toolName string, subs []SubAgent) string {
	var b strings.Builder
	b.WriteString("<available_agents>\n")
	b.WriteString("The following sub-agents are available for delegating subtasks. ")
	b.WriteString("They are NOT loaded as tools yet. When a subtask matches one of them, ")
	b.WriteString("first call the " + toolName + " tool with the sub-agent's name to load it; ")
	b.WriteString("this registers a call_<name> tool, which you then call with a self-contained task description.\n")
	for _, s := range subs {
		fmt.Fprintf(&b, "\n- %s: %s\n", s.Name, s.Description)
	}
	b.WriteString("</available_agents>")
	return b.String()
}
