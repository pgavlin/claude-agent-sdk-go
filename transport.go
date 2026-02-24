package agentsdk

import (
	"context"
	"iter"
)

// Transport is the interface for communicating with the Claude CLI.
type Transport interface {
	// Connect establishes the connection to the CLI process.
	Connect(ctx context.Context) error

	// Write sends a string (JSON line) to the CLI's stdin.
	Write(ctx context.Context, data string) error

	// ReadMessages returns an iterator over raw JSON messages from the CLI's stdout.
	// Each yielded value is either a parsed JSON object or an error.
	ReadMessages(ctx context.Context) iter.Seq2[map[string]any, error]

	// Close terminates the connection and cleans up resources.
	Close() error

	// EndInput signals that no more input will be sent (closes stdin).
	EndInput() error

	// IsReady reports whether the transport is connected and ready for I/O.
	IsReady() bool
}
