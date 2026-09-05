package agentsdk

import (
	"context"
	"iter"
	"log"
	"path/filepath"
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
		if err := configureCanUseTool(opts); err != nil {
			finalErr = err
			return
		}
		if warning := CanUseToolShadowedWarning(opts); warning != "" {
			log.Print("agentsdk: " + warning)
		}

		// A store-backed resume has no local transcript to resume from, so
		// materialize one into a temporary config directory and repoint the
		// options at it before spawning.
		materialized, err := MaterializeResumeSession(ctx, opts)
		if err != nil {
			finalErr = err
			return
		}
		effectiveOpts := opts
		if materialized != nil {
			defer materialized.Cleanup()
			effectiveOpts = ApplyMaterializedOptions(opts, materialized)
		}

		transport := NewSubprocessTransport(effectiveOpts, "sdk-go")

		if err := transport.Connect(ctx); err != nil {
			finalErr = err
			return
		}
		defer transport.Close()

		q := newProtocolQuery(transport, effectiveOpts)

		// Mirror transcripts to the configured store, resolving the
		// projects directory to the materialized temporary directory when
		// there is one so file-path-to-key resolution matches what the
		// subprocess writes.
		if effectiveOpts.SessionStore != nil {
			mirrorProjectsDir := projectsDir(effectiveOpts.Env)
			if materialized != nil {
				mirrorProjectsDir = filepath.Join(materialized.ConfigDir, "projects")
			}
			mirror := newTranscriptMirrorBatcher(
				effectiveOpts.SessionStore,
				mirrorProjectsDir,
				effectiveOpts.SessionStoreFlush,
				func(key *SessionKey, message string) { q.reportMirrorError(key, message) },
			)
			q.setMirrorBatcher(mirror)
			defer mirror.close()
		}
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
