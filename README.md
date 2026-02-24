# Claude Agent SDK for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/pgavlin/claude-agent-sdk-go.svg)](https://pkg.go.dev/github.com/pgavlin/claude-agent-sdk-go)

Go SDK for building agents and automations on top of [Claude Code](https://docs.anthropic.com/en/docs/claude-code). It communicates with the Claude Code CLI over stdin/stdout using newline-delimited JSON. Zero external dependencies.

## Requirements

- Go 1.23+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) 2.0+

## Installation

```bash
go get github.com/pgavlin/claude-agent-sdk-go
```

## Quick start

### One-shot query

Use `Query` to send a single prompt and iterate over the response:

```go
package main

import (
	"context"
	"fmt"
	"log"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	ctx := context.Background()
	opts := &agentsdk.ClaudeAgentOptions{
		PermissionMode: agentsdk.PermissionModePlan,
	}

	messages, errFn := agentsdk.Query(ctx, "What is the capital of France?", opts)
	for msg := range messages {
		if m, ok := msg.(*agentsdk.AssistantMessage); ok {
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Println(tb.Text)
				}
			}
		}
	}
	if err := errFn(); err != nil {
		log.Fatal(err)
	}
}
```

### Interactive client

Use `Client` for multi-turn sessions:

```go
client := agentsdk.NewClient(&agentsdk.ClaudeAgentOptions{
	PermissionMode: agentsdk.PermissionModePlan,
})

if err := client.Connect(ctx); err != nil {
	log.Fatal(err)
}
defer client.Close()

client.SendQuery(ctx, "Name three planets.", "session-1")
for msg := range client.ReceiveResponse() {
	// handle msg
}

client.SendQuery(ctx, "Name three more.", "session-1")
for msg := range client.ReceiveResponse() {
	// handle msg
}
```

## Handling tools and permissions

Set the `CanUseTool` callback to intercept tool-use requests. The SDK automatically configures the CLI's permission prompt when this callback is set.

```go
client := agentsdk.NewClient(&agentsdk.ClaudeAgentOptions{
	PermissionMode: agentsdk.PermissionModeDefault,
	CanUseTool: func(
		ctx context.Context,
		toolName string,
		input map[string]any,
		permCtx agentsdk.ToolPermissionContext,
	) (agentsdk.PermissionResult, error) {
		if toolName == "Bash" {
			return agentsdk.PermissionDeny{Message: "no shell access"}, nil
		}
		return agentsdk.PermissionAllow{}, nil
	},
})
```

## Examples

The [`examples/`](examples/) directory contains complete working programs:

| Example | Description |
|---------|-------------|
| [`quickstart`](examples/quickstart) | Basic query, options, and tool usage patterns |
| [`streaming`](examples/streaming) | Streaming responses with the Client API |
| [`multiturn`](examples/multiturn) | Multi-turn conversation preserving context |
| [`tools`](examples/tools) | Control available tools: array, empty, or preset |
| [`toolpermission`](examples/toolpermission) | Programmatically allow/deny and modify tool usage |
| [`agents`](examples/agents) | Define and use custom agents with specific tools and models |
| [`filesystemagents`](examples/filesystemagents) | Load agents from `.claude/agents/` markdown files |
| [`hooks`](examples/hooks) | Intercept tool use and prompt submission with hooks |
| [`maxbudget`](examples/maxbudget) | Control API costs with `MaxBudgetUSD` option |
| [`mcpserver`](examples/mcpserver) | Configure MCP servers via stdio, SSE, HTTP transports |
| [`partialmessages`](examples/partialmessages) | Stream incremental updates with `IncludePartialMessages` |
| [`plugin`](examples/plugin) | Load and configure local plugins |
| [`settingsources`](examples/settingsources) | Control which settings load (user/project/default) |
| [`stderr`](examples/stderr) | Capture CLI debug output via `Stderr` callback |
| [`systemprompt`](examples/systemprompt) | Configure system prompt: none, string, preset, or appended |

Run any example with:

```bash
go run ./examples/quickstart
```

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
