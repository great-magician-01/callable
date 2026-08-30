# AGENTS.md

Guidance for AI coding agents working on this repository. The reader is assumed to know nothing about the project.

## Project overview

**callable** is a unified Go client library for LLM APIs with a built-in agent loop.

- Module: `github.com/great-magician-01/callable` (Go library, not an application; there is no `main` package outside `examples/`).
- Go version: `go 1.21` (see `go.mod`; the older `docs/PLAN.md` design doc mentions a different version — `go.mod` is authoritative).
- It speaks three wire formats behind one provider-agnostic message model:
  - OpenAI Chat Completions (including OpenAI-compatible endpoints: GLM, DeepSeek, Qwen, Z.AI, Volcano Ark, Kimi/Moonshot, ...)
  - OpenAI Responses
  - Anthropic Messages (including Anthropic-compatible third-party endpoints)
- On top of the providers sits an `Agent` that runs the full tool-calling loop automatically (model → tool execution → model → ... until a final answer), with streaming events, thinking/reasoning support, skills (progressive disclosure via a built-in `read_skill` tool), sub-agent delegation (built-in `load_agent` tool registers `call_<name>`), multi-turn `Session`, and image input.
- **Single third-party dependency**: `github.com/invopop/jsonschema` (used to reflect tool-argument structs into JSON Schema). HTTP, retries, and SSE parsing are all hand-written on the standard library — keep it that way; do not add dependencies casually.
- Current release: `0.6.0` (see `.release-please-manifest.json` and `CHANGELOG.md`).

## Repository layout

```
callable.go               # Single public entry point: package doc + full re-export
                          #   (type aliases + consts + thin function wrappers) of the
                          #   internal packages under internal/.
internal/                 # ALL implementation and tests live here, split by concern
                          #   (~10k lines incl. tests). Dependency DAG (no cycles):
                          #   model <- provider <- client <- agent; agent also uses skill.
  model/                    # Unified message model and shared data types (leaf package).
    message.go / content.go #   Message{Role, Parts}; Part is a sealed interface
                            #   (Text / Image / Thinking / ToolCall / ToolResult).
    request.go / response.go#   Unified Request / Response / Usage / StopReason.
    stream.go               #   Unified streaming events (ThinkingDelta, TextDelta, ...) +
                            #   EventSink. Event is a sealed interface: every event type must
                            #   stay in this package (AgentDoneEvent is why result.go is here).
    result.go               #   AgentResult (agent-run outcome data type).
    thinking.go             #   Thinking config + Effort levels.
    format.go               #   Structured output: ResponseFormat + JSONSchemaFor reflection.
    image.go                #   Image loading (path / URL / bytes), media-type detection, base64.
    tool.go                 #   Tool interface, NewTool[A] (generics + jsonschema reflection).
  provider/                 # Wire-format adapters.
    provider.go             #   Provider interface + shared HTTP / SSE / retry infrastructure.
    provider_oai_chat.go    #   OpenAI Chat Completions adapter (+ Chinese-endpoint compat dialects).
    provider_oai_responses.go # OpenAI Responses adapter.
    provider_anthropic.go   #   Anthropic Messages adapter.
    endpoints.go            #   Well-known base-URL constants + Compat dialect auto-detection.
    thinking_map.go         #   Thinking/Effort -> per-provider wire mapping helpers.
    errors.go               #   APIError.
    websearch.go            #   SupportsWebSearch seam: built-in server-side search detection.
  client/                   # Client (Create/Stream, retries, defaults) + package-level Derive.
  skill/                    # Skill type + built-in read_skill tool (NewReadTool) + read hook.
  agent/                    # Agent loop and everything plugged into it.
    agent.go                #   Agent loop + Session.
    compact.go              #   Session context-window options + auto/manual history compaction.
    subagent.go             #   SubAgent definitions + built-in load_agent / call_<name> tools.
    websearch.go            #   Web-search agent wiring + Kimi echo stub + Tavily fallback tool.
    id.go                   #   Conversation ID generation (sess-/run- prefixes).
    errors.go               #   MaxTurnsError.
  testutil/                 # Shared test helpers (mock servers, JSON asserts, SSE fixtures).
                            #   Must not import the other internal packages (import cycle with
                            #   white-box tests).
examples/                 # Runnable examples: quickstart, tools, thinking, vision, skills,
                          # subagents, compact, websearch, deepseek. Each is a main package needing real API keys.
docs/zh/ , docs/en/       # Per-feature user docs, fully bilingual (Chinese + English).
docs/PLAN.md              # Original design doc (Chinese); useful for design rationale, but
                          # the code is the source of truth where they diverge.
README.md / README_EN.md  # Chinese / English project readmes.
.github/workflows/        # ci.yml (gofmt + tests) and release-please.yml (releases).
go.mod / go.sum           # Module definition; the only Go config files.
release-please-config.json, .release-please-manifest.json  # Release automation config.
```

There is **no** `pyproject.toml`, `package.json`, `Cargo.toml`, Makefile, or linter config — this is a pure Go module with a deliberately minimal toolchain.

## Build and test commands

All commands run from the repo root. Everything below has been verified to pass.

```bash
go build ./...            # build the library and all examples
go vet ./...              # static checks (not in CI, but keep it clean)
go test ./...             # run all tests
go test -race -count=1 ./...   # exactly what CI runs
gofmt -l .                # must print nothing; CI fails on unformatted files
```

Running a single example (requires real API keys, never done in CI):

```bash
ANTHROPIC_API_KEY=... go run ./examples/quickstart
OPENAI_API_KEY=...    go run ./examples/quickstart
DEEPSEEK_API_KEY=...  go run ./examples/deepseek
```

Examples read keys from env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, plus `*_BASE_URL` / `DEEPSEEK_MODEL` overrides). The repo root has a gitignored `.env` for local keys — never commit it or echo its contents.

## Code style guidelines

- **Language**: All code, identifiers, doc comments, and inline comments are written in **English** (user-facing docs are bilingual zh/en). Keep new comments in English.
- **Formatting**: standard `gofmt`; CI rejects unformatted code.
- **Public API surface**: `callable.go` in the root is the *only* public entry point. It contains no logic — only type aliases (`type X = model.X` / `= provider.X` / ...), constant re-exports, and one-line function wrappers with doc comments. When you add or change an exported symbol in any `internal/` package that should be public, you must mirror it in `callable.go`, keeping the existing section-comment organization and doc-comment style.
- **Implementation stays internal**: everything lives in the packages under `internal/` (`model`, `provider`, `client`, `skill`, `agent`), so external users cannot depend on unexported details. Tests are white-box: each test file uses the package of the code it covers.
- **Options pattern**: configuration is via functional options (`WithModel`, `WithTools`, `WithThinking`, ...). Follow this pattern for new configurability; grouped per type (`ProviderOption`, `ClientOption`, `AgentOption`, `SubAgentOption`).
- **Sealed interfaces**: `Part` and `Event` are closed type families handled by type switches. Extend the family rather than opening the interface.
- **Doc comments**: every exported symbol carries a proper doc comment (golint style, starts with the symbol name). Match the density and tone of neighboring comments.
- **Provider portability**: business logic must operate on the unified message model; wire-format concerns belong in the provider adapters. History round-tripping fidelity (Anthropic thinking signatures, Responses reasoning items, DeepSeek/GLM `reasoning_content`) is a correctness requirement — preserve it when touching message conversion.

## Testing instructions

- Tests live in the `*_test.go` files next to the code they cover (each internal package has its own); run with `go test -race -count=1 ./...`.
- Test strategy (per `docs/PLAN.md`, §14):
  - **Golden request tests**: build requests for each provider × feature combination and compare against expected wire JSON, using the helpers in `internal/testutil` (`DecodeMap`, `AsMap`, `AsSlice`, `AsString`, `AsFloat`).
  - **SSE fixture tests**: canned streaming-response bodies in each provider's format, asserting the unified event sequence.
  - **Integration tests via `httptest`**: `internal/testutil` provides `NewMockServer` (serves queued SSE bodies, records request bodies), `NewMockJSONServer` and `NewMockMixedServer` — reuse these; agent-loop tests (multi-turn tool calls, max turns, approval hooks, skills, sub-agents, cancellation) are all built on them. Test files that need these helpers dot-import `internal/testutil`.
  - **Unit tests**: schema generation, image encoding/type detection, message JSON round-trip.
- **Tests must never call real LLM APIs.** No API keys exist in CI; all provider behavior is mocked via `httptest`.
- Test names follow `TestXxx` with table-driven cases where natural (see `endpoints_test.go`). Endpoint URL constants are pinned by `TestEndpointURLs` — a typo there breaks real calls, so update the test only intentionally.

## CI and release process

- **CI** (`.github/workflows/ci.yml`): on push/PR to `master` — checks `gofmt -l .` is empty, then runs `go test -race -count=1 ./...` with the Go version from `go.mod`.
- **Releases** (`.github/workflows/release-please.yml`): [release-please](https://github.com/googleapis/release-please) with `release-type: go`. It maintains `CHANGELOG.md` and version tags from **conventional commits** (`feat:`, `fix:`, etc.) on `master`. Write commit messages accordingly; do not hand-edit `CHANGELOG.md` or `.release-please-manifest.json`.
- **Docs**: when changing user-visible behavior, update both `docs/zh/` and `docs/en/` plus `README.md` / `README_EN.md` — the project keeps Chinese and English documentation in sync.

## Security considerations

- API keys come from environment variables only; `.env` and `.env.*` are gitignored. Never commit keys, and never print or log key material.
- The library sends user-provided message history and local image files (via `callable.Image(path)`) to the configured provider endpoint — be careful not to weaken that boundary (e.g., don't auto-attach files beyond what the user passed).
- `WithExtra` is a request-level escape hatch that merges arbitrary fields into the request body; treat its contents as user-controlled.
- `WithToolCallHook` / `Approve` / `Deny` / `ReplaceArgs` is the approval gate for tool execution — changes to the agent loop must not bypass this hook or silently auto-approve.
- Keep the dependency footprint minimal (currently just `invopop/jsonschema`); the networking layer intentionally uses only the standard library.
