package agentsdk

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func storeEntry(fields map[string]any) SessionStoreEntry {
	return SessionStoreEntry(fields)
}

func TestInMemorySessionStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	key := SessionKey{ProjectKey: "proj", SessionID: testSessionID}

	if entries, err := store.Load(ctx, key); err != nil || entries != nil {
		t.Fatalf("expected a nil load for an unwritten key, got %v, %v", entries, err)
	}

	first := []SessionStoreEntry{storeEntry(map[string]any{"type": "user", "uuid": testUUIDA})}
	if err := store.Append(ctx, key, first); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	second := []SessionStoreEntry{storeEntry(map[string]any{"type": "assistant", "uuid": testUUIDB})}
	if err := store.Append(ctx, key, second); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	loaded, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}
	if loaded[0].EntryUUID() != testUUIDA || loaded[1].EntryUUID() != testUUIDB {
		t.Error("expected entries in append order")
	}

	// Load returns a copy, so mutating it must not corrupt the store.
	loaded[0]["uuid"] = "mutated"
	reloaded, _ := store.Load(ctx, key)
	if reloaded[0].EntryUUID() != testUUIDA {
		t.Error("expected Load to return a copy of the stored entries")
	}
}

func TestInMemorySessionStoreListAndDelete(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	mainKey := SessionKey{ProjectKey: "proj", SessionID: testSessionID}
	subKey := SessionKey{ProjectKey: "proj", SessionID: testSessionID, Subpath: "subagents/agent-abc"}

	_ = store.Append(ctx, mainKey, []SessionStoreEntry{storeEntry(map[string]any{"type": "user", "uuid": testUUIDA})})
	_ = store.Append(ctx, subKey, []SessionStoreEntry{storeEntry(map[string]any{"type": "user", "uuid": testUUIDB})})

	listing, err := store.ListSessions(ctx, "proj")
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(listing) != 1 || listing[0].SessionID != testSessionID {
		t.Fatalf("expected only the main transcript, got %+v", listing)
	}
	if listing[0].Mtime == 0 {
		t.Error("expected a non-zero mtime")
	}

	subkeys, err := store.ListSubkeys(ctx, SessionListSubkeysKey{ProjectKey: "proj", SessionID: testSessionID})
	if err != nil {
		t.Fatalf("ListSubkeys failed: %v", err)
	}
	if len(subkeys) != 1 || subkeys[0] != "subagents/agent-abc" {
		t.Fatalf("unexpected subkeys: %v", subkeys)
	}

	// Deleting the main transcript cascades to its subkeys.
	if err := store.Delete(ctx, mainKey); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if entries, _ := store.Load(ctx, mainKey); entries != nil {
		t.Error("expected the main transcript to be deleted")
	}
	if entries, _ := store.Load(ctx, subKey); entries != nil {
		t.Error("expected the delete to cascade to subagent transcripts")
	}
}

func TestInMemorySessionStoreMtimeMonotonic(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()

	var last int64
	for i := 0; i < 5; i++ {
		key := SessionKey{ProjectKey: "proj", SessionID: testSessionID}
		_ = store.Append(ctx, key, []SessionStoreEntry{storeEntry(map[string]any{"type": "user"})})
		listing, _ := store.ListSessions(ctx, "proj")
		if listing[0].Mtime <= last {
			t.Fatalf("expected strictly increasing mtimes, got %d after %d", listing[0].Mtime, last)
		}
		last = listing[0].Mtime
	}
}

func TestFoldSessionSummary(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: testSessionID}

	summary := FoldSessionSummary(nil, key, []SessionStoreEntry{
		storeEntry(map[string]any{
			"type": "user", "uuid": testUUIDA, "cwd": "/work",
			"timestamp": "2026-01-01T00:00:00.000Z",
			"message":   map[string]any{"role": "user", "content": "first prompt"},
		}),
	})
	if summary.SessionID != testSessionID {
		t.Errorf("unexpected session ID: %q", summary.SessionID)
	}
	if summary.Mtime != 0 {
		t.Error("expected the fold to leave mtime for the adapter to stamp")
	}
	if summary.Data["first_prompt"] != "first prompt" {
		t.Errorf("unexpected first prompt: %v", summary.Data["first_prompt"])
	}
	if summary.Data["cwd"] != "/work" {
		t.Errorf("unexpected cwd: %v", summary.Data["cwd"])
	}
	if summary.Data["created_at"] == nil {
		t.Error("expected created_at to latch from the first timestamp")
	}

	// Last-wins fields overwrite, and the first prompt stays latched.
	summary = FoldSessionSummary(&summary, key, []SessionStoreEntry{
		storeEntry(map[string]any{"type": "custom-title", "customTitle": "Title A"}),
		storeEntry(map[string]any{"type": "custom-title", "customTitle": "Title B"}),
		storeEntry(map[string]any{
			"type": "user", "uuid": testUUIDB,
			"message": map[string]any{"role": "user", "content": "second prompt"},
		}),
	})
	if summary.Data["custom_title"] != "Title B" {
		t.Errorf("expected the last title to win, got %v", summary.Data["custom_title"])
	}
	if summary.Data["first_prompt"] != "first prompt" {
		t.Errorf("expected the first prompt to stay latched, got %v", summary.Data["first_prompt"])
	}

	// A tag entry sets the tag; an empty one clears it.
	summary = FoldSessionSummary(&summary, key, []SessionStoreEntry{
		storeEntry(map[string]any{"type": "tag", "tag": "experiment"}),
	})
	if summary.Data["tag"] != "experiment" {
		t.Errorf("unexpected tag: %v", summary.Data["tag"])
	}
	summary = FoldSessionSummary(&summary, key, []SessionStoreEntry{
		storeEntry(map[string]any{"type": "tag", "tag": ""}),
	})
	if _, ok := summary.Data["tag"]; ok {
		t.Error("expected an empty tag entry to clear the tag")
	}

	info := SummaryEntryToSDKInfo(summary, "/work")
	if info == nil {
		t.Fatal("expected an SDKSessionInfo")
	}
	if info.Summary != "Title B" {
		t.Errorf("expected the custom title as the summary, got %q", info.Summary)
	}
	if info.FileSize != nil {
		t.Error("expected no file size for a store-backed session")
	}
}

func TestFoldSessionSummaryDropsSidechain(t *testing.T) {
	key := SessionKey{ProjectKey: "proj", SessionID: testSessionID}
	summary := FoldSessionSummary(nil, key, []SessionStoreEntry{
		storeEntry(map[string]any{
			"type": "user", "uuid": testUUIDA, "isSidechain": true,
			"message": map[string]any{"role": "user", "content": "sub work"},
		}),
	})
	if SummaryEntryToSDKInfo(summary, "") != nil {
		t.Error("expected sidechain sessions to yield no session info")
	}
}

func TestListSessionsFromStoreUsesSummaries(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	dir := t.TempDir()
	projectKey := ProjectKeyForDirectory(dir)

	key := SessionKey{ProjectKey: projectKey, SessionID: testSessionID}
	_ = store.Append(ctx, key, []SessionStoreEntry{
		storeEntry(map[string]any{
			"type": "user", "uuid": testUUIDA, "timestamp": "2026-01-01T00:00:00.000Z",
			"message": map[string]any{"role": "user", "content": "store prompt"},
		}),
	})

	sessions, err := ListSessionsFromStore(ctx, store, dir, 0, 0)
	if err != nil {
		t.Fatalf("ListSessionsFromStore failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Summary != "store prompt" {
		t.Errorf("unexpected summary: %q", sessions[0].Summary)
	}
	if sessions[0].SessionID != testSessionID {
		t.Errorf("unexpected session ID: %q", sessions[0].SessionID)
	}
}

// minimalStore implements only the required SessionStore methods, so the
// optional-capability branches can be exercised.
type minimalStore struct {
	mu      sync.Mutex
	entries map[string][]SessionStoreEntry
	err     error
	appends int
}

func newMinimalStore() *minimalStore {
	return &minimalStore{entries: map[string][]SessionStoreEntry{}}
}

func (m *minimalStore) Append(_ context.Context, key SessionKey, entries []SessionStoreEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appends++
	if m.err != nil {
		return m.err
	}
	k := keyToString(key)
	m.entries[k] = append(m.entries[k], entries...)
	return nil
}

func (m *minimalStore) Load(_ context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.entries[keyToString(key)]
	if !ok {
		return nil, nil
	}
	return append([]SessionStoreEntry(nil), entries...), nil
}

func TestListSessionsFromStoreRequiresCapability(t *testing.T) {
	_, err := ListSessionsFromStore(context.Background(), newMinimalStore(), t.TempDir(), 0, 0)
	if err == nil {
		t.Fatal("expected an error for a store with no listing capability")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Errorf("expected a ValidationError, got %T", err)
	}
}

func TestListSubagentsFromStoreRequiresCapability(t *testing.T) {
	_, err := ListSubagentsFromStore(context.Background(), newMinimalStore(), testSessionID, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a store with no subkey capability")
	}
}

func TestDeleteSessionViaStoreIsNoOpWithoutCapability(t *testing.T) {
	// A store without SessionDeleter makes deletion a no-op, which suits
	// append-only backends.
	if err := DeleteSessionViaStore(context.Background(), newMinimalStore(), testSessionID, ""); err != nil {
		t.Errorf("expected a no-op delete, got %v", err)
	}
}

func TestGetSessionMessagesFromStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	dir := t.TempDir()
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(dir), SessionID: testSessionID}

	_ = store.Append(ctx, key, []SessionStoreEntry{
		storeEntry(userEntry(testUUIDA, "", "first")),
		storeEntry(assistantEntry(testUUIDB, testUUIDA, "reply")),
		// Metadata entries must not reach the chain builder.
		storeEntry(map[string]any{"type": "custom-title", "customTitle": "ignored"}),
	})

	messages, err := GetSessionMessagesFromStore(ctx, store, testSessionID, dir, 0, 0)
	if err != nil {
		t.Fatalf("GetSessionMessagesFromStore failed: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].UUID != testUUIDA || messages[1].UUID != testUUIDB {
		t.Error("expected messages in chronological order")
	}
}

func TestGetSubagentMessagesFromStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	dir := t.TempDir()
	projectKey := ProjectKeyForDirectory(dir)

	subKey := SessionKey{ProjectKey: projectKey, SessionID: testSessionID, Subpath: "subagents/agent-abc"}
	_ = store.Append(ctx, subKey, []SessionStoreEntry{
		storeEntry(map[string]any{"type": "agent_metadata", "toolUseId": "toolu_1", "parentAgentId": "agent-p"}),
		storeEntry(userEntry(testUUIDA, "", "sub prompt")),
		storeEntry(assistantEntry(testUUIDB, testUUIDA, "sub reply")),
	})

	ids, err := ListSubagentsFromStore(ctx, store, testSessionID, dir)
	if err != nil {
		t.Fatalf("ListSubagentsFromStore failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != "abc" {
		t.Fatalf("unexpected subagent IDs: %v", ids)
	}

	messages, err := GetSubagentMessagesFromStore(ctx, store, testSessionID, "abc", dir, 0, 0)
	if err != nil {
		t.Fatalf("GetSubagentMessagesFromStore failed: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	for _, msg := range messages {
		if msg.ParentToolUseID != "toolu_1" || msg.ParentAgentID != "agent-p" {
			t.Errorf("expected parent IDs from the agent_metadata entry, got %+v", msg)
		}
	}
}

func TestMarshalTypeFirst(t *testing.T) {
	encoded, err := marshalTypeFirst(storeEntry(map[string]any{"tag": "x", "type": "tag", "sessionId": "s"}))
	if err != nil {
		t.Fatalf("marshalTypeFirst failed: %v", err)
	}
	if !strings.HasPrefix(string(encoded), `{"type":"tag"`) {
		t.Errorf("expected the type key first, got %s", encoded)
	}

	// An entry with no type still round-trips.
	encoded, err = marshalTypeFirst(storeEntry(map[string]any{"a": 1}))
	if err != nil {
		t.Fatalf("marshalTypeFirst failed: %v", err)
	}
	if string(encoded) != `{"a":1}` {
		t.Errorf("unexpected encoding: %s", encoded)
	}

	// A type-only entry produces a well-formed object.
	encoded, err = marshalTypeFirst(storeEntry(map[string]any{"type": "tag"}))
	if err != nil {
		t.Fatalf("marshalTypeFirst failed: %v", err)
	}
	if string(encoded) != `{"type":"tag"}` {
		t.Errorf("unexpected encoding: %s", encoded)
	}
}

func TestFilePathToSessionKey(t *testing.T) {
	root := filepath.Join("tmp", "config", "projects")

	main := filePathToSessionKey(filepath.Join(root, "proj", testSessionID+".jsonl"), root)
	if main == nil {
		t.Fatal("expected a key for a main transcript")
	}
	if main.ProjectKey != "proj" || main.SessionID != testSessionID || main.Subpath != "" {
		t.Errorf("unexpected main key: %+v", main)
	}

	sub := filePathToSessionKey(
		filepath.Join(root, "proj", testSessionID, "subagents", "agent-abc.jsonl"), root)
	if sub == nil {
		t.Fatal("expected a key for a subagent transcript")
	}
	if sub.Subpath != "subagents/agent-abc" {
		t.Errorf("unexpected subpath: %q", sub.Subpath)
	}

	// Paths outside the projects directory are rejected so the batcher drops
	// the frame rather than writing under an unrelated key.
	if got := filePathToSessionKey(filepath.Join("elsewhere", "x.jsonl"), root); got != nil {
		t.Errorf("expected nil for a path outside the projects dir, got %+v", got)
	}
	if got := filePathToSessionKey(filepath.Join(root, "proj"), root); got != nil {
		t.Errorf("expected nil for a non-transcript path, got %+v", got)
	}
}

func TestSafeSubpathTarget(t *testing.T) {
	sessionDir := t.TempDir()

	if _, ok := safeSubpathTarget("subagents/agent-abc", sessionDir); !ok {
		t.Error("expected a normal subagent subpath to be accepted")
	}

	for _, bad := range []string{
		"",
		"/etc/passwd",
		`\windows\system32`,
		"../escape",
		"subagents/../../escape",
		"C:evil",
		"subagents/./agent",
	} {
		if _, ok := safeSubpathTarget(bad, sessionDir); ok {
			t.Errorf("expected subpath %q to be rejected", bad)
		}
	}
}
