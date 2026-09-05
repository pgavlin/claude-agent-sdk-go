package agentsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withProjectsDir points CLAUDE_CONFIG_DIR at a temporary directory and
// returns the projects root inside it.
func withProjectsDir(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	root := filepath.Join(configDir, "projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("failed to create projects dir: %v", err)
	}
	return root
}

// writeTranscript writes JSONL lines for a session under a project
// directory, returning the project directory path.
func writeTranscript(t *testing.T, root, projectKey, sessionID string, entries []map[string]any) string {
	t.Helper()
	projectDir := filepath.Join(root, projectKey)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	var b strings.Builder
	for _, entry := range entries {
		// Encode with "type" first, matching the byte shape the CLI writes
		// on disk: the lite parse scans for a {"type":"tag" line prefix.
		encoded, err := marshalTypeFirst(SessionStoreEntry(entry))
		if err != nil {
			t.Fatalf("failed to encode entry: %v", err)
		}
		b.Write(encoded)
		b.WriteByte('\n')
	}

	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}
	return projectDir
}

func userEntry(uuid, parent, text string) map[string]any {
	entry := map[string]any{
		"type":      "user",
		"uuid":      uuid,
		"sessionId": testSessionID,
		"timestamp": "2026-01-01T00:00:00.000Z",
		"message":   map[string]any{"role": "user", "content": text},
	}
	if parent != "" {
		entry["parentUuid"] = parent
	}
	return entry
}

func assistantEntry(uuid, parent, text string) map[string]any {
	return map[string]any{
		"type":       "assistant",
		"uuid":       uuid,
		"parentUuid": parent,
		"sessionId":  testSessionID,
		"timestamp":  "2026-01-01T00:00:01.000Z",
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
}

const (
	testSessionID = "550e8400-e29b-41d4-a716-446655440000"
	testUUIDA     = "11111111-1111-4111-8111-111111111111"
	testUUIDB     = "22222222-2222-4222-8222-222222222222"
	testUUIDC     = "33333333-3333-4333-8333-333333333333"
)

func TestValidateUUID(t *testing.T) {
	if got := validateUUID(testSessionID); got != testSessionID {
		t.Errorf("expected the UUID to validate, got %q", got)
	}
	for _, bad := range []string{"", "not-a-uuid", "550e8400e29b41d4a716446655440000", "../etc/passwd"} {
		if got := validateUUID(bad); got != "" {
			t.Errorf("expected %q to be rejected, got %q", bad, got)
		}
	}
}

func TestSanitizePath(t *testing.T) {
	if got := sanitizePath("/home/user/my project"); got != "-home-user-my-project" {
		t.Errorf("unexpected sanitized path: %q", got)
	}

	// A path over the limit is truncated and suffixed with a hash, so the
	// component stays within filesystem limits.
	long := "/" + strings.Repeat("a", 300)
	got := sanitizePath(long)
	if len(got) <= maxSanitizedLength {
		t.Errorf("expected a truncated path longer than the limit, got %d chars", len(got))
	}
	if !strings.HasPrefix(got, strings.Repeat("-", 1)+strings.Repeat("a", 199)) {
		t.Errorf("expected the sanitized prefix to be preserved, got %q", got[:40])
	}
	if !strings.Contains(got[maxSanitizedLength:], "-") {
		t.Error("expected a hash suffix after the truncation point")
	}
}

func TestSimpleHashMatchesJavaScript(t *testing.T) {
	// Values produced by the CLI's JS implementation:
	//   h = (h << 5) - h + c; h |= 0; Math.abs(h).toString(36)
	for _, tc := range []struct{ in, want string }{
		{"a", "2p"},
		{"abc", "22ci"},
		{"", "0"},
	} {
		if got := simpleHash(tc.in); got != tc.want {
			t.Errorf("simpleHash(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtractJSONStringField(t *testing.T) {
	line := `{"type":"user","cwd":"/home/user","gitBranch":"main"}`
	if got := extractJSONStringField(line, "cwd"); got != "/home/user" {
		t.Errorf("expected the cwd value, got %q", got)
	}
	if got := extractJSONStringField(line, "missing"); got != "" {
		t.Errorf("expected an empty string for a missing key, got %q", got)
	}

	// Escaped quotes inside the value must not terminate the scan.
	escaped := `{"summary":"say \"hi\" now","x":1}`
	if got := extractJSONStringField(escaped, "summary"); got != `say "hi" now` {
		t.Errorf("expected the unescaped value, got %q", got)
	}
}

func TestExtractLastJSONStringField(t *testing.T) {
	text := `{"customTitle":"first"}` + "\n" + `{"customTitle":"second"}`
	if got := extractLastJSONStringField(text, "customTitle"); got != "second" {
		t.Errorf("expected the last occurrence, got %q", got)
	}
}

func TestExtractFirstPromptFromHead(t *testing.T) {
	head := strings.Join([]string{
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"x"}]}}`,
		`{"type":"user","message":{"role":"user","content":"<tick>ignored"}}`,
		`{"type":"user","message":{"role":"user","content":"real prompt"}}`,
	}, "\n")

	if got := extractFirstPromptFromHead(head); got != "real prompt" {
		t.Errorf("expected the first meaningful prompt, got %q", got)
	}
}

func TestExtractFirstPromptCommandFallback(t *testing.T) {
	head := `{"type":"user","message":{"role":"user","content":"<command-name>deploy</command-name>"}}`
	if got := extractFirstPromptFromHead(head); got != "deploy" {
		t.Errorf("expected the slash-command fallback, got %q", got)
	}
}

func TestListSessionsAndGetSessionInfo(t *testing.T) {
	root := withProjectsDir(t)
	projectKey := sanitizePath("/work/project")
	writeTranscript(t, root, projectKey, testSessionID, []map[string]any{
		{
			"type": "user", "uuid": testUUIDA, "cwd": "/work/project",
			"timestamp": "2026-01-01T00:00:00.000Z", "gitBranch": "main",
			"message": map[string]any{"role": "user", "content": "hello world"},
		},
		{"type": "custom-title", "customTitle": "My session"},
		{"type": "tag", "tag": "experiment"},
	})

	sessions := ListSessions(ListSessionsOptions{})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	got := sessions[0]
	if got.SessionID != testSessionID {
		t.Errorf("unexpected session ID: %q", got.SessionID)
	}
	if got.Summary != "My session" {
		t.Errorf("expected the custom title as the summary, got %q", got.Summary)
	}
	if got.CustomTitle != "My session" {
		t.Errorf("unexpected custom title: %q", got.CustomTitle)
	}
	if got.FirstPrompt != "hello world" {
		t.Errorf("unexpected first prompt: %q", got.FirstPrompt)
	}
	if got.GitBranch != "main" {
		t.Errorf("unexpected git branch: %q", got.GitBranch)
	}
	if got.Tag != "experiment" {
		t.Errorf("unexpected tag: %q", got.Tag)
	}
	if got.CWD != "/work/project" {
		t.Errorf("unexpected cwd: %q", got.CWD)
	}
	if got.CreatedAt == 0 {
		t.Error("expected a created-at timestamp")
	}
	if got.FileSize == nil || *got.FileSize == 0 {
		t.Error("expected a non-zero file size for a local transcript")
	}

	info := GetSessionInfo(testSessionID, "")
	if info == nil {
		t.Fatal("expected GetSessionInfo to find the session")
	}
	if info.Summary != got.Summary {
		t.Errorf("GetSessionInfo disagrees with ListSessions: %q vs %q", info.Summary, got.Summary)
	}

	if GetSessionInfo("not-a-uuid", "") != nil {
		t.Error("expected nil for an invalid session ID")
	}
}

func TestListSessionsSkipsSidechain(t *testing.T) {
	root := withProjectsDir(t)
	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		{
			"type": "user", "uuid": testUUIDA, "isSidechain": true,
			"message": map[string]any{"role": "user", "content": "sub-agent work"},
		},
	})

	if sessions := ListSessions(ListSessionsOptions{}); len(sessions) != 0 {
		t.Errorf("expected sidechain sessions to be skipped, got %d", len(sessions))
	}
}

func TestListSessionsPagination(t *testing.T) {
	root := withProjectsDir(t)
	ids := []string{
		"aaaaaaaa-1111-4111-8111-111111111111",
		"bbbbbbbb-2222-4222-8222-222222222222",
		"cccccccc-3333-4333-8333-333333333333",
	}
	for _, id := range ids {
		writeTranscript(t, root, "proj", id, []map[string]any{
			{"type": "user", "uuid": testUUIDA, "message": map[string]any{"role": "user", "content": "prompt " + id}},
		})
	}

	all := ListSessions(ListSessionsOptions{})
	if len(all) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(all))
	}
	if page := ListSessions(ListSessionsOptions{Limit: 2}); len(page) != 2 {
		t.Errorf("expected a page of 2, got %d", len(page))
	}
	if page := ListSessions(ListSessionsOptions{Offset: 2}); len(page) != 1 {
		t.Errorf("expected 1 session after an offset of 2, got %d", len(page))
	}
	if page := ListSessions(ListSessionsOptions{Offset: 99}); len(page) != 0 {
		t.Errorf("expected no sessions past the end, got %d", len(page))
	}
}

func TestGetSessionMessagesBuildsChain(t *testing.T) {
	root := withProjectsDir(t)
	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "first"),
		assistantEntry(testUUIDB, testUUIDA, "reply"),
		userEntry(testUUIDC, testUUIDB, "second"),
	})

	messages := GetSessionMessages(testSessionID, "", 0, 0)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if messages[0].Type != "user" || messages[1].Type != "assistant" || messages[2].Type != "user" {
		t.Errorf("unexpected message order: %v", []string{messages[0].Type, messages[1].Type, messages[2].Type})
	}
	if messages[0].UUID != testUUIDA {
		t.Errorf("expected the chain root first, got %q", messages[0].UUID)
	}

	if page := GetSessionMessages(testSessionID, "", 1, 1); len(page) != 1 || page[0].UUID != testUUIDB {
		t.Errorf("unexpected paginated result: %+v", page)
	}
	if msgs := GetSessionMessages("not-a-uuid", "", 0, 0); msgs != nil {
		t.Error("expected nil for an invalid session ID")
	}
}

func TestGetSessionMessagesSkipsMetaAndSidechain(t *testing.T) {
	root := withProjectsDir(t)
	meta := userEntry(testUUIDB, testUUIDA, "meta")
	meta["isMeta"] = true

	writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "visible"),
		meta,
		assistantEntry(testUUIDC, testUUIDB, "reply"),
	})

	messages := GetSessionMessages(testSessionID, "", 0, 0)
	for _, msg := range messages {
		if msg.UUID == testUUIDB {
			t.Error("expected isMeta entries to be filtered out of the results")
		}
	}
}

func TestListSubagentsAndMessages(t *testing.T) {
	root := withProjectsDir(t)
	projectDir := writeTranscript(t, root, "proj", testSessionID, []map[string]any{
		userEntry(testUUIDA, "", "main"),
	})

	subagentsDir := filepath.Join(projectDir, testSessionID, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("failed to create subagents dir: %v", err)
	}

	agentPath := filepath.Join(subagentsDir, "agent-abc.jsonl")
	lines := []map[string]any{
		userEntry(testUUIDB, "", "sub prompt"),
		assistantEntry(testUUIDC, testUUIDB, "sub reply"),
	}
	var b strings.Builder
	for _, l := range lines {
		encoded, _ := marshalTypeFirst(SessionStoreEntry(l))
		b.Write(encoded)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(agentPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("failed to write subagent transcript: %v", err)
	}

	meta, _ := json.Marshal(map[string]any{"toolUseId": "toolu_123", "parentAgentId": "agent-parent"})
	if err := os.WriteFile(agentMetadataSidecarPath(agentPath), meta, 0o600); err != nil {
		t.Fatalf("failed to write metadata sidecar: %v", err)
	}

	ids := ListSubagents(testSessionID, "")
	if len(ids) != 1 || ids[0] != "abc" {
		t.Fatalf("expected the subagent ID abc, got %v", ids)
	}

	messages := GetSubagentMessages(testSessionID, "abc", "", 0, 0)
	if len(messages) != 2 {
		t.Fatalf("expected 2 subagent messages, got %d", len(messages))
	}
	for _, msg := range messages {
		if msg.ParentToolUseID != "toolu_123" {
			t.Errorf("expected the parent tool use ID from the sidecar, got %q", msg.ParentToolUseID)
		}
		if msg.ParentAgentID != "agent-parent" {
			t.Errorf("expected the parent agent ID from the sidecar, got %q", msg.ParentAgentID)
		}
	}
}

func TestProjectKeyForDirectory(t *testing.T) {
	dir := t.TempDir()
	key := ProjectKeyForDirectory(dir)
	if key == "" {
		t.Fatal("expected a non-empty project key")
	}
	if strings.ContainsAny(key, "/\\") {
		t.Errorf("expected a single path component, got %q", key)
	}
	if key != ProjectKeyForDirectory(dir) {
		t.Error("expected the project key to be stable across calls")
	}
}

func TestNormalizePathNFC(t *testing.T) {
	// The same text in composed and decomposed form must normalize to one
	// representation. macOS HFS+ stores decomposed, so without this the SDK
	// and the CLI would disagree about a path containing non-ASCII text.
	// Written as escapes so the fixtures cannot be silently composed
	// by an editor, which would make this test vacuous.
	composed := "/tmp/caf\u00e9"    // precomposed U+00E9
	decomposed := "/tmp/cafe\u0301" // e + U+0301 combining acute

	if composed == decomposed {
		t.Fatal("test inputs are already identical; the fixture is wrong")
	}
	if normalizePath(composed) != normalizePath(decomposed) {
		t.Errorf("expected both forms to normalize alike:\n composed:   %q\n decomposed: %q",
			normalizePath(composed), normalizePath(decomposed))
	}
	if normalizePath(composed) != composed {
		t.Errorf("expected the composed form to be the NFC representative, got %q", normalizePath(composed))
	}
}

func TestProjectKeyIsNormalizationInsensitive(t *testing.T) {
	// This is the bug NFC prevents: sanitizePath maps each non-alphanumeric
	// rune to a hyphen, so a decomposed path yields an extra hyphen and a
	// different project key than its composed twin.
	composed := ProjectKeyForDirectory("/tmp/caf\u00e9/project")
	decomposed := ProjectKeyForDirectory("/tmp/cafe\u0301/project")

	// Without normalization these differ: sanitizePath maps the combining
	// accent to its own hyphen, yielding "-tmp-cafe--project" against
	// "-tmp-caf--project".
	if composed != decomposed {
		t.Errorf("expected one project key for both normalization forms, got %q and %q",
			composed, decomposed)
	}
}

func TestCanonicalizePathNormalizesUnresolvablePaths(t *testing.T) {
	// A path that cannot be resolved still has to yield a stable key, so
	// the fallback branches normalize too.
	missing := filepath.Join(t.TempDir(), "does-not-exist-caf\u00e9")
	missingDecomposed := filepath.Join(filepath.Dir(missing), "does-not-exist-cafe\u0301")

	if canonicalizePath(missing) != canonicalizePath(missingDecomposed) {
		t.Errorf("expected the unresolvable-path fallback to normalize, got %q and %q",
			canonicalizePath(missing), canonicalizePath(missingDecomposed))
	}
}
