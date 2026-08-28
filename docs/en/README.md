# callable Documentation

[中文](../zh/README.md) | **English**

Detailed usage documentation organized by feature. All API names and signatures follow the root package [`callable.go`](../../callable.go).

## Contents

- [Getting Started](getting-started.md) — installation, Client and the three Providers, endpoint constants, Compat dialects
- [Message Model](messages.md) — the unified Message/Part model, constructors, history fidelity, JSON persistence
- [Agent Loop](agent.md) — Run/RunStream, approval hooks, parallel tool execution, max turns, config layering
- [Sessions](session.md) — Session, Ask/AskStream, context window with auto/manual compaction, history persistence and restore
- [Tools](tools.md) — the NewTool generic constructor, JSON Schema generation, NewRawTool, error feedback
- [Streaming Events](streaming.md) — event reference, typical event sequences, Usage accounting
- [Thinking Mode](thinking.md) — Effort levels, per-provider mapping, pitfalls of Chinese-compatible endpoints
- [Skills](skills.md) — progressive disclosure, the read_skill tool, read hooks, custom loader tools
- [Sub-Agents](subagents.md) — the two-step load_agent delegation flow, SubAgentOption, event forwarding
- [Image Input](images.md) — file paths/URLs/raw bytes, mixed content, per-provider conversion
- [Error Handling](errors.md) — APIError, automatic retries, cancellation and timeouts, the WithExtra escape hatch

## See Also

- [Design document PLAN.md](../PLAN.md) (Chinese) — architecture and wire-format mappings
- [examples/](../../examples) — runnable examples
