package agentsdk

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Eager-flush thresholds for the transcript mirror batcher.
const (
	maxPendingMirrorEntries = 500
	maxPendingMirrorBytes   = 1 << 20 // 1 MiB
	mirrorSendTimeout       = 60 * time.Second
)

// mirrorAppendBackoff bounds retries of transient adapter failures. Its
// length is one less than the attempt count: one delay between each pair of
// attempts.
var mirrorAppendBackoff = []time.Duration{200 * time.Millisecond, 800 * time.Millisecond}

// mirrorAppendMaxAttempts is the total number of SessionStore.Append
// attempts per batch.
const mirrorAppendMaxAttempts = 3

// mirrorFrame is one buffered transcript_mirror frame.
type mirrorFrame struct {
	filePath string
	entries  []SessionStoreEntry
	size     int
}

// transcriptMirrorBatcher accumulates transcript_mirror frames and flushes
// them to a SessionStore.
//
// The CLI subprocess emits {"type": "transcript_mirror", "filePath": ...,
// "entries": [...]} frames interleaved with normal SDK messages. The read
// loop peels these off and hands them to enqueue, which accumulates them and
// flushes to SessionStore.Append either when a result message arrives (an
// explicit flush) or when the pending buffer exceeds its size thresholds (an
// eager background flush). That keeps adapter latency off the hot path
// during model streaming.
//
// enqueue is fire-and-forget; flush blocks. Adapter failures are retried
// mirrorAppendMaxAttempts times with short backoff, though a timeout is not
// retried since the in-flight call may still land. Only after the final
// attempt fails is the batch dropped and reported through onError. Failures
// never propagate: the local-disk transcript is already durable, so the
// session must continue unaffected. Adapters should deduplicate by an
// entry's uuid when present, since a retried batch may partially overlap a
// prior partial write.
type transcriptMirrorBatcher struct {
	store       SessionStore
	projectsDir string
	onError     func(key *SessionKey, message string)
	sendTimeout time.Duration
	eager       bool

	// mu guards the pending buffer.
	mu             sync.Mutex
	pending        []mirrorFrame
	pendingEntries int
	pendingBytes   int

	// flushMu serializes flushes so appends reach the store in enqueue
	// order.
	flushMu sync.Mutex

	// wg tracks background eager flushes so close can wait for them.
	wg sync.WaitGroup
}

// newTranscriptMirrorBatcher creates a batcher for a store.
func newTranscriptMirrorBatcher(
	store SessionStore,
	projectsDir string,
	flushMode SessionStoreFlushMode,
	onError func(key *SessionKey, message string),
) *transcriptMirrorBatcher {
	return &transcriptMirrorBatcher{
		store:       store,
		projectsDir: projectsDir,
		onError:     onError,
		sendTimeout: mirrorSendTimeout,
		eager:       flushMode == SessionStoreFlushEager,
	}
}

// enqueue buffers a frame, scheduling a background flush when the eager
// flush mode is set or the pending thresholds are exceeded.
//
// rawEntries is the decoded "entries" value from the frame; entries that are
// not JSON objects are skipped.
func (b *transcriptMirrorBatcher) enqueue(filePath string, rawEntries any) {
	list, ok := rawEntries.([]any)
	if !ok || len(list) == 0 {
		return
	}

	entries := make([]SessionStoreEntry, 0, len(list))
	for _, raw := range list {
		if entry, ok := raw.(map[string]any); ok {
			entries = append(entries, SessionStoreEntry(entry))
		}
	}
	if len(entries) == 0 {
		return
	}

	// Approximate the wire size with one marshal per frame rather than per
	// entry, keeping this cheap relative to the decode the transport
	// already did.
	size := 0
	if encoded, err := json.Marshal(entries); err == nil {
		size = len(encoded)
	}

	b.mu.Lock()
	b.pending = append(b.pending, mirrorFrame{filePath: filePath, entries: entries, size: size})
	b.pendingEntries += len(entries)
	b.pendingBytes += size
	overThreshold := b.pendingEntries > maxPendingMirrorEntries || b.pendingBytes > maxPendingMirrorBytes
	b.mu.Unlock()

	if b.eager || overThreshold {
		// Fire and forget. The flush lock serializes against any in-flight
		// flush, so append ordering holds.
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			b.drain(context.Background())
		}()
	}
}

// flush sends all pending entries, serialized after any in-flight flush.
func (b *transcriptMirrorBatcher) flush(ctx context.Context) {
	b.drain(ctx)
}

// close performs a final flush before teardown. It waits for background
// flushes to finish first and never panics.
func (b *transcriptMirrorBatcher) close() {
	b.wg.Wait()
	// Detach from any cancelled caller context so the final batch still
	// reaches the store on client disconnect or interrupt.
	b.drain(context.Background())
}

// drain detaches the pending buffer, waits for any prior flush, then sends.
//
// Detaching happens before acquiring the flush lock so enqueue can keep
// accumulating into a fresh buffer while a prior flush is in flight.
func (b *transcriptMirrorBatcher) drain(ctx context.Context) {
	b.mu.Lock()
	items := b.pending
	b.pending = nil
	b.pendingEntries = 0
	b.pendingBytes = 0
	b.mu.Unlock()

	if len(items) == 0 {
		return
	}

	var errs []mirrorError
	b.flushMu.Lock()
	errs = b.doFlush(ctx, items)
	b.flushMu.Unlock()

	// Report errors after releasing the lock so a slow callback cannot
	// block subsequent drains, which need the lock only for append
	// ordering.
	if b.onError != nil {
		for _, e := range errs {
			b.onError(e.key, e.message)
		}
	}
}

// mirrorError pairs a failed batch's key with its error text.
type mirrorError struct {
	key     *SessionKey
	message string
}

// doFlush coalesces frames by file path and appends each file's entries to
// the store, retrying transient failures.
func (b *transcriptMirrorBatcher) doFlush(ctx context.Context, items []mirrorFrame) []mirrorError {
	// Coalesce by file path so each unique file gets one append per flush
	// rather than one per enqueued frame. Preserve first-seen path order,
	// and enqueue order within a path.
	var order []string
	byPath := map[string][]SessionStoreEntry{}
	for _, item := range items {
		if _, seen := byPath[item.filePath]; !seen {
			order = append(order, item.filePath)
		}
		byPath[item.filePath] = append(byPath[item.filePath], item.entries...)
	}

	var errs []mirrorError
	for _, filePath := range order {
		entries := byPath[filePath]
		if len(entries) == 0 {
			// Avoid creating phantom keys in adapters that touch storage on
			// an empty append: there is nothing to write.
			continue
		}

		key := filePathToSessionKey(filePath, b.projectsDir)
		if key == nil {
			log.Printf("agentsdk: dropping mirror frame: filePath %s is not under %s; "+
				"the subprocess CLAUDE_CONFIG_DIR likely differs from the parent's "+
				"(custom env or container?)", filePath, b.projectsDir)
			continue
		}

		if err := b.appendWithRetry(ctx, *key, entries); err != nil {
			log.Printf("agentsdk: transcript mirror flush failed for %s: %v", filePath, err)
			errs = append(errs, mirrorError{key: key, message: err.Error()})
		}
	}
	return errs
}

// appendWithRetry calls SessionStore.Append, retrying transient failures.
// A deadline is not retried: the context cancels the call, but cancellation
// is best-effort for adapters wrapping non-cancellable I/O, so the in-flight
// call may still land and a retry would launch a concurrent duplicate. It
// also keeps the worst-case lock hold near one timeout rather than three
// plus backoff.
func (b *transcriptMirrorBatcher) appendWithRetry(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	var lastErr error
	for attempt := 0; attempt < mirrorAppendMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(mirrorAppendBackoff[attempt-1]):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, b.sendTimeout)
		err := b.store.Append(attemptCtx, key, entries)
		timedOut := attemptCtx.Err() != nil
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err

		if timedOut {
			return err
		}
	}
	return lastErr
}

// filePathToSessionKey derives a SessionKey from an absolute transcript file
// path, returning nil when the path is not under projectsDir or has an
// unrecognized shape.
//
// Main transcripts look like
// <projectsDir>/<projectKey>/<sessionID>.jsonl, and subagent transcripts
// like <projectsDir>/<projectKey>/<sessionID>/subagents/agent-<id>.jsonl.
func filePathToSessionKey(filePath, projectsDirPath string) *SessionKey {
	rel, err := filepath.Rel(projectsDirPath, filePath)
	if err != nil {
		// On Windows Rel fails when the paths are on different drives.
		// Treat that as "not under projectsDir" so the batcher drops the
		// frame with a warning.
		return nil
	}
	if rel == "" || rel == "." || filepath.IsAbs(rel) {
		return nil
	}

	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || parts[0] == ".." {
		return nil
	}

	projectKey := parts[0]
	second := parts[1]

	// Main transcript: <projectKey>/<sessionID>.jsonl
	if len(parts) == 2 && strings.HasSuffix(second, ".jsonl") {
		return &SessionKey{
			ProjectKey: projectKey,
			SessionID:  strings.TrimSuffix(second, ".jsonl"),
		}
	}

	// Subagent transcript: <projectKey>/<sessionID>/subagents/.../agent-<id>.jsonl
	if len(parts) >= 4 {
		subpathParts := append([]string(nil), parts[2:]...)
		last := len(subpathParts) - 1
		subpathParts[last] = strings.TrimSuffix(subpathParts[last], ".jsonl")
		// Subpaths always join with "/" regardless of the OS separator, so
		// keys stay portable across platforms.
		return &SessionKey{
			ProjectKey: projectKey,
			SessionID:  second,
			Subpath:    strings.Join(subpathParts, "/"),
		}
	}

	return nil
}
