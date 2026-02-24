# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Go SDK for the Claude Code CLI (`github.com/pgavlin/claude-agent-sdk-go`, package `agentsdk`). Ported from the upstream [Python SDK](https://github.com/anthropics/claude-agent-sdk-python) (synced to version in `UPSTREAM_VERSION`). Zero external dependencies, Go 1.23+ (uses `iter.Seq`). Communicates with the Claude CLI over stdin/stdout using newline-delimited JSON.

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

- **CI** (`ci.yml`): Runs on every push/PR. Matrix: Go 1.23 + 1.24. Steps: gofumpt check, `go vet`, `go build`, `go test -race`.
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
- **`parser.go`**: Switches on `data["type"]` to produce typed `Message` values. Returns `(nil, nil)` for unknown types (forward-compatible).

## Key Patterns

- **Sealed interfaces** with unexported marker methods: `Message` (`messageType()`), `ContentBlock` (`contentBlockType()`), `PermissionResult` (`permissionResult()`), `ThinkingConfig` (`thinkingConfigType()`), `McpServerConfig`, `HookInput`.
- **Error hierarchy**: unexported `sdkError` base (avoids field/method name collision with `Error()`), public constructors like `NewProcessError()`.
- **Test helpers**: `TestMain` in `integration_test.go` intercepts subprocess re-invocations via `AGENTSDK_TEST_HELPER` env var to simulate the CLI (modes: `fake-cli`, `echo-json`, `version`, etc.). `chanTransport` in `protocol_test.go` provides an in-memory mock transport.
- **CanUseTool callback**: When set on options, `NewClient` auto-sets `PermissionPromptToolName = "stdio"`. The callback receives tool name, input, and permission context; returns `PermissionAllow` or `PermissionDeny`.

## Examples

The `examples/` directory contains 15 runnable programs demonstrating SDK usage: `quickstart`, `streaming`, `multiturn`, `tools`, `toolpermission`, `agents`, `filesystemagents`, `hooks`, `maxbudget`, `mcpserver`, `partialmessages`, `plugin`, `settingsources`, `stderr`, `systemprompt`.
