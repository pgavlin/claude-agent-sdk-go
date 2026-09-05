package agentsdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameSession(t *testing.T) {
	root := withProjectsDir(t)
	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "original prompt"),
	})

	if err := RenameSession(testSessionID, "  New title  ", ""); err != nil {
		t.Fatalf("RenameSession failed: %v", err)
	}

	info := GetSessionInfo(testSessionID, "")
	if info == nil {
		t.Fatal("expected to find the session")
	}
	if info.CustomTitle != "New title" {
		t.Errorf("expected the title to be trimmed and stored, got %q", info.CustomTitle)
	}

	// The last title wins, so repeated renames are safe.
	if err := RenameSession(testSessionID, "Newer title", ""); err != nil {
		t.Fatalf("second RenameSession failed: %v", err)
	}
	if info := GetSessionInfo(testSessionID, ""); info.CustomTitle != "Newer title" {
		t.Errorf("expected the most recent title, got %q", info.CustomTitle)
	}
}

func TestRenameSessionValidation(t *testing.T) {
	withProjectsDir(t)

	if err := RenameSession("not-a-uuid", "Title", ""); err == nil {
		t.Error("expected an error for an invalid session ID")
	}
	if err := RenameSession(testSessionID, "   ", ""); err == nil {
		t.Error("expected an error for a whitespace-only title")
	}
	if err := RenameSession(testSessionID, "Title", ""); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestTagSession(t *testing.T) {
	root := withProjectsDir(t)
	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "prompt"),
	})

	if err := TagSession(testSessionID, "experiment", ""); err != nil {
		t.Fatalf("TagSession failed: %v", err)
	}
	if info := GetSessionInfo(testSessionID, ""); info.Tag != "experiment" {
		t.Errorf("unexpected tag: %q", info.Tag)
	}

	// An empty tag clears it.
	if err := TagSession(testSessionID, "", ""); err != nil {
		t.Fatalf("clearing the tag failed: %v", err)
	}
	if info := GetSessionInfo(testSessionID, ""); info.Tag != "" {
		t.Errorf("expected the tag to be cleared, got %q", info.Tag)
	}
}

func TestTagSessionSanitizesUnicode(t *testing.T) {
	root := withProjectsDir(t)
	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "prompt"),
	})

	// A zero-width space and an RTL override are stripped.
	if err := TagSession(testSessionID, "safe\u200btag\u202e", ""); err != nil {
		t.Fatalf("TagSession failed: %v", err)
	}
	if info := GetSessionInfo(testSessionID, ""); info.Tag != "safetag" {
		t.Errorf("expected dangerous characters to be stripped, got %q", info.Tag)
	}

	// A tag made only of stripped characters is rejected rather than
	// silently clearing the tag.
	if err := TagSession(testSessionID, "\u200b\u200b", ""); err == nil {
		t.Error("expected an error for a tag that sanitizes away entirely")
	}
}

func TestSanitizeUnicode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"zero\u200bwidth", "zerowidth"},
		{"rtl\u202eoverride", "rtloverride"},
		{"bom\ufeffmark", "bommark"},
		{"private\uf8ffuse", "privateuse"},
	} {
		if got := sanitizeUnicode(tc.in); got != tc.want {
			t.Errorf("sanitizeUnicode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDeleteSession(t *testing.T) {
	root := withProjectsDir(t)
	projectDir := writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "prompt"),
	})

	// A subagent directory beside the transcript must be removed too.
	subagentsDir := filepath.Join(projectDir, testSessionID, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("failed to create subagents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subagentsDir, "agent-abc.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("failed to write subagent transcript: %v", err)
	}

	if err := DeleteSession(testSessionID, ""); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, testSessionID+".jsonl")); !os.IsNotExist(err) {
		t.Error("expected the transcript to be deleted")
	}
	if _, err := os.Stat(filepath.Join(projectDir, testSessionID)); !os.IsNotExist(err) {
		t.Error("expected the subagent directory to be deleted")
	}

	if err := DeleteSession(testSessionID, ""); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound on a second delete, got %v", err)
	}
}

func TestForkSession(t *testing.T) {
	root := withProjectsDir(t)
	projectDir := writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "first"),
		assistantEntry(testUUIDB, testUUIDA, "reply"),
		userEntry(testUUIDC, testUUIDB, "second"),
	})

	result, err := ForkSession(testSessionID, ForkSessionOptions{})
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}
	if validateUUID(result.SessionID) == "" {
		t.Fatalf("expected a UUID for the fork, got %q", result.SessionID)
	}
	if result.SessionID == testSessionID {
		t.Error("expected the fork to get a new session ID")
	}

	forkPath := filepath.Join(projectDir, result.SessionID+".jsonl")
	content, err := os.ReadFile(forkPath)
	if err != nil {
		t.Fatalf("failed to read the fork: %v", err)
	}

	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("fork line is not valid JSON: %v", err)
		}
		entries = append(entries, entry)
	}

	// Three transcript entries plus the appended custom-title entry.
	if len(entries) != 4 {
		t.Fatalf("expected 4 fork entries, got %d", len(entries))
	}

	seenUUIDs := map[string]bool{}
	for _, entry := range entries {
		if entry["sessionId"] != result.SessionID {
			t.Errorf("expected every entry to carry the fork session ID, got %v", entry["sessionId"])
		}
		uuid, _ := entry["uuid"].(string)
		if uuid == testUUIDA || uuid == testUUIDB || uuid == testUUIDC {
			t.Errorf("expected every UUID to be remapped, found the original %q", uuid)
		}
		if seenUUIDs[uuid] {
			t.Errorf("duplicate UUID in the fork: %q", uuid)
		}
		seenUUIDs[uuid] = true
	}

	// The parent chain is preserved through the remapping.
	if entries[0]["parentUuid"] != nil {
		t.Errorf("expected the root to have no parent, got %v", entries[0]["parentUuid"])
	}
	if entries[1]["parentUuid"] != entries[0]["uuid"] {
		t.Error("expected the second entry to point at the remapped root")
	}
	if entries[2]["parentUuid"] != entries[1]["uuid"] {
		t.Error("expected the third entry to point at the remapped second entry")
	}

	// forkedFrom records the source.
	forkedFrom, ok := entries[0]["forkedFrom"].(map[string]any)
	if !ok {
		t.Fatal("expected a forkedFrom record")
	}
	if forkedFrom["sessionId"] != testSessionID || forkedFrom["messageUuid"] != testUUIDA {
		t.Errorf("unexpected forkedFrom: %+v", forkedFrom)
	}

	// The derived title gets a fork suffix.
	title, _ := entries[3]["customTitle"].(string)
	if !strings.HasSuffix(title, " (fork)") {
		t.Errorf("expected a derived title with a fork suffix, got %q", title)
	}
}

func TestForkSessionUpToMessage(t *testing.T) {
	root := withProjectsDir(t)
	projectDir := writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "first"),
		assistantEntry(testUUIDB, testUUIDA, "reply"),
		userEntry(testUUIDC, testUUIDB, "second"),
	})

	result, err := ForkSession(testSessionID, ForkSessionOptions{
		UpToMessageID: testUUIDB,
		Title:         "Explicit title",
	})
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(projectDir, result.SessionID+".jsonl"))
	if err != nil {
		t.Fatalf("failed to read the fork: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")

	// Two transcript entries up to the cutoff, plus the title entry.
	if len(lines) != 3 {
		t.Fatalf("expected the transcript to be truncated to 2 entries plus a title, got %d lines", len(lines))
	}

	var titleEntry map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &titleEntry); err != nil {
		t.Fatalf("title entry is not valid JSON: %v", err)
	}
	if titleEntry["customTitle"] != "Explicit title" {
		t.Errorf("expected the explicit title without a suffix, got %v", titleEntry["customTitle"])
	}
}

func TestForkSessionValidation(t *testing.T) {
	root := withProjectsDir(t)
	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "prompt"),
	})

	if _, err := ForkSession("not-a-uuid", ForkSessionOptions{}); err == nil {
		t.Error("expected an error for an invalid session ID")
	}
	if _, err := ForkSession(testSessionID, ForkSessionOptions{UpToMessageID: "nope"}); err == nil {
		t.Error("expected an error for an invalid up-to message ID")
	}

	// A UUID that is valid but absent from the transcript is rejected.
	_, err := ForkSession(testSessionID, ForkSessionOptions{UpToMessageID: testUUIDC})
	if err == nil {
		t.Error("expected an error for a message not present in the transcript")
	}
}

func TestForkSessionSkipsSidechains(t *testing.T) {
	root := withProjectsDir(t)
	sidechain := userEntry(testUUIDB, testUUIDA, "sub work")
	sidechain["isSidechain"] = true

	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "main"),
		sidechain,
	})

	result, err := ForkSession(testSessionID, ForkSessionOptions{})
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}

	messages := GetSessionMessages(result.SessionID, "", 0, 0)
	if len(messages) != 1 {
		t.Errorf("expected sidechain entries to be excluded from the fork, got %d messages", len(messages))
	}
}

func TestRenameAndTagViaStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	dir := t.TempDir()
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(dir), SessionID: testSessionID}

	_ = store.Append(ctx, key, []SessionStoreEntry{storeEntry(userEntry(testUUIDA, "", "prompt"))})

	if err := RenameSessionViaStore(ctx, store, testSessionID, "Store title", dir); err != nil {
		t.Fatalf("RenameSessionViaStore failed: %v", err)
	}
	if err := TagSessionViaStore(ctx, store, testSessionID, "store-tag", dir); err != nil {
		t.Fatalf("TagSessionViaStore failed: %v", err)
	}

	info, err := GetSessionInfoFromStore(ctx, store, testSessionID, dir)
	if err != nil {
		t.Fatalf("GetSessionInfoFromStore failed: %v", err)
	}
	if info == nil {
		t.Fatal("expected session info")
	}
	if info.CustomTitle != "Store title" {
		t.Errorf("unexpected title: %q", info.CustomTitle)
	}
	if info.Tag != "store-tag" {
		t.Errorf("unexpected tag: %q", info.Tag)
	}
}

func TestForkSessionViaStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	dir := t.TempDir()
	projectKey := ProjectKeyForDirectory(dir)
	key := SessionKey{ProjectKey: projectKey, SessionID: testSessionID}

	_ = store.Append(ctx, key, []SessionStoreEntry{
		storeEntry(userEntry(testUUIDA, "", "first")),
		storeEntry(assistantEntry(testUUIDB, testUUIDA, "reply")),
	})

	result, err := ForkSessionViaStore(ctx, store, testSessionID, ForkSessionOptions{Directory: dir})
	if err != nil {
		t.Fatalf("ForkSessionViaStore failed: %v", err)
	}
	if result.SessionID == testSessionID {
		t.Error("expected a new session ID for the fork")
	}

	forked, err := store.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: result.SessionID})
	if err != nil {
		t.Fatalf("loading the fork failed: %v", err)
	}
	if len(forked) != 3 {
		t.Fatalf("expected 2 transcript entries plus a title, got %d", len(forked))
	}
	for _, entry := range forked {
		if entry["sessionId"] != result.SessionID {
			t.Errorf("expected the fork session ID on every entry, got %v", entry["sessionId"])
		}
	}

	// The source is untouched.
	source, _ := store.Load(ctx, key)
	if len(source) != 2 {
		t.Errorf("expected the source session to be unchanged, got %d entries", len(source))
	}
}

func TestForkSessionViaStoreNotFound(t *testing.T) {
	_, err := ForkSessionViaStore(context.Background(), NewInMemorySessionStore(), testSessionID, ForkSessionOptions{})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestImportSessionToStore(t *testing.T) {
	ctx := context.Background()
	root := withProjectsDir(t)
	projectDir := writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "first"),
		assistantEntry(testUUIDB, testUUIDA, "reply"),
	})

	// A subagent transcript with a metadata sidecar.
	subagentsDir := filepath.Join(projectDir, testSessionID, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("failed to create subagents dir: %v", err)
	}
	agentPath := filepath.Join(subagentsDir, "agent-abc.jsonl")
	agentLine, _ := json.Marshal(userEntry(testUUIDC, "", "sub prompt"))
	if err := os.WriteFile(agentPath, append(agentLine, '\n'), 0o600); err != nil {
		t.Fatalf("failed to write the subagent transcript: %v", err)
	}
	meta, _ := json.Marshal(map[string]any{"toolUseId": "toolu_9"})
	if err := os.WriteFile(agentMetadataSidecarPath(agentPath), meta, 0o600); err != nil {
		t.Fatalf("failed to write the metadata sidecar: %v", err)
	}

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(ctx, testSessionID, store, ImportSessionOptions{}); err != nil {
		t.Fatalf("ImportSessionToStore failed: %v", err)
	}

	// The destination key is the on-disk project directory name, so an
	// imported session is indistinguishable from a live-mirrored one.
	mainKey := SessionKey{ProjectKey: "proj", SessionID: testSessionID}
	entries, _ := store.Load(ctx, mainKey)
	if len(entries) != 2 {
		t.Fatalf("expected 2 main entries, got %d", len(entries))
	}

	subKey := SessionKey{ProjectKey: "proj", SessionID: testSessionID, Subpath: "subagents/agent-abc"}
	subEntries, _ := store.Load(ctx, subKey)
	if len(subEntries) != 2 {
		t.Fatalf("expected a transcript line plus the metadata entry, got %d", len(subEntries))
	}

	metaEntry, transcript := splitAgentMetadata(subEntries)
	if metaEntry == nil {
		t.Fatal("expected the sidecar to be imported as an agent_metadata entry")
	}
	if metaEntry["toolUseId"] != "toolu_9" {
		t.Errorf("unexpected metadata: %+v", metaEntry)
	}
	if len(transcript) != 1 {
		t.Errorf("expected 1 transcript line, got %d", len(transcript))
	}
}

func TestImportSessionToStoreExcludeSubagents(t *testing.T) {
	ctx := context.Background()
	root := withProjectsDir(t)
	projectDir := writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "prompt"),
	})

	subagentsDir := filepath.Join(projectDir, testSessionID, "subagents")
	_ = os.MkdirAll(subagentsDir, 0o755)
	_ = os.WriteFile(filepath.Join(subagentsDir, "agent-abc.jsonl"), []byte("{\"type\":\"user\"}\n"), 0o600)

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(ctx, testSessionID, store, ImportSessionOptions{ExcludeSubagents: true}); err != nil {
		t.Fatalf("ImportSessionToStore failed: %v", err)
	}

	subkeys, _ := store.ListSubkeys(ctx, SessionListSubkeysKey{ProjectKey: "proj", SessionID: testSessionID})
	if len(subkeys) != 0 {
		t.Errorf("expected subagents to be skipped, got %v", subkeys)
	}
}

func TestImportSessionToStoreNotFound(t *testing.T) {
	withProjectsDir(t)
	err := ImportSessionToStore(context.Background(), testSessionID, NewInMemorySessionStore(), ImportSessionOptions{})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSanitizeUnicodeNFKCFolding(t *testing.T) {
	// NFKC folds compatibility characters onto their canonical forms, so a
	// tag cannot smuggle a visual lookalike past a filter comparing plain
	// strings.
	for _, tc := range []struct {
		name, in, want string
	}{
		{"ligature", "\ufb01le", "file"},           // U+FB01 LATIN SMALL LIGATURE FI
		{"fullwidth", "\uff34\uff41\uff47", "Tag"}, // fullwidth T, a, g
		{"circled digit", "v\u2460", "v1"},         // U+2460 CIRCLED DIGIT ONE
		{"roman numeral", "\u2168", "IX"},          // U+2168 ROMAN NUMERAL NINE
		{"superscript", "x\u00b2", "x2"},           // U+00B2 SUPERSCRIPT TWO
		{"nbsp folds to space", "a\u00a0b", "a b"}, // U+00A0 NO-BREAK SPACE
	} {
		if got := sanitizeUnicode(tc.in); got != tc.want {
			t.Errorf("%s: sanitizeUnicode(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestSanitizeUnicodeStripsUnassigned(t *testing.T) {
	// U+0378 is unassigned (category Cn). The standard library has no Cn
	// table, so this exercises the "in no assigned category" fallback.
	if got := sanitizeUnicode("tag\u0378name"); got != "tagname" {
		t.Errorf("expected an unassigned code point to be stripped, got %q", got)
	}
	// U+E000 is private use (Co) and U+FEFF is a format character (Cf).
	if got := sanitizeUnicode("a\ue000b\ufeffc"); got != "abc" {
		t.Errorf("expected private-use and format characters to be stripped, got %q", got)
	}
}

func TestSanitizeUnicodeIsIdempotent(t *testing.T) {
	// The loop runs to a fixed point, so a second pass must be a no-op.
	for _, in := range []string{
		"plain",
		"o\ufb01ce",
		"\uff34\uff41\uff47",
		"a\u200b\u202e\ufeff\ue000\u0378b",
		"\u2168\u00b2\u2460",
	} {
		once := sanitizeUnicode(in)
		if twice := sanitizeUnicode(once); twice != once {
			t.Errorf("sanitizeUnicode is not idempotent for %q: %q then %q", in, once, twice)
		}
	}
}

func TestTagSessionFoldsCompatibilityForms(t *testing.T) {
	root := withProjectsDir(t)
	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "prompt"),
	})

	// A fullwidth tag is stored in its folded form, so it matches a plain
	// ASCII filter.
	if err := TagSession(testSessionID, "\uff32\uff45\uff56\uff49\uff45\uff57", ""); err != nil {
		t.Fatalf("TagSession failed: %v", err)
	}
	if info := GetSessionInfo(testSessionID, ""); info.Tag != "Review" {
		t.Errorf("expected the fullwidth tag to fold to ASCII, got %q", info.Tag)
	}
}
