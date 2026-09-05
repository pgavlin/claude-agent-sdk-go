# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Go SDK for the Claude Code CLI (`github.com/pgavlin/claude-agent-sdk-go`, package `agentsdk`). Ported from the upstream [Python SDK](https://github.com/anthropics/claude-agent-sdk-python) (synced to version in `UPSTREAM_VERSION`). Go 1.25+ (uses `iter.Seq`). One dependency, `golang.org/x/text`, supplying NFC path normalization and NFKC tag folding; both are load-bearing for matching the CLI's on-disk layout, so do not swap them for hand-rolled approximations. Communicates with the Claude CLI over stdin/stdout using newline-delimited JSON.

## Commands

```bash
go build ./...                       # Build everything
go test ./...                        # Run all tests
go test -run TestName ./...          # Run a single test
go vet ./...                         # Lint
gofumpt -l -d .                      # Check formatting (CI enforces this)
```

Some integration tests require a real Claude CLI install and are gated behind `AGENTSDK_TEST_REAL_CLI=1`.

## CI/CD

Three GitHub Actions workflows in `.github/workflows/`:

- **CI** (`ci.yml`): Runs on every push/PR. Matrix: Go 1.25 + 1.26. Steps: gofumpt check, `go vet`, `go build`, `go test -race`.
- **Integration Tests** (`integration.yml`): Daily schedule + manual dispatch. Installs the real Claude CLI via npm, runs `go test -race -run 'RealCLI'` with `AGENTSDK_TEST_REAL_CLI=1` and an API key.
- **Upstream Sync** (`upstream-sync.yml`): Daily schedule + manual dispatch. Compares `UPSTREAM_VERSION` against the latest `anthropics/claude-agent-sdk-python` release and opens/updates a GitHub issue when they diverge.

## Architecture

All code lives in the root `agentsdk` package (no internal packages — avoids import cycles since the protocol layer references the public types).

**Layered design:**

```
User Code
  ├── Query()       — one-shot, returns iter.Seq[Message]  (query.go)
  └── Client        — bidirectional, multi-turn             (client.go)
         │
      protocolQuery  — control protocol handler, goroutines + channels  (protocol.go)
         │
      Transport (interface)                                  (transport.go)
         │
      SubprocessTransport  — spawns Claude CLI, pipes stdin/stdout  (subprocess.go)
```

- **`SubprocessTransport`** (`subprocess.go`): Finds the CLI binary, builds ~35 CLI flags from `ClaudeAgentOptions`, manages the subprocess lifecycle. `ReadMessages` yields parsed JSON maps from stdout; a separate goroutine handles stderr.
- **`protocolQuery`** (`protocol.go`): Core read loop routes messages by `type` field — `control_response` matched to pending requests via `map[string]chan`, `control_request` dispatched to handler goroutines (CanUseTool, HookCallback), everything else parsed via `parseMessage` and sent to a buffered `chan Message`.
- **`parser.go`**: Switches on `data["type"]` to produce typed `Message` values. Returns `(nil, nil)` for unknown types (forward-compatible). Several system subtypes (`task_started`, `task_progress`, `task_notification`, `task_updated`, `hook_started`/`hook_response`, `mirror_error`) parse into richer types that *embed* `SystemMessage` — Go has no subclassing, so a type switch on `*SystemMessage` will not match them.
- **Session subsystem** (no CLI subprocess involved — these read and write the CLI's on-disk JSONL transcripts directly):
  - `sessions.go` — `ListSessions`, `GetSessionInfo`, `GetSessionMessages`, `ListSubagents`, `GetSubagentMessages`, plus the path sanitization and "lite" head/tail parse that mirror the CLI's own directory naming.
  - `sessionmutations.go` — `RenameSession`, `TagSession`, `DeleteSession`, `ForkSession`, and their `*ViaStore` variants.
  - `sessionstore.go` — the `SessionStore` interface (`Append`/`Load` required) plus the optional `SessionLister`, `SessionSummaryLister`, `SessionDeleter`, and `SessionSubkeyLister` capability interfaces, which call sites reach via type assertion.
  - `memorystore.go` — `InMemorySessionStore`, the reference adapter.
  - `summary.go` — `FoldSessionSummary`, the incremental summary adapters maintain inside `Append`.
  - `mirror.go` — the transcript-mirror batcher that coalesces `transcript_mirror` frames and retries failed appends.
  - `storesessions.go` — the store-backed read helpers (`*FromStore`).
  - `sessionresume.go` / `sessionimport.go` — materializing a store session into a temporary `CLAUDE_CONFIG_DIR` for resume, and the inverse replay of a local transcript into a store.

## Key Patterns

- **Sealed interfaces** with unexported marker methods: `Message` (`messageType()`), `ContentBlock` (`contentBlockType()`), `PermissionResult` (`permissionResult()`), `ThinkingConfig` (`thinkingConfigType()`), `McpServerConfig`, `HookInput`.
- **Error hierarchy**: unexported `sdkError` base (avoids field/method name collision with `Error()`), public constructors like `NewProcessError()`.
- **Test helpers**: `TestMain` in `integration_test.go` intercepts subprocess re-invocations via `AGENTSDK_TEST_HELPER` env var to simulate the CLI (modes: `fake-cli`, `echo-json`, `version`, etc.). `chanTransport` in `protocol_test.go` provides an in-memory mock transport.
- **CanUseTool callback**: When set on options, `configureCanUseTool` sets `PermissionPromptToolName = "stdio"` (and rejects the combination with an explicit prompt-tool name). The callback receives tool name, input, and permission context; returns `PermissionAllow` or `PermissionDeny`. `CanUseToolShadowedWarning` reports options that auto-approve tools before the callback runs.
- **Transcript byte shape**: Go sorts map keys when marshaling, but the CLI writes `"type"` first and the lite parse scans for a `{"type":"tag"` line prefix. Every transcript writer goes through `marshalTypeFirst` so the two agree.
- **Unicode normalization**: `normalizePath` (NFC) is applied wherever a path becomes a project key — `claudeConfigHomeDir`, `projectsDir`, `canonicalizePath`, and the worktree scan — because `sanitizePath` maps every non-alphanumeric rune to a hyphen, so a decomposed path yields a *different* key than its composed twin and the session would be invisible. `sanitizeUnicode` (NFKC) folds compatibility forms in tags and strips Cf/Co/Cs/Cn, looping to a fixed point since one pass can expose new characters. Unassigned code points use `unicode.Cn` directly — they cannot be derived from the one-letter aggregates, because `unicode.C` is generated to include unassigned ranges, so membership in it says nothing about whether a rune is assigned.
- **Remaining deviations from upstream**: no OpenTelemetry span injection (`TRACEPARENT`/`TRACESTATE` are inherited from the process environment instead), and in-process SDK MCP servers are out of scope, since they would require an MCP server dependency.

## Examples

The `examples/` directory contains 17 runnable programs demonstrating SDK usage: `quickstart`, `streaming`, `multiturn`, `tools`, `toolpermission`, `agents`, `filesystemagents`, `hooks`, `maxbudget`, `mcpserver`, `partialmessages`, `plugin`, `sessions`, `sessionstore`, `settingsources`, `stderr`, `systemprompt`.
