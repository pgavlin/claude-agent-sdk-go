package agentsdk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests drive the real Claude Code CLI and assert that the session
// helpers understand the transcripts it actually writes. Unit tests can only
// check the helpers against fixtures the SDK itself produced, so this is the
// only place the on-disk format is validated against its real producer.
//
// Each test points CLAUDE_CONFIG_DIR at a temporary directory, so the CLI
// writes an isolated transcript and the mutation helpers never touch the
// caller's own sessions.

// realCLISession runs one trivial query against the real CLI in an isolated
// config directory, returning the config dir, the working directory, and the
// session ID the CLI reported.
func realCLISession(t *testing.T) (configDir, workDir, sessionID string) {
	t.Helper()
	skipIfNoRealCLI(t)

	configDir = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	// A distinct working directory keeps the project key clear of any other
	// sessions on this machine.
	workDir = t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &ClaudeAgentOptions{
		CWD:            workDir,
		PermissionMode: PermissionModePlan,
		SystemPrompt:   "Answer with a single short word.",
	}

	messages, errFn := Query(ctx, "Reply with the word: banana", opts)
	for msg := range messages {
		if result, ok := msg.(*ResultMessage); ok {
			sessionID = result.SessionID
		}
	}
	if err := errFn(); err != nil {
		t.Fatalf("real CLI query failed: %v", err)
	}
	if sessionID == "" {
		t.Fatal("the CLI reported no session ID")
	}
	return configDir, workDir, sessionID
}

func TestSessions_RealCLI_ReadBackWhatTheCLIWrote(t *testing.T) {
	configDir, workDir, sessionID := realCLISession(t)

	transcript := filepath.Join(configDir, "projects", sanitizePath(canonicalizePath(workDir)), sessionID+".jsonl")
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("expected the CLI to write %s: %v", transcript, err)
	}

	// The project key the SDK derives must match the directory the CLI
	// actually chose, or every session helper looks in the wrong place.
	if got := ProjectKeyForDirectory(workDir); got != filepath.Base(filepath.Dir(transcript)) {
		t.Errorf("ProjectKeyForDirectory = %q, but the CLI wrote to %q",
			got, filepath.Base(filepath.Dir(transcript)))
	}

	sessions := ListSessions(ListSessionsOptions{Directory: workDir})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	got := sessions[0]
	if got.SessionID != sessionID {
		t.Errorf("ListSessions returned %q, want %q", got.SessionID, sessionID)
	}
	if got.Summary == "" {
		t.Error("expected a non-empty summary derived from the real transcript")
	}
	if !strings.Contains(got.FirstPrompt, "banana") {
		t.Errorf("expected the first prompt to come from the real transcript, got %q", got.FirstPrompt)
	}
	if got.CreatedAt == 0 {
		t.Error("expected created_at to parse from a real ISO timestamp")
	}
	if got.FileSize == nil || *got.FileSize == 0 {
		t.Error("expected a non-zero file size")
	}
	if got.CWD == "" {
		t.Error("expected the cwd to be extracted from the transcript")
	}

	// GetSessionInfo takes the single-file path rather than the directory
	// scan, so the two must agree.
	info := GetSessionInfo(sessionID, workDir)
	if info == nil {
		t.Fatal("GetSessionInfo did not find the session")
	}
	if info.Summary != got.Summary || info.SessionID != got.SessionID {
		t.Errorf("GetSessionInfo disagrees with ListSessions:\n %+v\n %+v", info, got)
	}

	// The conversation chain is rebuilt from the CLI's own parentUuid links.
	messages := GetSessionMessages(sessionID, workDir, 0, 0)
	if len(messages) < 2 {
		t.Fatalf("expected at least a prompt and a reply, got %d messages", len(messages))
	}
	if messages[0].Type != "user" {
		t.Errorf("expected the chain to start with the user turn, got %q", messages[0].Type)
	}
	var sawAssistant bool
	for _, msg := range messages {
		if msg.Type == "assistant" {
			sawAssistant = true
		}
		if msg.UUID == "" || msg.SessionID == "" {
			t.Errorf("expected identifiers on every message, got %+v", msg)
		}
		if msg.Message == nil {
			t.Errorf("expected a raw message payload on %+v", msg)
		}
	}
	if !sawAssistant {
		t.Error("expected an assistant message in the chain")
	}
}

func TestSessions_RealCLI_Mutations(t *testing.T) {
	_, workDir, sessionID := realCLISession(t)

	// Rename and tag append entries the lite parse must read back. This is
	// the byte-shape check that unit fixtures cannot make: the entries have
	// to be written the way the CLI's own reader expects.
	if err := RenameSession(sessionID, "Renamed by the SDK", workDir); err != nil {
		t.Fatalf("RenameSession failed: %v", err)
	}
	if err := TagSession(sessionID, "sdk-tag", workDir); err != nil {
		t.Fatalf("TagSession failed: %v", err)
	}

	info := GetSessionInfo(sessionID, workDir)
	if info == nil {
		t.Fatal("the session disappeared after mutation")
	}
	if info.CustomTitle != "Renamed by the SDK" {
		t.Errorf("expected the appended title to be read back, got %q", info.CustomTitle)
	}
	if info.Summary != "Renamed by the SDK" {
		t.Errorf("expected the title to win as the summary, got %q", info.Summary)
	}
	if info.Tag != "sdk-tag" {
		t.Errorf("expected the appended tag to be read back, got %q", info.Tag)
	}

	// Clearing the tag must not disturb the title.
	if err := TagSession(sessionID, "", workDir); err != nil {
		t.Fatalf("clearing the tag failed: %v", err)
	}
	if info := GetSessionInfo(sessionID, workDir); info.Tag != "" {
		t.Errorf("expected the tag to be cleared, got %q", info.Tag)
	}

	// Forking a real transcript exercises the UUID remap over entry shapes
	// the SDK never authored.
	fork, err := ForkSession(sessionID, ForkSessionOptions{Directory: workDir})
	if err != nil {
		t.Fatalf("ForkSession failed: %v", err)
	}

	original := GetSessionMessages(sessionID, workDir, 0, 0)
	forked := GetSessionMessages(fork.SessionID, workDir, 0, 0)
	if len(forked) != len(original) {
		t.Errorf("expected the fork to preserve %d messages, got %d", len(original), len(forked))
	}
	for i := range forked {
		if i < len(original) && forked[i].UUID == original[i].UUID {
			t.Errorf("expected message %d to be remapped to a fresh UUID", i)
		}
		if forked[i].SessionID != fork.SessionID {
			t.Errorf("expected fork entries to carry the new session ID, got %q", forked[i].SessionID)
		}
	}

	forkInfo := GetSessionInfo(fork.SessionID, workDir)
	if forkInfo == nil {
		t.Fatal("the fork is not listable")
	}
	if !strings.HasSuffix(forkInfo.CustomTitle, " (fork)") {
		t.Errorf("expected a derived fork title, got %q", forkInfo.CustomTitle)
	}

	// Deleting removes only the fork; the source session survives.
	if err := DeleteSession(fork.SessionID, workDir); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	if GetSessionInfo(fork.SessionID, workDir) != nil {
		t.Error("expected the fork to be deleted")
	}
	if GetSessionInfo(sessionID, workDir) == nil {
		t.Error("expected the source session to survive deleting the fork")
	}
}

func TestSessionStore_RealCLI_Mirror(t *testing.T) {
	skipIfNoRealCLI(t)

	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	workDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	store := NewInMemorySessionStore()
	opts := &ClaudeAgentOptions{
		CWD:            workDir,
		PermissionMode: PermissionModePlan,
		SystemPrompt:   "Answer with a single short word.",
		SessionStore:   store,
	}

	var sessionID string
	messages, errFn := Query(ctx, "Reply with the word: cherry", opts)
	for msg := range messages {
		switch m := msg.(type) {
		case *MirrorErrorMessage:
			t.Errorf("mirror error from the real CLI: %s", m.Error)
		case *ResultMessage:
			sessionID = m.SessionID
		}
	}
	if err := errFn(); err != nil {
		t.Fatalf("real CLI query failed: %v", err)
	}
	if sessionID == "" {
		t.Fatal("the CLI reported no session ID")
	}

	// --session-mirror makes the CLI emit transcript_mirror frames; the
	// batcher must have turned those into store appends under the same
	// project key the read helpers derive.
	if store.Size() == 0 {
		t.Fatal("expected the CLI's transcript-mirror frames to reach the store")
	}

	stored, err := GetSessionMessagesFromStore(ctx, store, sessionID, workDir, 0, 0)
	if err != nil {
		t.Fatalf("GetSessionMessagesFromStore failed: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("expected mirrored messages to be readable back from the store")
	}

	// The mirrored copy and the local transcript describe the same
	// conversation.
	local := GetSessionMessages(sessionID, workDir, 0, 0)
	if len(local) != len(stored) {
		t.Errorf("local transcript has %d messages but the mirror has %d", len(local), len(stored))
	}

	listed, err := ListSessionsFromStore(ctx, store, workDir, 0, 0)
	if err != nil {
		t.Fatalf("ListSessionsFromStore failed: %v", err)
	}
	if len(listed) != 1 || listed[0].SessionID != sessionID {
		t.Errorf("expected the mirrored session to be listable, got %+v", listed)
	}
}

func TestImportSessionToStore_RealCLI(t *testing.T) {
	ctx := context.Background()
	_, workDir, sessionID := realCLISession(t)

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(ctx, sessionID, store, ImportSessionOptions{Directory: workDir}); err != nil {
		t.Fatalf("ImportSessionToStore failed: %v", err)
	}

	imported, err := GetSessionMessagesFromStore(ctx, store, sessionID, workDir, 0, 0)
	if err != nil {
		t.Fatalf("GetSessionMessagesFromStore failed: %v", err)
	}
	local := GetSessionMessages(sessionID, workDir, 0, 0)

	if len(imported) != len(local) {
		t.Errorf("expected the import to round-trip %d messages, got %d", len(local), len(imported))
	}
	for i := range imported {
		if i < len(local) && imported[i].UUID != local[i].UUID {
			t.Errorf("message %d differs after import: %q vs %q", i, imported[i].UUID, local[i].UUID)
		}
	}
}

// skipIfNoRealCLI skips a test unless real-CLI testing is enabled.
func skipIfNoRealCLI(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTSDK_TEST_REAL_CLI") == "" {
		t.Skip("set AGENTSDK_TEST_REAL_CLI=1 to run real CLI tests")
	}
}
