// Materializing a SessionStore-backed resume into a temporary
// CLAUDE_CONFIG_DIR.
//
// When Resume or ContinueConversation is paired with a SessionStore, the
// session JSONL almost certainly does not exist on local disk: it lives in
// the external store. The CLI subprocess only knows how to resume from a
// local file. This bridges the gap by loading the session from the store,
// writing it to a temporary directory laid out exactly like ~/.claude/, and
// returning the path so the caller can point the subprocess at it via
// CLAUDE_CONFIG_DIR.

package agentsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// keychainServiceName is the default macOS Keychain service holding OAuth
// credentials when CLAUDE_CONFIG_DIR is unset.
const keychainServiceName = "Claude Code-credentials"

// resumeStrippedSettingsKeys are user-settings keys that only misbehave
// under a redirected CLAUDE_CONFIG_DIR: plugin declarations reconcile
// against the always-empty temporary plugin cache, and would network-install
// each declared marketplace on every resume.
var resumeStrippedSettingsKeys = []string{"enabledPlugins", "extraKnownMarketplaces"}

// MaterializedResume is the result of MaterializeResumeSession.
type MaterializedResume struct {
	// ConfigDir is a temporary directory laid out like ~/.claude/. Point
	// the subprocess at it via CLAUDE_CONFIG_DIR.
	ConfigDir string

	// ResumeSessionID is the session ID to pass as --resume. When the input
	// was ContinueConversation, it is the most recent session resolved via
	// SessionLister.
	ResumeSessionID string
}

// Cleanup removes the temporary config directory. Call it after the
// subprocess exits. It is best-effort and safe to call more than once.
func (m *MaterializedResume) Cleanup() {
	if m == nil || m.ConfigDir == "" {
		return
	}
	removeAllWithRetry(m.ConfigDir)
}

// ApplyMaterializedOptions returns a copy of options repointed at a
// materialized temporary config directory.
//
// It sets CLAUDE_CONFIG_DIR in the environment and Resume to the
// materialized session ID, and clears ContinueConversation, which
// materialization has already resolved to a concrete session ID.
func ApplyMaterializedOptions(options *ClaudeAgentOptions, materialized *MaterializedResume) *ClaudeAgentOptions {
	if options == nil || materialized == nil {
		return options
	}

	updated := *options
	updated.Env = make(map[string]string, len(options.Env)+1)
	for k, v := range options.Env {
		updated.Env[k] = v
	}
	updated.Env["CLAUDE_CONFIG_DIR"] = materialized.ConfigDir
	updated.Resume = materialized.ResumeSessionID
	updated.ContinueConversation = false
	return &updated
}

// MaterializeResumeSession loads a session from options.SessionStore and
// writes it to a temporary directory laid out like ~/.claude/.
//
// It returns nil when no materialization is needed — no store, no resume or
// continue, a store with no entries, or a resolved session ID that is not a
// valid UUID — and the caller then falls through to the normal resume and
// spawn path. For ContinueConversation that means a fresh session; for an
// explicit Resume the CLI receives the value unchanged.
//
// It returns an error when a store call fails or times out.
func MaterializeResumeSession(ctx context.Context, options *ClaudeAgentOptions) (*MaterializedResume, error) {
	if options == nil || options.SessionStore == nil {
		return nil, nil
	}
	if options.Resume == "" && !options.ContinueConversation {
		return nil, nil
	}

	store := options.SessionStore
	timeout := time.Duration(options.loadTimeoutMS()) * time.Millisecond
	projectKey := ProjectKeyForDirectory(options.CWD)

	// Resolve the session ID: an explicit resume wins, and otherwise the
	// most recently modified non-sidechain session from the store. An empty
	// listing means a fresh session, matching the CLI's --continue with no
	// history.
	var sessionID string
	var entries []SessionStoreEntry
	var err error

	if options.Resume != "" {
		// The session ID becomes a path component below, so reject anything
		// that is not a UUID, to prevent traversal and to match every other
		// resume path.
		if validateUUID(options.Resume) == "" {
			return nil, nil
		}
		sessionID, entries, err = loadResumeCandidate(ctx, store, projectKey, options.Resume, timeout)
	} else {
		sessionID, entries, err = resolveContinueCandidate(ctx, store, projectKey, timeout)
	}
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, nil
	}

	tmpBase, err := os.MkdirTemp("", "claude-resume-")
	if err != nil {
		return nil, err
	}

	materialized, err := func() (*MaterializedResume, error) {
		projectDir := filepath.Join(tmpBase, "projects", projectKey)
		if err := os.MkdirAll(projectDir, 0o700); err != nil {
			return nil, err
		}
		if err := writeJSONL(filepath.Join(projectDir, sessionID+".jsonl"), entries); err != nil {
			return nil, err
		}

		// The subprocess runs with CLAUDE_CONFIG_DIR set to tmpBase, so copy
		// auth config from the caller's effective config locations to let it
		// authenticate. Missing files are fine, as with API-key auth.
		copyAuthFiles(tmpBase, options.Env)

		// Materialize subagent transcripts when the store can enumerate them.
		if subkeyLister, ok := store.(SessionSubkeyLister); ok {
			if err := materializeSubkeys(
				ctx, store, subkeyLister, projectDir, projectKey, sessionID, timeout); err != nil {
				return nil, err
			}
		}

		return &MaterializedResume{ConfigDir: tmpBase, ResumeSessionID: sessionID}, nil
	}()
	if err != nil {
		// Any failure after MkdirTemp would leave tmpBase — which may
		// already hold a credentials copy — on disk with no path for the
		// caller to clean it up.
		removeAllWithRetry(tmpBase)
		return nil, err
	}

	return materialized, nil
}

// loadResumeCandidate loads entries for a session, returning an empty
// session ID when the session is missing or empty.
func loadResumeCandidate(
	ctx context.Context,
	store SessionStore,
	projectKey, sessionID string,
	timeout time.Duration,
) (string, []SessionStoreEntry, error) {
	loadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	entries, err := store.Load(loadCtx, SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil {
		return "", nil, resumeStoreError(fmt.Sprintf("SessionStore.Load for session %s", sessionID), loadCtx, timeout, err)
	}
	if len(entries) == 0 {
		return "", nil, nil
	}
	return sessionID, entries, nil
}

// resolveContinueCandidate picks the most recently modified non-sidechain
// session.
//
// Sidechain transcripts are mirrored as ordinary top-level keys and often
// have the highest mtime, since their append lands after the main session's
// in the same flush. Walking newest to oldest and skipping sidechains makes
// continue resume the user's conversation rather than a subagent's, matching
// the CLI's own filter.
func resolveContinueCandidate(
	ctx context.Context,
	store SessionStore,
	projectKey string,
	timeout time.Duration,
) (string, []SessionStoreEntry, error) {
	lister, ok := store.(SessionLister)
	if !ok {
		return "", nil, NewValidationError(
			"ContinueConversation with SessionStore requires the store to implement SessionLister")
	}

	listCtx, cancel := context.WithTimeout(ctx, timeout)
	sessions, err := lister.ListSessions(listCtx, projectKey)
	cancel()
	if err != nil {
		return "", nil, resumeStoreError("SessionStore.ListSessions", listCtx, timeout, err)
	}
	if len(sessions) == 0 {
		return "", nil, nil
	}

	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Mtime > sessions[j].Mtime })

	for _, cand := range sessions {
		if validateUUID(cand.SessionID) == "" {
			continue
		}
		sid, entries, err := loadResumeCandidate(ctx, store, projectKey, cand.SessionID, timeout)
		if err != nil {
			return "", nil, err
		}
		if sid == "" {
			continue
		}
		if isSidechain, _ := entries[0]["isSidechain"].(bool); isSidechain {
			continue
		}
		return sid, entries, nil
	}
	return "", nil, nil
}

// resumeStoreError wraps an adapter failure with resume context, naming a
// timeout explicitly when the deadline expired.
func resumeStoreError(what string, ctx context.Context, timeout time.Duration, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out after %dms during resume materialization",
			what, timeout.Milliseconds())
	}
	return fmt.Errorf("%s failed during resume materialization: %w", what, err)
}

// writeJSONL writes entries as one JSON line each, with mode 0600.
func writeJSONL(path string, entries []SessionStoreEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, entry := range entries {
		// "type" first, matching the byte shape the CLI writes and the lite
		// parse scans for.
		encoded, err := marshalTypeFirst(entry)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(encoded, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// materializeSubkeys loads and writes every subagent transcript and metadata
// sidecar under a session.
func materializeSubkeys(
	ctx context.Context,
	store SessionStore,
	lister SessionSubkeyLister,
	projectDir, projectKey, sessionID string,
	timeout time.Duration,
) error {
	sessionDir := filepath.Join(projectDir, sessionID)

	listCtx, cancel := context.WithTimeout(ctx, timeout)
	subkeys, err := lister.ListSubkeys(listCtx, SessionListSubkeysKey{
		ProjectKey: projectKey,
		SessionID:  sessionID,
	})
	cancel()
	if err != nil {
		return resumeStoreError(
			fmt.Sprintf("SessionStore.ListSubkeys for session %s", sessionID), listCtx, timeout, err)
	}

	for _, subpath := range subkeys {
		// Subpaths come from an external store and become filesystem path
		// components below, so reject anything that would escape the
		// session directory.
		subFile, ok := safeSubpathTarget(subpath, sessionDir)
		if !ok {
			log.Printf("agentsdk: skipping unsafe subpath from ListSubkeys: %q", subpath)
			continue
		}

		loadCtx, cancel := context.WithTimeout(ctx, timeout)
		subEntries, err := store.Load(loadCtx, SessionKey{
			ProjectKey: projectKey,
			SessionID:  sessionID,
			Subpath:    subpath,
		})
		cancel()
		if err != nil {
			return resumeStoreError(
				fmt.Sprintf("SessionStore.Load for session %s subpath %s", sessionID, subpath),
				loadCtx, timeout, err)
		}
		if len(subEntries) == 0 {
			continue
		}

		// agent_metadata entries describe the .meta.json sidecar, last one
		// winning; everything else is a transcript line.
		metadata, transcript := splitAgentMetadata(subEntries)

		if len(transcript) > 0 {
			if err := writeJSONL(subFile, transcript); err != nil {
				return err
			}
		}

		if metadata != nil {
			// Strip the synthetic type field.
			metaContent := make(map[string]any, len(metadata))
			for k, v := range metadata {
				if k != "type" {
					metaContent[k] = v
				}
			}
			metaFile := agentMetadataSidecarPath(subFile)
			if err := os.MkdirAll(filepath.Dir(metaFile), 0o700); err != nil {
				return err
			}
			encoded, err := json.Marshal(metaContent)
			if err != nil {
				return err
			}
			if err := os.WriteFile(metaFile, encoded, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

// safeSubpathTarget resolves a store subpath to its .jsonl target under
// sessionDir, reporting false for a subpath that is empty, absolute,
// drive-prefixed, contains a "." or ".." component or a NUL byte, or escapes
// sessionDir after resolution.
func safeSubpathTarget(subpath, sessionDir string) (string, bool) {
	// An empty subpath is rejected explicitly: "" + ".jsonl" is ".jsonl", a
	// hidden dotfile that would pass a naive prefix check.
	if subpath == "" {
		return "", false
	}
	if strings.HasPrefix(subpath, "/") || strings.HasPrefix(subpath, `\`) || filepath.IsAbs(subpath) {
		return "", false
	}
	if strings.ContainsRune(subpath, 0) {
		return "", false
	}
	// Drive-prefixed subpaths such as "C:foo" are never legitimate store
	// keys. Check regardless of host OS so a Windows consumer is protected
	// even when the store was populated elsewhere.
	if len(subpath) >= 2 && subpath[1] == ':' {
		return "", false
	}
	// Subpaths are store keys that may use either separator regardless of
	// host OS.
	for _, part := range strings.FieldsFunc(subpath, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "." || part == ".." {
			return "", false
		}
	}

	target := filepath.Join(sessionDir, filepath.FromSlash(strings.ReplaceAll(subpath, `\`, "/")))
	subFile := target + ".jsonl"

	// Confirm the target stays under sessionDir. Resolve both sides so a
	// symlinked temporary directory does not produce a false mismatch,
	// falling back to the unresolved paths when the target does not exist
	// yet, which is the normal case here.
	base := sessionDir
	if resolved, err := filepath.EvalSymlinks(sessionDir); err == nil {
		base = resolved
	}
	rel, err := filepath.Rel(base, subFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return subFile, true
}

// removeAllWithRetry removes a directory tree, retrying transient lock
// errors.
//
// On Windows an antivirus scanner or indexer can briefly hold a handle on a
// freshly written file, notably .credentials.json, making removal fail. It
// retries a few times with a short backoff and then gives up silently, since
// this is best-effort cleanup of a temporary directory.
func removeAllWithRetry(path string) {
	const retries = 4
	delay := 100 * time.Millisecond

	for attempt := 0; attempt < retries; attempt++ {
		err := os.RemoveAll(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return
		}
		time.Sleep(delay)
		delay *= 2
	}
	_ = os.RemoveAll(path)
}

// copyAuthFiles seeds the temporary config directory with the caller's auth
// and user config: .credentials.json with the refresh token redacted,
// .claude.json, and the user settings.json and cowork_settings.json with
// plugin declarations stripped.
//
// Source resolution mirrors the CLI: .credentials.json, settings.json, and
// cowork_settings.json live under the config directory (by default
// ~/.claude/), while .claude.json lives at $CLAUDE_CONFIG_DIR/.claude.json
// when that is set and at ~/.claude.json otherwise — not under ~/.claude/.
func copyAuthFiles(tmpBase string, optEnv map[string]string) {
	callerConfigDir := optEnv["CLAUDE_CONFIG_DIR"]
	if callerConfigDir == "" {
		callerConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}

	sourceConfigDir := callerConfigDir
	if sourceConfigDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		sourceConfigDir = filepath.Join(home, ".claude")
	}

	credsBytes := readIfPresent(filepath.Join(sourceConfigDir, ".credentials.json"))

	// The default macOS setup keeps OAuth tokens in the Keychain rather than
	// a file. Redirecting CLAUDE_CONFIG_DIR changes the Keychain service
	// name suffix, so the subprocess's lookup misses and falls back to
	// plaintext storage at <tmpBase>/.credentials.json. Populate that file
	// from the parent's Keychain so the resumed subprocess can authenticate.
	// Skip it when env-based auth or a custom config directory is already in
	// play.
	if callerConfigDir == "" &&
		envOrProcess(optEnv, "ANTHROPIC_API_KEY") == "" &&
		envOrProcess(optEnv, "CLAUDE_CODE_OAUTH_TOKEN") == "" {
		if keychain := readKeychainCredentials(); keychain != nil {
			credsBytes = keychain
		}
	}

	writeRedactedCredentials(credsBytes, filepath.Join(tmpBase, ".credentials.json"))

	claudeJSONSrc := filepath.Join(sourceConfigDir, ".claude.json")
	if callerConfigDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			claudeJSONSrc = filepath.Join(home, ".claude.json")
		}
	}
	copyIfPresent(claudeJSONSrc, filepath.Join(tmpBase, ".claude.json"), nil)

	// User settings carry apiKeyHelper — a fourth auth mechanism alongside
	// .credentials.json, the Keychain, and the environment — plus env,
	// hooks, and permissions. Without it the resumed subprocess sees no user
	// settings at all, and an apiKeyHelper-only host fails to authenticate.
	// cowork_settings.json is the alternate filename the CLI reads in
	// cowork-plugins mode. Both pass through the strip so plugin
	// declarations do not reconcile against the empty temporary plugin
	// cache.
	for _, name := range []string{"settings.json", "cowork_settings.json"} {
		copyIfPresent(
			filepath.Join(sourceConfigDir, name),
			filepath.Join(tmpBase, name),
			stripSettingsForResume,
		)
	}
}

// envOrProcess reads a variable from the options environment, falling back
// to the process environment.
func envOrProcess(optEnv map[string]string, name string) string {
	if v := optEnv[name]; v != "" {
		return v
	}
	return os.Getenv(name)
}

// stripSettingsForResume drops settings keys that misbehave under a
// redirected config directory.
//
// It removes resumeStrippedSettingsKeys and env.CLAUDE_CONFIG_DIR, which
// would point the subprocess's config reads away from the temporary
// directory. Content that does not parse as a JSON object is returned
// untouched, so the subprocess sees exactly what the CLI would have read.
func stripSettingsForResume(content []byte) []byte {
	// Trim a UTF-8 BOM the way the CLI's settings reader does: PowerShell
	// writes settings.json with one, which a plain decode rejects.
	trimmed := strings.TrimPrefix(string(content), "\ufeff")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return content
	}

	stripped := false
	for _, key := range resumeStrippedSettingsKeys {
		if _, ok := parsed[key]; ok {
			delete(parsed, key)
			stripped = true
		}
	}
	if envBlock, ok := parsed["env"].(map[string]any); ok {
		if _, ok := envBlock["CLAUDE_CONFIG_DIR"]; ok {
			delete(envBlock, "CLAUDE_CONFIG_DIR")
			stripped = true
		}
	}
	if !stripped {
		return content
	}

	encoded, err := json.Marshal(parsed)
	if err != nil {
		return content
	}
	return encoded
}

// writeRedactedCredentials writes credentials with
// claudeAiOauth.refreshToken removed.
//
// The resumed subprocess runs under a redirected CLAUDE_CONFIG_DIR. If it
// refreshed, the single-use refresh token would be consumed server-side and
// the new tokens written where the parent never reads them back, leaving the
// parent's stored credentials revoked. With no refresh token the
// subprocess's refresh check short-circuits.
func writeRedactedCredentials(credsJSON []byte, dst string) {
	if credsJSON == nil {
		return
	}

	out := credsJSON
	var data map[string]any
	if err := json.Unmarshal(credsJSON, &data); err == nil {
		if oauth, ok := data["claudeAiOauth"].(map[string]any); ok {
			if _, ok := oauth["refreshToken"]; ok {
				delete(oauth, "refreshToken")
				if encoded, err := json.Marshal(data); err == nil {
					out = encoded
				}
			}
		}
	}
	// Unparseable content is written through: the subprocess will fail to
	// parse it too, which is the same outcome the CLI would produce.

	if err := os.WriteFile(dst, out, 0o600); err != nil {
		log.Printf("agentsdk: resume: skipping credentials copy (%v)", err)
	}
}

// readIfPresent reads a regular file, or returns nil.
//
// A missing source is skipped silently. Any other reason it cannot be read —
// a permission error, or a directory or FIFO where a file was expected — is
// logged and skipped: these files are best-effort enrichment of the
// temporary config directory, so an unreadable one must not abort, or for a
// FIFO hang, the resume.
func readIfPresent(src string) []byte {
	fi, err := os.Stat(src)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("agentsdk: resume: skipping %s (%v)", src, err)
		}
		return nil
	}
	if !fi.Mode().IsRegular() {
		log.Printf("agentsdk: resume: skipping %s (not a regular file)", src)
		return nil
	}

	content, err := os.ReadFile(src)
	if err != nil {
		log.Printf("agentsdk: resume: skipping %s (%v)", src, err)
		return nil
	}
	return content
}

// copyIfPresent copies src to dst with mode 0600 when it exists, through an
// optional transform. See readIfPresent for the skip policy.
func copyIfPresent(src, dst string, transform func([]byte) []byte) {
	content := readIfPresent(src)
	if content == nil {
		return
	}
	if transform != nil {
		content = transform(content)
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		// Do not leave a truncated destination behind for the subprocess to
		// misparse.
		_ = os.Remove(dst)
		log.Printf("agentsdk: resume: skipping %s (%v)", src, err)
	}
}

// readKeychainCredentials reads OAuth credentials JSON from the macOS
// Keychain under the default service name. It is best-effort, returning nil
// on any error and on non-macOS platforms.
func readKeychainCredentials() []byte {
	if runtime.GOOS != "darwin" {
		return nil
	}

	user := os.Getenv("USER")
	if user == "" {
		user = "claude-code-user"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "security",
		"find-generic-password", "-a", user, "-w", "-s", keychainServiceName).Output()
	if err != nil {
		return nil
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
		return []byte(trimmed)
	}
	return nil
}
