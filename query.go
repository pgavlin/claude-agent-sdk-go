package agentsdk

import (
	"context"
	"iter"
)

// Query sends a one-shot prompt to the Claude CLI and returns an iterator
// over the response messages. The returned function reports any error that
// occurred after iteration completes.
//
// Usage:
//
//	messages, errFn := agentsdk.Query(ctx, "Hello", nil)
//	for msg := range messages {
//	    // handle msg
//	}
//	if err := errFn(); err != nil {
//	    // handle error
//	}
func Query(ctx context.Context, prompt string, opts *ClaudeAgentOptions) (iter.Seq[Message], func() error) {
	if opts == nil {
		opts = &ClaudeAgentOptions{}
	}

	var finalErr error

	seq := func(yield func(Message) bool) {
		transport := NewSubprocessTransport(opts, "sdk-go")

		if err := transport.Connect(ctx); err != nil {
			finalErr = err
			return
		}
		defer transport.Close()

		q := newProtocolQuery(transport, opts)
		q.Start(ctx)

		if _, err := q.Initialize(ctx); err != nil {
			finalErr = err
			return
		}

		if err := q.WriteUserMessage(ctx, prompt, ""); err != nil {
			finalErr = err
			return
		}

		if err := q.EndInput(); err != nil {
			finalErr = err
			return
		}

		for msg := range q.Messages() {
			if !yield(msg) {
				return
			}
		}
	}

	return seq, func() error { return finalErr }
}
