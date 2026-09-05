package agentsdk

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// recordingStore captures appends and can fail a configurable number of
// times before succeeding, to exercise the batcher's retry path.
type recordingStore struct {
	mu           sync.Mutex
	appends      []recordedAppend
	failures     int
	failuresLeft int
	blockFor     time.Duration
}

type recordedAppend struct {
	key     SessionKey
	entries []SessionStoreEntry
}

func (r *recordingStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	if r.blockFor > 0 {
		select {
		case <-time.After(r.blockFor):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.appends = append(r.appends, recordedAppend{key: key, entries: entries})
	if r.failuresLeft > 0 {
		r.failuresLeft--
		return errors.New("transient adapter failure")
	}
	return nil
}

func (r *recordingStore) Load(context.Context, SessionKey) ([]SessionStoreEntry, error) {
	return nil, nil
}

func (r *recordingStore) recorded() []recordedAppend {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedAppend(nil), r.appends...)
}

// mirrorFrameEntries builds the decoded "entries" value of a
// transcript_mirror frame.
func mirrorFrameEntries(entries ...map[string]any) any {
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = e
	}
	return out
}

func TestMirrorBatcherFlushCoalescesByPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)

	path := filepath.Join(root, "proj", testSessionID+".jsonl")
	batcher.enqueue(path, mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDA}))
	batcher.enqueue(path, mirrorFrameEntries(map[string]any{"type": "assistant", "uuid": testUUIDB}))

	// Nothing reaches the store until a flush.
	if len(store.recorded()) != 0 {
		t.Fatal("expected batched frames not to reach the store before a flush")
	}

	batcher.flush(context.Background())

	appends := store.recorded()
	if len(appends) != 1 {
		t.Fatalf("expected the frames to coalesce into 1 append, got %d", len(appends))
	}
	if len(appends[0].entries) != 2 {
		t.Fatalf("expected 2 coalesced entries, got %d", len(appends[0].entries))
	}
	if appends[0].key.SessionID != testSessionID || appends[0].key.ProjectKey != "proj" {
		t.Errorf("unexpected key: %+v", appends[0].key)
	}
	if appends[0].entries[0].EntryUUID() != testUUIDA {
		t.Error("expected entries to keep enqueue order")
	}
}

func TestMirrorBatcherSeparatesPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)

	mainPath := filepath.Join(root, "proj", testSessionID+".jsonl")
	subPath := filepath.Join(root, "proj", testSessionID, "subagents", "agent-abc.jsonl")

	batcher.enqueue(mainPath, mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDA}))
	batcher.enqueue(subPath, mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDB}))
	batcher.flush(context.Background())

	appends := store.recorded()
	if len(appends) != 2 {
		t.Fatalf("expected one append per file path, got %d", len(appends))
	}

	var sawSubpath bool
	for _, a := range appends {
		if a.key.Subpath == "subagents/agent-abc" {
			sawSubpath = true
		}
	}
	if !sawSubpath {
		t.Error("expected the subagent frame to derive a subpath key")
	}
}

func TestMirrorBatcherDropsFramesOutsideProjectsDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)

	batcher.enqueue(filepath.Join(t.TempDir(), "elsewhere.jsonl"),
		mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDA}))
	batcher.flush(context.Background())

	if len(store.recorded()) != 0 {
		t.Error("expected frames outside the projects directory to be dropped")
	}
}

func TestMirrorBatcherRetriesThenReportsError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{failuresLeft: 1}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)
	// Keep the test fast: the retry backoff is fixed, so only the timeout
	// needs shrinking.
	batcher.sendTimeout = time.Second

	path := filepath.Join(root, "proj", testSessionID+".jsonl")
	batcher.enqueue(path, mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDA}))
	batcher.flush(context.Background())

	if got := len(store.recorded()); got != 2 {
		t.Fatalf("expected one retry after a transient failure, got %d attempts", got)
	}
}

func TestMirrorBatcherReportsPermanentFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{failuresLeft: mirrorAppendMaxAttempts}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)
	batcher.sendTimeout = time.Second

	var (
		mu       sync.Mutex
		gotKey   *SessionKey
		gotError string
	)
	batcher.onError = func(key *SessionKey, message string) {
		mu.Lock()
		defer mu.Unlock()
		gotKey, gotError = key, message
	}

	path := filepath.Join(root, "proj", testSessionID+".jsonl")
	batcher.enqueue(path, mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDA}))
	batcher.flush(context.Background())

	if got := len(store.recorded()); got != mirrorAppendMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", mirrorAppendMaxAttempts, got)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotKey == nil || gotKey.SessionID != testSessionID {
		t.Fatalf("expected the failing key to be reported, got %+v", gotKey)
	}
	if gotError == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestMirrorBatcherEagerFlush(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushEager, nil)

	path := filepath.Join(root, "proj", testSessionID+".jsonl")
	batcher.enqueue(path, mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDA}))

	// Eager mode schedules a background flush per frame; close waits for it.
	batcher.close()

	if len(store.recorded()) == 0 {
		t.Error("expected the eager flush to reach the store")
	}
}

func TestMirrorBatcherIgnoresEmptyFrames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)

	path := filepath.Join(root, "proj", testSessionID+".jsonl")
	batcher.enqueue(path, mirrorFrameEntries())
	batcher.enqueue(path, nil)
	batcher.enqueue(path, "not a list")
	batcher.flush(context.Background())

	if len(store.recorded()) != 0 {
		t.Error("expected empty or malformed frames to produce no append")
	}
}

func TestMirrorBatcherCloseFlushesPending(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{}
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)

	path := filepath.Join(root, "proj", testSessionID+".jsonl")
	batcher.enqueue(path, mirrorFrameEntries(map[string]any{"type": "user", "uuid": testUUIDA}))
	batcher.close()

	if len(store.recorded()) != 1 {
		t.Error("expected close to flush pending entries")
	}
}
