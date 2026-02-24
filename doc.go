// Package agentsdk is a Go SDK for the Claude Code CLI.
//
// It communicates with the Claude Code CLI over stdin/stdout using
// newline-delimited JSON and provides two entry points:
//
//   - [Query] for one-shot prompts that return an iterator over response messages.
//   - [Client] for interactive, multi-turn sessions with full bidirectional control.
//
// # Quick start
//
//	messages, errFn := agentsdk.Query(ctx, "Hello", nil)
//	for msg := range messages {
//	    if m, ok := msg.(*agentsdk.AssistantMessage); ok {
//	        for _, block := range m.Content {
//	            if tb, ok := block.(agentsdk.TextBlock); ok {
//	                fmt.Println(tb.Text)
//	            }
//	        }
//	    }
//	}
//	if err := errFn(); err != nil {
//	    log.Fatal(err)
//	}
//
// See the [examples] directory for complete working programs.
//
// [examples]: https://github.com/pgavlin/claude-agent-sdk-go/tree/main/examples
package agentsdk
