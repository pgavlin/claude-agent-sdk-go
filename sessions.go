package agentsdk

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

// SDKSessionInfo is session metadata returned by ListSessions.
//
// It contains only data extractable from a stat plus head and tail reads, so
// no full JSONL parse is required.
type SDKSessionInfo struct {
	// SessionID is the unique session identifier (a UUID).
	SessionID string `json:"session_id"`

	// Summary is the display title for the session: the custom title, the
	// auto-generated summary, or the first prompt.
	Summary string `json:"summary"`

	// LastModified is the last modified time in milliseconds since epoch.
	LastModified int64 `json:"last_modified"`

	// FileSize is the session file size in bytes. It is populated only for
	// local JSONL storage and is nil for store-backed reads.
	FileSize *int64 `json:"file_size,omitempty"`

	// CustomTitle is the user-set custom title, or an AI-generated title.
	CustomTitle string `json:"custom_title,omitempty"`

	// FirstPrompt is the first meaningful user prompt in the session.
	FirstPrompt string `json:"first_prompt,omitempty"`

	// GitBranch is the git branch at the end of the session.
	GitBranch string `json:"git_branch,omitempty"`

	// CWD is the working directory for the session.
	CWD string `json:"cwd,omitempty"`

	// Tag is the user-set session tag.
	Tag string `json:"tag,omitempty"`

	// CreatedAt is the creation time in milliseconds since epoch, taken
	// from the first entry's ISO timestamp. It is more reliable than a
	// stat birth time, which some filesystems do not support. Zero when
	// unavailable.
	CreatedAt int64 `json:"created_at,omitempty"`
}

// SessionMessage is a user or assistant message from a session transcript,
// returned by GetSessionMessages for reading historical session data. Its
// fields match the SDK wire protocol types.
type SessionMessage struct {
	// Type is "user" or "assistant".
	Type string `json:"type"`

	// UUID is the unique message identifier.
	UUID string `json:"uuid"`

	// SessionID is the session this message belongs to.
	SessionID string `json:"session_id"`

	// Message is the raw Anthropic API message (role, content, and so on).
	Message any `json:"message"`

	// ParentToolUseID is, for messages from GetSubagentMessages, the ID of
	// the Agent tool_use block in the parent session that spawned the
	// subagent, recovered from the subagent's metadata. It is empty when
	// that metadata is unavailable, and always empty for top-level
	// GetSessionMessages results.
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`

	// ParentAgentID is, for subagent messages, the agent ID of the
	// subagent that spawned this one. It is empty when the subagent was
	// spawned by the main session or the metadata is unavailable, and
	// always empty for top-level session messages.
	ParentAgentID string `json:"parent_agent_id,omitempty"`
}

// liteReadBufSize is the size of the head and tail buffers for lite
// metadata reads.
const liteReadBufSize = 65536

// maxSanitizedLength bounds a single filesystem path component. Most
// filesystems limit components to 255 bytes; 200 leaves room for the hash
// suffix and separator.
const maxSanitizedLength = 200

var (
	uuidRe = regexp.MustCompile(
		`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	// skipFirstPromptPattern matches auto-generated or system messages that
	// are skipped when looking for the first meaningful user prompt.
	skipFirstPromptPattern = regexp.MustCompile(
		`^(?:<local-command-stdout>|<session-start-hook>|<tick>|<goal>|` +
			`\[Request interrupted by user[^\]]*\]|` +
			`\s*<ide_opened_file>[\s\S]*</ide_opened_file>\s*$|` +
			`\s*<ide_selection>[\s\S]*</ide_selection>\s*$)`)

	commandNameRe = regexp.MustCompile(`<command-name>(.*?)</command-name>`)

	sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9]`)
)

// validateUUID returns the string when it is a valid UUID, else "".
func validateUUID(maybeUUID string) string {
	if uuidRe.MatchString(maybeUUID) {
		return maybeUUID
	}
	return ""
}

// simpleHash is a 32-bit integer hash rendered in base 36, matching the
// CLI's directory naming. It reproduces the JavaScript
// `h = (h << 5) - h + c; h |= 0` loop, including the coercion to a signed
// 32-bit integer, and then formats the absolute value with toString(36).
func simpleHash(s string) string {
	var h int32
	for _, ch := range s {
		h = (h << 5) - h + int32(ch)
	}
	n := int64(h)
	if n < 0 {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var out []byte
	for n > 0 {
		out = append(out, digits[n%36])
		n /= 36
	}
	// Reverse.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// sanitizePath makes a string safe for use as a directory name by replacing
// every non-alphanumeric character with a hyphen. Paths exceeding
// maxSanitizedLength are truncated and suffixed with a hash.
func sanitizePath(name string) string {
	sanitized := sanitizeRe.ReplaceAllString(name, "-")
	if len(sanitized) <= maxSanitizedLength {
		return sanitized
	}
	return sanitized[:maxSanitizedLength] + "-" + simpleHash(name)
}

// normalizePath applies Unicode NFC normalization to a filesystem path.
//
// The CLI derives its project directory names from NFC-normalized paths, so
// the SDK must normalize identically or the two disagree on filesystems that
// store decomposed Unicode — notably macOS HFS+, where a path containing "é"
// round-trips as "e" plus a combining accent. Without this, a session
// written by the CLI would be invisible to the SDK's readers, and a
// store-mirrored session would land under a different project key than its
// local counterpart.
func normalizePath(path string) string {
	return norm.NFC.String(path)
}

// claudeConfigHomeDir returns the Claude config directory, respecting
// CLAUDE_CONFIG_DIR.
func claudeConfigHomeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return normalizePath(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return normalizePath(filepath.Join(home, ".claude"))
}

// projectsDir returns the projects directory. envOverride is consulted
// before the process environment so callers that pass CLAUDE_CONFIG_DIR to
// the subprocess via options.Env resolve the same directory the subprocess
// writes to.
func projectsDir(envOverride map[string]string) string {
	if override := envOverride["CLAUDE_CONFIG_DIR"]; override != "" {
		return filepath.Join(normalizePath(override), "projects")
	}
	return filepath.Join(claudeConfigHomeDir(), "projects")
}

// projectDirFor returns the projects subdirectory for a project path.
func projectDirFor(projectPath string) string {
	return filepath.Join(projectsDir(nil), sanitizePath(projectPath))
}

// canonicalizePath resolves a directory to its canonical form, following
// symlinks and applying NFC normalization. It falls back to the input on
// error, still normalized, so a path that cannot be resolved still yields a
// stable project key.
func canonicalizePath(d string) string {
	resolved, err := filepath.EvalSymlinks(d)
	if err != nil {
		if abs, absErr := filepath.Abs(d); absErr == nil {
			return normalizePath(abs)
		}
		return normalizePath(d)
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		return normalizePath(abs)
	}
	return normalizePath(resolved)
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// findProjectDir finds the project directory for a path, returning "" when
// there is none.
//
// It tolerates hash mismatches for long paths (over maxSanitizedLength): the
// CLI uses Bun.hash while the SDK uses simpleHash, so those paths can
// produce different directory suffixes. When the exact match is absent, it
// falls back to prefix-based scanning.
func findProjectDir(projectPath string) string {
	exact := projectDirFor(projectPath)
	if isDir(exact) {
		return exact
	}

	// The exact match failed. For short paths that means no sessions
	// exist; only long paths can have a differing hash suffix.
	sanitized := sanitizePath(projectPath)
	if len(sanitized) <= maxSanitizedLength {
		return ""
	}

	prefix := sanitized[:maxSanitizedLength]
	entries, err := os.ReadDir(projectsDir(nil))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix+"-") {
			return filepath.Join(projectsDir(nil), entry.Name())
		}
	}
	return ""
}

// unescapeJSONString unescapes a JSON string value extracted as raw text.
func unescapeJSONString(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	var out string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &out); err != nil {
		return raw
	}
	return out
}

// scanJSONStringValue returns the unescaped value starting at valueStart and
// the index just past its closing quote. ok is false when the string is
// unterminated.
func scanJSONStringValue(text string, valueStart int) (value string, end int, ok bool) {
	for i := valueStart; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++ // Skip the escaped character.
		case '"':
			return unescapeJSONString(text[valueStart:i]), i, true
		}
	}
	return "", len(text), false
}

// extractJSONStringField extracts a simple JSON string field without a full
// parse, looking for "key":"value" and "key": "value". It returns the first
// match, or "" when there is none. It works on truncated lines.
func extractJSONStringField(text, key string) string {
	for _, pattern := range []string{`"` + key + `":"`, `"` + key + `": "`} {
		idx := strings.Index(text, pattern)
		if idx < 0 {
			continue
		}
		if value, _, ok := scanJSONStringValue(text, idx+len(pattern)); ok {
			return value
		}
	}
	return ""
}

// extractLastJSONStringField is extractJSONStringField for the last
// occurrence rather than the first.
func extractLastJSONStringField(text, key string) string {
	var lastValue string
	for _, pattern := range []string{`"` + key + `":"`, `"` + key + `": "`} {
		searchFrom := 0
		for {
			rel := strings.Index(text[searchFrom:], pattern)
			if rel < 0 {
				break
			}
			idx := searchFrom + rel
			value, end, ok := scanJSONStringValue(text, idx+len(pattern))
			if ok {
				lastValue = value
			}
			searchFrom = end + 1
			if searchFrom > len(text) {
				break
			}
		}
	}
	return lastValue
}

// truncatePrompt caps a prompt at 200 characters, appending an ellipsis.
func truncatePrompt(s string) string {
	runes := []rune(s)
	if len(runes) <= 200 {
		return s
	}
	return strings.TrimRight(string(runes[:200]), " \t\n") + "…"
}

// entryTextBlocks extracts text strings from a user entry's message content.
func entryTextBlocks(entry map[string]any) []string {
	message, ok := entry["message"].(map[string]any)
	if !ok {
		return nil
	}
	var texts []string
	switch content := message["content"].(type) {
	case string:
		texts = append(texts, content)
	case []any:
		for _, raw := range content {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	return texts
}

// extractFirstPromptFromHead extracts the first meaningful user prompt from
// a JSONL head chunk, skipping tool_result messages, isMeta,
// isCompactSummary, command-name messages, and auto-generated patterns.
func extractFirstPromptFromHead(head string) string {
	var commandFallback string

	for _, line := range strings.Split(head, "\n") {
		if !strings.Contains(line, `"type":"user"`) && !strings.Contains(line, `"type": "user"`) {
			continue
		}
		if strings.Contains(line, `"tool_result"`) {
			continue
		}
		if strings.Contains(line, `"isMeta":true`) || strings.Contains(line, `"isMeta": true`) {
			continue
		}
		if strings.Contains(line, `"isCompactSummary":true`) || strings.Contains(line, `"isCompactSummary": true`) {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["type"] != "user" {
			continue
		}

		for _, raw := range entryTextBlocks(entry) {
			result := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
			if result == "" {
				continue
			}

			// Skip slash-command messages, remembering the first as a
			// fallback.
			if m := commandNameRe.FindStringSubmatch(result); m != nil {
				if commandFallback == "" {
					commandFallback = m[1]
				}
				continue
			}
			if skipFirstPromptPattern.MatchString(result) {
				continue
			}
			return truncatePrompt(result)
		}
	}

	return commandFallback
}

// liteSessionFile is the result of reading a session file's head, tail,
// mtime, and size.
type liteSessionFile struct {
	mtime int64
	size  int64
	head  string
	tail  string
}

// readSessionLite opens a session file, stats it, and reads the head and
// tail. It returns nil on any error or when the file is empty.
func readSessionLite(filePath string) *liteSessionFile {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	size := fi.Size()
	mtime := fi.ModTime().UnixMilli()

	headBuf := make([]byte, liteReadBufSize)
	n, err := f.ReadAt(headBuf, 0)
	if n == 0 {
		return nil
	}
	_ = err // A short read at EOF is expected and fine.
	head := string(headBuf[:n])

	tail := head
	if tailOffset := size - liteReadBufSize; tailOffset > 0 {
		tailBuf := make([]byte, liteReadBufSize)
		tn, _ := f.ReadAt(tailBuf, tailOffset)
		tail = string(tailBuf[:tn])
	}

	return &liteSessionFile{mtime: mtime, size: size, head: head, tail: tail}
}

// getWorktreePaths returns absolute worktree paths for the git repository
// containing cwd, or nil when git is unavailable or cwd is not in a repo.
func getWorktreePaths(cwd string) []string {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if after, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, normalizePath(strings.TrimRight(after, "\r")))
		}
	}
	return paths
}

// isoToEpochMS parses an ISO-8601 timestamp into Unix epoch milliseconds,
// returning 0 when it cannot be parsed.
func isoToEpochMS(ts string) int64 {
	if ts == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

// parseSessionInfoFromLite parses SDKSessionInfo fields from a lite session
// read. It returns nil for sidechain sessions and for metadata-only sessions
// with no extractable summary.
func parseSessionInfoFromLite(sessionID string, lite *liteSessionFile, projectPath string) *SDKSessionInfo {
	head, tail := lite.head, lite.tail

	// Check the first line for sidechain sessions.
	firstLine := head
	if idx := strings.Index(head, "\n"); idx >= 0 {
		firstLine = head[:idx]
	}
	if strings.Contains(firstLine, `"isSidechain":true`) || strings.Contains(firstLine, `"isSidechain": true`) {
		return nil
	}

	// A user-set title wins over an AI-generated one. The head fallback
	// covers short sessions whose title entry may not reach the tail.
	customTitle := firstNonEmpty(
		extractLastJSONStringField(tail, "customTitle"),
		extractLastJSONStringField(head, "customTitle"),
		extractLastJSONStringField(tail, "aiTitle"),
		extractLastJSONStringField(head, "aiTitle"),
	)
	firstPrompt := extractFirstPromptFromHead(head)

	// The lastPrompt tail entry shows what the user was most recently doing.
	summary := firstNonEmpty(
		customTitle,
		extractLastJSONStringField(tail, "lastPrompt"),
		extractLastJSONStringField(tail, "summary"),
		firstPrompt,
	)

	// Skip metadata-only sessions: no title, no summary, no prompt.
	if summary == "" {
		return nil
	}

	gitBranch := firstNonEmpty(
		extractLastJSONStringField(tail, "gitBranch"),
		extractJSONStringField(head, "gitBranch"),
	)
	sessionCWD := firstNonEmpty(extractJSONStringField(head, "cwd"), projectPath)

	// Scope tag extraction to {"type":"tag"} lines: a bare tail scan for
	// "tag" would match tool_use inputs such as git tags, Docker tags, and
	// cloud resource tags.
	var tag string
	tailLines := strings.Split(tail, "\n")
	for i := len(tailLines) - 1; i >= 0; i-- {
		if strings.HasPrefix(tailLines[i], `{"type":"tag"`) {
			tag = extractLastJSONStringField(tailLines[i], "tag")
			break
		}
	}

	// createdAt comes from the first ISO timestamp in the head. It is more
	// reliable than a stat birth time, which some filesystems do not
	// support. The whole head is scanned rather than only the first line,
	// since the first record may be a metadata-only entry (a
	// permission-mode marker, say) with no timestamp; the first
	// user or assistant record that follows does carry one.
	createdAt := isoToEpochMS(extractJSONStringField(head, "timestamp"))

	size := lite.size
	return &SDKSessionInfo{
		SessionID:    sessionID,
		Summary:      summary,
		LastModified: lite.mtime,
		FileSize:     &size,
		CustomTitle:  customTitle,
		FirstPrompt:  firstPrompt,
		GitBranch:    gitBranch,
		CWD:          sessionCWD,
		Tag:          tag,
		CreatedAt:    createdAt,
	}
}

// firstNonEmpty returns the first non-empty string, or "".
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// readSessionsFromDir reads session files from one project directory. Each
// file gets a stat plus a head and tail read. Sidechain sessions and
// metadata-only sessions are filtered out.
func readSessionsFromDir(projectDir, projectPath string) []SDKSessionInfo {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}

	var results []SDKSessionInfo
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sessionID := validateUUID(strings.TrimSuffix(name, ".jsonl"))
		if sessionID == "" {
			continue
		}
		lite := readSessionLite(filepath.Join(projectDir, name))
		if lite == nil {
			continue
		}
		if info := parseSessionInfoFromLite(sessionID, lite, projectPath); info != nil {
			results = append(results, *info)
		}
	}
	return results
}

// deduplicateBySessionID keeps the newest LastModified per session ID.
func deduplicateBySessionID(sessions []SDKSessionInfo) []SDKSessionInfo {
	byID := make(map[string]SDKSessionInfo, len(sessions))
	// Preserve a deterministic output order: first appearance wins the slot.
	var order []string
	for _, s := range sessions {
		existing, ok := byID[s.SessionID]
		if !ok {
			order = append(order, s.SessionID)
			byID[s.SessionID] = s
			continue
		}
		if s.LastModified > existing.LastModified {
			byID[s.SessionID] = s
		}
	}
	out := make([]SDKSessionInfo, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

// applySortLimitOffset sorts by LastModified descending, then applies the
// offset and limit. A limit of zero or less means no limit.
func applySortLimitOffset(sessions []SDKSessionInfo, limit, offset int) []SDKSessionInfo {
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].LastModified > sessions[j].LastModified
	})
	if offset > 0 {
		if offset >= len(sessions) {
			return nil
		}
		sessions = sessions[offset:]
	}
	if limit > 0 && limit < len(sessions) {
		sessions = sessions[:limit]
	}
	return sessions
}

// ListSessionsOptions configures ListSessions.
type ListSessionsOptions struct {
	// Directory scopes the listing to one project directory and, unless
	// ExcludeWorktrees is set, its git worktrees. When empty, sessions
	// across all projects are returned.
	Directory string

	// Limit caps the number of sessions returned. Zero means no limit.
	Limit int

	// Offset skips this many sessions from the start of the sorted result
	// set. Use it with Limit to paginate.
	Offset int

	// ExcludeWorktrees skips scanning sibling git worktrees when Directory
	// is set and lies inside a git repository. Worktrees are scanned by
	// default, matching the Python SDK's include_worktrees=True.
	ExcludeWorktrees bool
}

// ListSessions lists sessions with metadata extracted from a stat plus head
// and tail reads, sorted by LastModified descending.
//
// See ListSessionsFromStore for the SessionStore-backed variant.
func ListSessions(opts ListSessionsOptions) []SDKSessionInfo {
	if opts.Directory != "" {
		return listSessionsForProject(opts.Directory, opts.Limit, opts.Offset, !opts.ExcludeWorktrees)
	}
	return listAllSessions(opts.Limit, opts.Offset)
}

// listSessionsForProject lists sessions for one project directory and its
// worktrees.
func listSessionsForProject(directory string, limit, offset int, includeWorktrees bool) []SDKSessionInfo {
	canonicalDir := canonicalizePath(directory)

	var worktreePaths []string
	if includeWorktrees {
		worktreePaths = getWorktreePaths(canonicalDir)
	}

	// With no worktrees — or git unavailable, or scanning disabled — scan
	// the single project directory.
	if len(worktreePaths) <= 1 {
		projectDir := findProjectDir(canonicalDir)
		if projectDir == "" {
			return nil
		}
		return applySortLimitOffset(readSessionsFromDir(projectDir, canonicalDir), limit, offset)
	}

	root := projectsDir(nil)

	// Sort worktree paths by sanitized prefix length, longest first, so
	// more specific matches take priority.
	type indexedWorktree struct {
		path   string
		prefix string
	}
	indexed := make([]indexedWorktree, 0, len(worktreePaths))
	for _, wt := range worktreePaths {
		indexed = append(indexed, indexedWorktree{path: wt, prefix: foldCase(sanitizePath(wt))})
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		return len(indexed[i].prefix) > len(indexed[j].prefix)
	})

	allDirents, err := os.ReadDir(root)
	if err != nil {
		// Fall back to the single project directory.
		projectDir := findProjectDir(canonicalDir)
		if projectDir == "" {
			return nil
		}
		return applySortLimitOffset(readSessionsFromDir(projectDir, canonicalDir), limit, offset)
	}

	var allSessions []SDKSessionInfo
	seenDirs := map[string]struct{}{}

	// Always include the caller's actual directory, which handles
	// subdirectories such as /repo/packages/my-app that do not match a
	// worktree root prefix.
	if canonicalProjectDir := findProjectDir(canonicalDir); canonicalProjectDir != "" {
		seenDirs[foldCase(filepath.Base(canonicalProjectDir))] = struct{}{}
		allSessions = append(allSessions, readSessionsFromDir(canonicalProjectDir, canonicalDir)...)
	}

	for _, entry := range allDirents {
		if !entry.IsDir() {
			continue
		}
		dirName := foldCase(entry.Name())
		if _, seen := seenDirs[dirName]; seen {
			continue
		}
		for _, wt := range indexed {
			// Use a prefix match only for truncated paths, where a hash
			// suffix follows. Short paths require an exact match so
			// /root/project does not match /root/project-foo.
			isMatch := dirName == wt.prefix ||
				(len(wt.prefix) >= maxSanitizedLength && strings.HasPrefix(dirName, wt.prefix+"-"))
			if isMatch {
				seenDirs[dirName] = struct{}{}
				allSessions = append(allSessions, readSessionsFromDir(filepath.Join(root, entry.Name()), wt.path)...)
				break
			}
		}
	}

	return applySortLimitOffset(deduplicateBySessionID(allSessions), limit, offset)
}

// listAllSessions lists sessions across every project directory.
func listAllSessions(limit, offset int) []SDKSessionInfo {
	root := projectsDir(nil)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var allSessions []SDKSessionInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		allSessions = append(allSessions, readSessionsFromDir(filepath.Join(root, entry.Name()), "")...)
	}

	return applySortLimitOffset(deduplicateBySessionID(allSessions), limit, offset)
}

// GetSessionInfo reads metadata for a single session by ID, without the
// directory scan ListSessions performs.
//
// directory is the project path, with the same semantics as
// ListSessionsOptions.Directory; when it is empty, every project directory
// is searched for the session file. It returns nil when the session file is
// not found, is a sidechain session, or has no extractable summary.
//
// See GetSessionInfoFromStore for the SessionStore-backed variant.
func GetSessionInfo(sessionID, directory string) *SDKSessionInfo {
	uuid := validateUUID(sessionID)
	if uuid == "" {
		return nil
	}
	fileName := uuid + ".jsonl"

	if directory != "" {
		canonical := canonicalizePath(directory)
		if projectDir := findProjectDir(canonical); projectDir != "" {
			if lite := readSessionLite(filepath.Join(projectDir, fileName)); lite != nil {
				return parseSessionInfoFromLite(uuid, lite, canonical)
			}
		}

		// Worktree fallback, matching GetSessionMessages: sessions may
		// live under a different worktree root.
		for _, wt := range getWorktreePaths(canonical) {
			if wt == canonical {
				continue
			}
			if wtProjectDir := findProjectDir(wt); wtProjectDir != "" {
				if lite := readSessionLite(filepath.Join(wtProjectDir, fileName)); lite != nil {
					return parseSessionInfoFromLite(uuid, lite, wt)
				}
			}
		}
		return nil
	}

	// No directory: search all project directories for the session file.
	root := projectsDir(nil)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if lite := readSessionLite(filepath.Join(root, entry.Name(), fileName)); lite != nil {
			return parseSessionInfoFromLite(uuid, lite, "")
		}
	}
	return nil
}

// transcriptEntryTypes are the transcript entry types that carry uuid and
// parentUuid chain links.
var transcriptEntryTypes = map[string]struct{}{
	"user": {}, "assistant": {}, "progress": {}, "system": {}, "attachment": {},
}

// transcriptEntry is a parsed JSONL transcript entry. It mirrors the
// TypeScript SDK's TranscriptEntry as a loose map, with fields such as type,
// uuid, parentUuid, sessionId, message, isSidechain, isMeta,
// isCompactSummary, and teamName.
type transcriptEntry = map[string]any

// entryString reads a string field from a transcript entry.
func entryString(entry transcriptEntry, key string) string {
	s, _ := entry[key].(string)
	return s
}

// entryBool reports whether a transcript entry field is boolean true.
func entryBool(entry transcriptEntry, key string) bool {
	b, _ := entry[key].(bool)
	return b
}

// tryReadSessionFile reads a session JSONL file from a project directory.
func tryReadSessionFile(projectDir, fileName string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, fileName))
	if err != nil {
		return ""
	}
	return string(data)
}

// readSessionFile finds and reads a session's JSONL file.
//
// When directory is set it looks in that project directory and its git
// worktrees, with the prefix fallback for hash mismatches on long paths.
// Otherwise it searches every project directory. It returns "" when not
// found.
func readSessionFile(sessionID, directory string) string {
	fileName := sessionID + ".jsonl"

	if directory != "" {
		canonicalDir := canonicalizePath(directory)

		if projectDir := findProjectDir(canonicalDir); projectDir != "" {
			if content := tryReadSessionFile(projectDir, fileName); content != "" {
				return content
			}
		}

		// Sessions may live under a different worktree root.
		for _, wt := range getWorktreePaths(canonicalDir) {
			if wt == canonicalDir {
				continue // Already tried above.
			}
			if wtProjectDir := findProjectDir(wt); wtProjectDir != "" {
				if content := tryReadSessionFile(wtProjectDir, fileName); content != "" {
					return content
				}
			}
		}
		return ""
	}

	root := projectsDir(nil)
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if content := tryReadSessionFile(filepath.Join(root, entry.Name()), fileName); content != "" {
			return content
		}
	}
	return ""
}

// parseTranscriptEntries parses JSONL content into transcript entries,
// keeping only entries that have a uuid and a transcript message type.
// Corrupt lines are skipped.
func parseTranscriptEntries(content string) []transcriptEntry {
	var entries []transcriptEntry
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entryType, _ := entry["type"].(string)
		if _, ok := transcriptEntryTypes[entryType]; !ok {
			continue
		}
		if _, ok := entry["uuid"].(string); !ok {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// buildConversationChain finds the conversation leaf and walks parentUuid
// links back to the root, returning messages in chronological order.
//
// logicalParentUuid, set on compact_boundary entries, is intentionally not
// followed. This matches the VS Code IDE: post-compaction the
// isCompactSummary message replaces earlier messages, so following logical
// parents would duplicate content.
func buildConversationChain(entries []transcriptEntry) []transcriptEntry {
	if len(entries) == 0 {
		return nil
	}

	byUUID := make(map[string]transcriptEntry, len(entries))
	entryIndex := make(map[string]int, len(entries))
	for i, entry := range entries {
		uid := entryString(entry, "uuid")
		byUUID[uid] = entry
		entryIndex[uid] = i
	}

	// Find terminal messages: those no other entry points to via parentUuid.
	parentUUIDs := map[string]struct{}{}
	for _, entry := range entries {
		if parent := entryString(entry, "parentUuid"); parent != "" {
			parentUUIDs[parent] = struct{}{}
		}
	}

	// From each terminal, walk back to the nearest user or assistant leaf.
	var leaves []transcriptEntry
	for _, entry := range entries {
		if _, isParent := parentUUIDs[entryString(entry, "uuid")]; isParent {
			continue
		}
		cur, ok := entry, true
		seen := map[string]struct{}{}
		for ok {
			uid := entryString(cur, "uuid")
			if _, dup := seen[uid]; dup {
				break
			}
			seen[uid] = struct{}{}
			if t := entryString(cur, "type"); t == "user" || t == "assistant" {
				leaves = append(leaves, cur)
				break
			}
			parent := entryString(cur, "parentUuid")
			if parent == "" {
				break
			}
			cur, ok = byUUID[parent]
		}
	}

	if len(leaves) == 0 {
		return nil
	}

	// Prefer a leaf on the main chain — not a sidechain, team, or meta
	// entry — and among those the one latest in file order.
	var mainLeaves []transcriptEntry
	for _, leaf := range leaves {
		if !entryBool(leaf, "isSidechain") && entryString(leaf, "teamName") == "" && !entryBool(leaf, "isMeta") {
			mainLeaves = append(mainLeaves, leaf)
		}
	}

	pickBest := func(candidates []transcriptEntry) transcriptEntry {
		best := candidates[0]
		bestIdx := entryIndex[entryString(best, "uuid")]
		for _, cur := range candidates[1:] {
			if curIdx := entryIndex[entryString(cur, "uuid")]; curIdx > bestIdx {
				best, bestIdx = cur, curIdx
			}
		}
		return best
	}

	leaf := pickBest(leaves)
	if len(mainLeaves) > 0 {
		leaf = pickBest(mainLeaves)
	}

	// Walk from the leaf back to the root via parentUuid.
	var chain []transcriptEntry
	seen := map[string]struct{}{}
	for cur, ok := leaf, true; ok; {
		uid := entryString(cur, "uuid")
		if _, dup := seen[uid]; dup {
			break
		}
		seen[uid] = struct{}{}
		chain = append(chain, cur)
		parent := entryString(cur, "parentUuid")
		if parent == "" {
			break
		}
		cur, ok = byUUID[parent]
	}

	// Reverse into chronological order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// isVisibleMessage reports whether an entry belongs in the returned messages.
//
// isCompactSummary messages are intentionally included: they carry the
// summarized content from compacted conversations and are its only
// representation post-compaction, matching the VS Code IDE.
func isVisibleMessage(entry transcriptEntry) bool {
	if t := entryString(entry, "type"); t != "user" && t != "assistant" {
		return false
	}
	if entryBool(entry, "isMeta") || entryBool(entry, "isSidechain") {
		return false
	}
	return entryString(entry, "teamName") == ""
}

// toSessionMessage converts a transcript entry into a SessionMessage.
func toSessionMessage(entry transcriptEntry, parentToolUseID, parentAgentID string) SessionMessage {
	msgType := "assistant"
	if entryString(entry, "type") == "user" {
		msgType = "user"
	}
	return SessionMessage{
		Type:            msgType,
		UUID:            entryString(entry, "uuid"),
		SessionID:       entryString(entry, "sessionId"),
		Message:         entry["message"],
		ParentToolUseID: parentToolUseID,
		ParentAgentID:   parentAgentID,
	}
}

// GetSessionMessages reads a session's conversation messages from its JSONL
// transcript.
//
// It parses the full JSONL, builds the conversation chain via parentUuid
// links, and returns user and assistant messages in chronological order.
// directory is the project directory to find the session in; when empty,
// every project directory under the Claude projects root is searched. A
// limit of zero means no limit.
//
// It returns nil when the session is not found, sessionID is not a valid
// UUID, or the transcript contains no visible messages.
//
// See GetSessionMessagesFromStore for the SessionStore-backed variant.
func GetSessionMessages(sessionID, directory string, limit, offset int) []SessionMessage {
	if validateUUID(sessionID) == "" {
		return nil
	}
	content := readSessionFile(sessionID, directory)
	if content == "" {
		return nil
	}
	return entriesToSessionMessages(parseTranscriptEntries(content), limit, offset)
}

// entriesToSessionMessages builds the conversation chain from parsed entries
// and applies paging. It is shared by the filesystem and SessionStore paths.
func entriesToSessionMessages(entries []transcriptEntry, limit, offset int) []SessionMessage {
	var messages []SessionMessage
	for _, entry := range buildConversationChain(entries) {
		if isVisibleMessage(entry) {
			messages = append(messages, toSessionMessage(entry, "", ""))
		}
	}
	return pageSessionMessages(messages, limit, offset)
}

// pageSessionMessages applies an offset and limit to a message slice.
func pageSessionMessages(messages []SessionMessage, limit, offset int) []SessionMessage {
	if offset > 0 {
		if offset >= len(messages) {
			return nil
		}
		messages = messages[offset:]
	}
	if limit > 0 && limit < len(messages) {
		messages = messages[:limit]
	}
	return messages
}

// ProjectKeyForDirectory returns the SessionKey.ProjectKey for a directory,
// using the same sanitization the CLI applies to its on-disk project
// directory names. An empty directory means the process working directory.
//
// Use it to build a SessionKey for store-backed helpers when the session was
// mirrored from a known working directory.
func ProjectKeyForDirectory(directory string) string {
	if directory == "" {
		if wd, err := os.Getwd(); err == nil {
			directory = wd
		}
	}
	return sanitizePath(canonicalizePath(directory))
}

// foldCase normalizes a directory name for comparison on case-insensitive
// filesystems. It lowercases on Windows and is the identity elsewhere,
// matching the Python SDK's sys.platform == "win32" check.
func foldCase(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(s)
	}
	return s
}

// statCandidate returns the path when it names a non-empty file, else "".
func statCandidate(projectDir, fileName string) string {
	candidate := filepath.Join(projectDir, fileName)
	if fi, err := os.Stat(candidate); err == nil && fi.Size() > 0 {
		return candidate
	}
	return ""
}

// resolveSessionFilePath resolves the on-disk path of a session JSONL file.
//
// Directory resolution mirrors readSessionFile: when directory is set it
// looks in that project directory and its git worktrees, and otherwise
// searches every project directory. It returns the first non-empty match, or
// "" when there is none.
func resolveSessionFilePath(sessionID, directory string) string {
	fileName := sessionID + ".jsonl"

	if directory != "" {
		canonicalDir := canonicalizePath(directory)

		if projectDir := findProjectDir(canonicalDir); projectDir != "" {
			if found := statCandidate(projectDir, fileName); found != "" {
				return found
			}
		}

		for _, wt := range getWorktreePaths(canonicalDir) {
			if wt == canonicalDir {
				continue
			}
			if wtProjectDir := findProjectDir(wt); wtProjectDir != "" {
				if found := statCandidate(wtProjectDir, fileName); found != "" {
					return found
				}
			}
		}
		return ""
	}

	root := projectsDir(nil)
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if found := statCandidate(filepath.Join(root, entry.Name()), fileName); found != "" {
			return found
		}
	}
	return ""
}

// resolveSubagentsDir resolves a session's subagents directory.
//
// The session file lives at <projectDir>/<sessionID>.jsonl and the subagents
// directory at <projectDir>/<sessionID>/subagents/. It returns "" when the
// session cannot be found.
func resolveSubagentsDir(sessionID, directory string) string {
	resolved := resolveSessionFilePath(sessionID, directory)
	if resolved == "" {
		return ""
	}
	// Strip the .jsonl suffix to derive the session directory.
	sessionDir := strings.TrimSuffix(resolved, ".jsonl")
	return filepath.Join(sessionDir, "subagents")
}

// agentFile pairs a subagent ID with its transcript path.
type agentFile struct {
	agentID string
	path    string
}

// collectAgentFiles recursively collects agent-*.jsonl files from a
// directory tree. Subagent transcripts may live directly in subagents/ or in
// nested subdirectories such as subagents/workflows/<runID>/.
func collectAgentFiles(baseDir string) []agentFile {
	var results []agentFile

	var walk func(dir string)
	walk = func(dir string) {
		dirents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		// ReadDir already sorts by filename, matching the Python SDK's
		// explicit sort so agent order is stable.
		for _, entry := range dirents {
			name := entry.Name()
			switch {
			case entry.IsDir():
				walk(filepath.Join(dir, name))
			case strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl"):
				results = append(results, agentFile{
					agentID: strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl"),
					path:    filepath.Join(dir, name),
				})
			}
		}
	}
	walk(baseDir)

	return results
}

// buildSubagentChain builds the conversation chain for a subagent
// transcript.
//
// Subagent transcripts are simpler than main sessions: no compaction, no
// sidechains, no preserved segments. The last user or assistant entry is the
// leaf, and parentUuid links lead back to the root.
func buildSubagentChain(entries []transcriptEntry) []transcriptEntry {
	if len(entries) == 0 {
		return nil
	}

	byUUID := make(map[string]transcriptEntry, len(entries))
	for _, entry := range entries {
		byUUID[entryString(entry, "uuid")] = entry
	}

	// Subagent transcripts are linear, so the last user or assistant entry
	// is the leaf.
	var leaf transcriptEntry
	for i := len(entries) - 1; i >= 0; i-- {
		if t := entryString(entries[i], "type"); t == "user" || t == "assistant" {
			leaf = entries[i]
			break
		}
	}
	if leaf == nil {
		return nil
	}

	var chain []transcriptEntry
	seen := map[string]struct{}{}
	for cur, ok := leaf, true; ok; {
		uid := entryString(cur, "uuid")
		if _, dup := seen[uid]; dup {
			break
		}
		seen[uid] = struct{}{}
		chain = append(chain, cur)
		parent := entryString(cur, "parentUuid")
		if parent == "" {
			break
		}
		cur, ok = byUUID[parent]
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// ListSubagents lists subagent IDs for a session by scanning its subagents
// directory.
//
// Subagent transcripts live at
// ~/.claude/projects/<project>/<sessionID>/subagents/agent-<agentID>.jsonl,
// and may be nested in subdirectories such as workflows/<runID>/. directory
// is the project directory to find the session in; when empty, every project
// directory is searched.
//
// It returns nil when the session is not found, sessionID is not a valid
// UUID, or the session has no subagents.
//
// See ListSubagentsFromStore for the SessionStore-backed variant.
func ListSubagents(sessionID, directory string) []string {
	if validateUUID(sessionID) == "" {
		return nil
	}
	subagentsDir := resolveSubagentsDir(sessionID, directory)
	if subagentsDir == "" {
		return nil
	}

	files := collectAgentFiles(subagentsDir)
	if len(files) == 0 {
		return nil
	}
	ids := make([]string, 0, len(files))
	for _, f := range files {
		ids = append(ids, f.agentID)
	}
	return ids
}

// GetSubagentMessages reads a subagent's conversation messages from its
// JSONL transcript.
//
// It parses the transcript, builds the conversation chain via parentUuid
// links, and returns user and assistant messages in chronological order.
// Each message's ParentToolUseID is the ID of the Agent tool_use in the
// parent session that spawned this subagent, and ParentAgentID the spawning
// subagent for nested subagents; both come from the agent-<agentID>.meta.json
// sidecar beside the transcript, and are empty when it is missing or
// unusable. A limit of zero means no limit.
//
// See GetSubagentMessagesFromStore for the SessionStore-backed variant.
func GetSubagentMessages(sessionID, agentID, directory string, limit, offset int) []SessionMessage {
	if validateUUID(sessionID) == "" {
		return nil
	}
	subagentsDir := resolveSubagentsDir(sessionID, directory)
	if subagentsDir == "" {
		return nil
	}

	// The agent file may sit directly in subagents/ or in a nested
	// subdirectory, so scan to find it.
	var match string
	for _, f := range collectAgentFiles(subagentsDir) {
		if f.agentID == agentID {
			match = f.path
			break
		}
	}
	if match == "" {
		return nil
	}

	content, err := os.ReadFile(match)
	if err != nil || len(content) == 0 {
		return nil
	}

	// The .meta.json sidecar beside the transcript records which Agent
	// tool_use spawned this subagent, and for nested subagents the parent
	// agent ID. Like the transcript read above, any failure to read it
	// degrades to "no metadata" rather than failing this best-effort
	// helper.
	parentToolUseID, parentAgentID := parentIDsFromAgentMetadata(readAgentMetadataSidecar(match))

	return entriesToSubagentMessages(
		parseTranscriptEntries(string(content)), limit, offset, parentToolUseID, parentAgentID)
}

// agentMetadataSidecarPath maps agent-<id>.jsonl to agent-<id>.meta.json in
// the same directory. It is the single definition of the sidecar naming
// convention, shared by the read path, session import, and resume
// materialization.
func agentMetadataSidecarPath(transcriptPath string) string {
	return strings.TrimSuffix(transcriptPath, ".jsonl") + ".meta.json"
}

// readAgentMetadataSidecar reads the .meta.json sidecar beside a subagent
// transcript. It returns nil when the sidecar is missing, unreadable, not
// valid JSON, or not a JSON object: an unusable optional sidecar degrades to
// an absent one.
func readAgentMetadataSidecar(transcriptPath string) map[string]any {
	data, err := os.ReadFile(agentMetadataSidecarPath(transcriptPath))
	if err != nil {
		return nil
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return meta
}

// splitAgentMetadata separates the synthetic agent_metadata entry from
// transcript lines.
//
// A subagent's SessionStore stream carries its .meta.json sidecar as
// {"type": "agent_metadata", ...} entries alongside the transcript. The last
// such entry wins, since it is rewritten on resume.
func splitAgentMetadata(entries []SessionStoreEntry) (metadata map[string]any, transcript []SessionStoreEntry) {
	for _, e := range entries {
		if e.EntryType() == "agent_metadata" {
			metadata = e
			continue
		}
		transcript = append(transcript, e)
	}
	return metadata, transcript
}

// parentIDsFromAgentMetadata extracts the tool use ID and parent agent ID
// from an agent metadata map. It works for both the on-disk .meta.json
// sidecar and the synthetic agent_metadata entry a SessionStore receives in
// its place.
func parentIDsFromAgentMetadata(meta map[string]any) (toolUseID, parentAgentID string) {
	if meta == nil {
		return "", ""
	}
	toolUseID, _ = meta["toolUseId"].(string)
	parentAgentID, _ = meta["parentAgentId"].(string)
	return toolUseID, parentAgentID
}

// entriesToSubagentMessages builds the subagent chain from parsed entries
// and applies paging. It is shared by the filesystem and SessionStore paths.
// Every message in a subagent transcript shares the same parent IDs.
func entriesToSubagentMessages(
	entries []transcriptEntry,
	limit, offset int,
	parentToolUseID, parentAgentID string,
) []SessionMessage {
	var messages []SessionMessage
	for _, entry := range buildSubagentChain(entries) {
		if t := entryString(entry, "type"); t == "user" || t == "assistant" {
			messages = append(messages, toSessionMessage(entry, parentToolUseID, parentAgentID))
		}
	}
	return pageSessionMessages(messages, limit, offset)
}
