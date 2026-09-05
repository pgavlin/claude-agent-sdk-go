package agentsdk

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ErrSessionNotFound is returned when a session's transcript cannot be
// located. Test for it with errors.Is.
var ErrSessionNotFound = errors.New("session not found")

// forkTranscriptTypes are the entry types a fork copies. Unlike
// transcriptEntryTypes this includes attachment entries in the same set.
var forkTranscriptTypes = map[string]struct{}{
	"user": {}, "assistant": {}, "attachment": {}, "system": {}, "progress": {},
}

// RenameSession renames a session by appending a custom-title entry.
//
// ListSessions reads the last custom title from the file tail, so repeated
// calls are safe: the most recent wins. The title is stripped of surrounding
// whitespace and must be non-empty afterwards, matching the CLI guard —
// an empty title is rejected rather than overloaded as "clear the title".
//
// directory is the project directory, with the same semantics as
// ListSessionsOptions.Directory; when empty, every project directory is
// searched for the session file.
//
// See RenameSessionViaStore for the SessionStore-backed variant.
func RenameSession(sessionID, title, directory string) error {
	if validateUUID(sessionID) == "" {
		return NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}
	stripped := strings.TrimSpace(title)
	if stripped == "" {
		return NewValidationError("title must be non-empty")
	}

	data, err := marshalSessionEntry(map[string]any{
		"type":        "custom-title",
		"customTitle": stripped,
		"sessionId":   sessionID,
	})
	if err != nil {
		return err
	}
	return appendToSession(sessionID, data, directory)
}

// TagSession tags a session, or clears the tag when tag is empty.
//
// It appends a {"type":"tag",...} JSONL entry. ListSessions reads the last
// tag from the file tail, so the most recent wins; an empty tag appends an
// empty-string entry that ListSessions treats as cleared.
//
// Tags are Unicode-sanitized before storing — zero-width characters,
// directional marks, and private-use characters are removed — for CLI filter
// compatibility. A non-empty tag that sanitizes away entirely is rejected.
//
// See TagSessionViaStore for the SessionStore-backed variant.
func TagSession(sessionID, tag, directory string) error {
	if validateUUID(sessionID) == "" {
		return NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}
	if tag != "" {
		sanitized := strings.TrimSpace(sanitizeUnicode(tag))
		if sanitized == "" {
			return NewValidationError(`tag must be non-empty (pass "" to clear)`)
		}
		tag = sanitized
	}

	data, err := marshalSessionEntry(map[string]any{
		"type":      "tag",
		"tag":       tag,
		"sessionId": sessionID,
	})
	if err != nil {
		return err
	}
	return appendToSession(sessionID, data, directory)
}

// DeleteSession deletes a session by removing its JSONL file and subagent
// transcripts.
//
// This is a hard delete: the <sessionID>.jsonl file is removed permanently,
// along with the sibling <sessionID>/ directory holding subagent
// transcripts, when present. For soft-delete semantics, tag the session and
// filter it out when listing instead.
//
// It returns an error wrapping ErrSessionNotFound when the session file
// cannot be found.
//
// See DeleteSessionViaStore for the SessionStore-backed variant.
func DeleteSession(sessionID, directory string) error {
	if validateUUID(sessionID) == "" {
		return NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}

	path, _ := findSessionFileWithDir(sessionID, directory)
	if path == "" {
		return sessionNotFoundError(sessionID, directory)
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sessionNotFoundError(sessionID, directory)
		}
		return err
	}

	// Subagent transcripts live in a sibling <sessionID>/ directory, which
	// is often absent.
	_ = os.RemoveAll(filepath.Join(filepath.Dir(path), sessionID))
	return nil
}

// ForkSessionResult is the result of a fork operation.
type ForkSessionResult struct {
	// SessionID is the UUID of the new forked session.
	SessionID string
}

// ForkSessionOptions configures ForkSession.
type ForkSessionOptions struct {
	// Directory is the project directory, with the same semantics as
	// ListSessionsOptions.Directory. When empty, every project directory is
	// searched for the session file.
	Directory string

	// UpToMessageID slices the transcript up to and including this message
	// UUID. When empty, the full transcript is copied.
	UpToMessageID string

	// Title is a custom title for the fork. When empty, it derives from the
	// original title with " (fork)" appended.
	Title string
}

// ForkSession forks a session into a new branch with fresh UUIDs.
//
// It copies transcript messages from the source session into a new session
// file, remapping every message UUID and preserving the parentUuid chain.
// Forked sessions start without undo history, since file-history snapshots
// are not copied.
//
// It returns an error wrapping ErrSessionNotFound when the source session
// cannot be found, and a ValidationError when the session has no messages to
// fork or UpToMessageID is not present in the transcript.
//
// See ForkSessionViaStore for the SessionStore-backed variant.
func ForkSession(sessionID string, opts ForkSessionOptions) (*ForkSessionResult, error) {
	if validateUUID(sessionID) == "" {
		return nil, NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}
	if opts.UpToMessageID != "" && validateUUID(opts.UpToMessageID) == "" {
		return nil, NewValidationError(fmt.Sprintf("invalid up-to message ID: %s", opts.UpToMessageID))
	}

	filePath, projectDir := findSessionFileWithDir(sessionID, opts.Directory)
	if filePath == "" {
		return nil, sessionNotFoundError(sessionID, opts.Directory)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, NewValidationError(fmt.Sprintf("session %s has no messages to fork", sessionID))
	}

	transcript, contentReplacements := parseForkTranscript(string(content), sessionID)

	deriveTitle := func() string {
		size := len(content)
		head := string(content[:min(size, liteReadBufSize)])
		tail := string(content[max(0, size-liteReadBufSize):])
		return firstNonEmpty(
			extractLastJSONStringField(tail, "customTitle"),
			extractLastJSONStringField(head, "customTitle"),
			extractLastJSONStringField(tail, "aiTitle"),
			extractLastJSONStringField(head, "aiTitle"),
			extractFirstPromptFromHead(head),
		)
	}

	forkedSessionID, lines, err := buildForkLines(
		transcript, contentReplacements, sessionID, opts.UpToMessageID, opts.Title, deriveTitle)
	if err != nil {
		return nil, err
	}

	forkPath := filepath.Join(projectDir, forkedSessionID+".jsonl")
	// O_EXCL so an existing file is never clobbered by a UUID collision.
	f, err := os.OpenFile(forkPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.WriteString(strings.Join(lines, "\n") + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	return &ForkSessionResult{SessionID: forkedSessionID}, nil
}

// buildForkLines is the core fork transform: it remaps UUIDs and produces
// serialized JSONL lines. It is shared by the filesystem and SessionStore
// paths, and each returned line is a compact JSON string with no trailing
// newline.
//
// deriveTitle is invoked only when no explicit title is given, so the disk
// path's head and tail byte scan and the store path's entry scan run only
// when needed.
func buildForkLines(
	transcript []map[string]any,
	contentReplacements []any,
	sessionID, upToMessageID, title string,
	deriveTitle func() string,
) (string, []string, error) {
	// Filter out sidechains, which are subagent sessions with separate
	// parentUuid graphs. Keep isMeta entries: they interleave in the main
	// chain.
	var filtered []map[string]any
	for _, e := range transcript {
		if isSidechain, _ := e["isSidechain"].(bool); !isSidechain {
			filtered = append(filtered, e)
		}
	}
	transcript = filtered

	if len(transcript) == 0 {
		return "", nil, NewValidationError(fmt.Sprintf("session %s has no messages to fork", sessionID))
	}

	if upToMessageID != "" {
		cutoff := -1
		for i, entry := range transcript {
			if stringFromAny(entry["uuid"]) == upToMessageID {
				cutoff = i
				break
			}
		}
		if cutoff == -1 {
			return "", nil, NewValidationError(
				fmt.Sprintf("message %s not found in session %s", upToMessageID, sessionID))
		}
		transcript = transcript[:cutoff+1]
	}

	// Include progress entries in the mapping, which the parentUuid chain
	// walk needs.
	uuidMapping := make(map[string]string, len(transcript))
	byUUID := make(map[string]map[string]any, len(transcript))
	for _, entry := range transcript {
		uid := stringFromAny(entry["uuid"])
		uuidMapping[uid] = newUUID()
		byUUID[uid] = entry
	}

	// Filter progress messages out of the written output. They are UI-only
	// chain links, not needed in a fresh fork.
	var writable []map[string]any
	for _, e := range transcript {
		if stringFromAny(e["type"]) != "progress" {
			writable = append(writable, e)
		}
	}
	if len(writable) == 0 {
		return "", nil, NewValidationError(fmt.Sprintf("session %s has no messages to fork", sessionID))
	}

	forkedSessionID := newUUID()
	now := isoNow()
	var lines []string

	for i, original := range writable {
		originalUUID := stringFromAny(original["uuid"])

		// Resolve the parent UUID, skipping progress ancestors.
		var newParentUUID any
		parentID := stringFromAny(original["parentUuid"])
		for parentID != "" {
			parent, ok := byUUID[parentID]
			if !ok {
				break
			}
			if stringFromAny(parent["type"]) != "progress" {
				if mapped, ok := uuidMapping[parentID]; ok {
					newParentUUID = mapped
				}
				break
			}
			parentID = stringFromAny(parent["parentUuid"])
		}

		// Only the last message gets a fresh timestamp, which is how resume
		// detects the leaf.
		timestamp := stringFromAny(original["timestamp"])
		if i == len(writable)-1 || timestamp == "" {
			timestamp = now
		}

		forked := make(map[string]any, len(original)+4)
		for k, v := range original {
			forked[k] = v
		}
		forked["uuid"] = uuidMapping[originalUUID]
		forked["parentUuid"] = newParentUUID
		forked["sessionId"] = forkedSessionID
		forked["timestamp"] = timestamp
		forked["isSidechain"] = false
		forked["forkedFrom"] = map[string]any{
			"sessionId":   sessionID,
			"messageUuid": originalUUID,
		}

		// Remap logicalParentUuid, the compact-boundary backpointer.
		if logicalParent, ok := original["logicalParentUuid"]; ok {
			if lp := stringFromAny(logicalParent); lp != "" {
				if mapped, ok := uuidMapping[lp]; ok {
					forked["logicalParentUuid"] = mapped
				} else {
					forked["logicalParentUuid"] = nil
				}
			}
		}

		// Drop fields that would leak state from the source session.
		for _, key := range []string{"teamName", "agentName", "slug", "sourceToolAssistantUUID"} {
			delete(forked, key)
		}

		encoded, err := marshalTypeFirst(SessionStoreEntry(forked))
		if err != nil {
			return "", nil, err
		}
		lines = append(lines, string(encoded))
	}

	// Re-emit the content-replacement entry, if any, under the fork's
	// session ID.
	if len(contentReplacements) > 0 {
		encoded, err := marshalTypeFirst(SessionStoreEntry{
			"type":         "content-replacement",
			"sessionId":    forkedSessionID,
			"replacements": contentReplacements,
			"uuid":         newUUID(),
			"timestamp":    now,
		})
		if err != nil {
			return "", nil, err
		}
		lines = append(lines, string(encoded))
	}

	// Title precedence: explicit, then the original custom title, then the
	// original AI title, then the first prompt. Derived titles get a
	// " (fork)" suffix. ListSessions reads the last custom title from the
	// tail, so this entry is what surfaces.
	forkTitle := strings.TrimSpace(title)
	if forkTitle == "" {
		derived := deriveTitle()
		if derived == "" {
			derived = "Forked session"
		}
		forkTitle = derived + " (fork)"
	}

	encoded, err := marshalTypeFirst(SessionStoreEntry{
		"type":        "custom-title",
		"sessionId":   forkedSessionID,
		"customTitle": forkTitle,
		"uuid":        newUUID(),
		"timestamp":   now,
	})
	if err != nil {
		return "", nil, err
	}
	lines = append(lines, string(encoded))

	return forkedSessionID, lines, nil
}

// parseForkTranscript parses JSONL content into transcript entries plus
// content-replacement records. It keeps only entries that have a uuid and a
// transcript message type, and collects content-replacement entries for
// re-emission in the fork.
func parseForkTranscript(content, sessionID string) ([]map[string]any, []any) {
	var transcript []map[string]any
	var contentReplacements []any

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		transcript, contentReplacements = collectForkEntry(entry, sessionID, transcript, contentReplacements)
	}
	return transcript, contentReplacements
}

// collectForkEntry routes one decoded entry into the transcript or the
// content-replacement list. It is shared by the filesystem and SessionStore
// fork paths.
func collectForkEntry(
	entry map[string]any,
	sessionID string,
	transcript []map[string]any,
	contentReplacements []any,
) ([]map[string]any, []any) {
	entryType := stringFromAny(entry["type"])

	if _, ok := forkTranscriptTypes[entryType]; ok {
		if _, hasUUID := entry["uuid"].(string); hasUUID {
			transcript = append(transcript, entry)
		}
		return transcript, contentReplacements
	}

	if entryType == "content-replacement" && stringFromAny(entry["sessionId"]) == sessionID {
		if replacements, ok := entry["replacements"].([]any); ok {
			contentReplacements = append(contentReplacements, replacements...)
		}
	}
	return transcript, contentReplacements
}

// deriveTitleFromEntries mirrors the disk path's head and tail title scan
// over already-parsed entries.
//
// Precedence matches extractLastJSONStringField: the last occurrence wins
// for both customTitle and aiTitle, customTitle beats aiTitle, and the first
// user prompt is the final fallback.
func deriveTitleFromEntries(raw []SessionStoreEntry) string {
	var custom, ai string
	for _, e := range raw {
		if ct := stringFromAny(e["customTitle"]); ct != "" {
			custom = ct
		}
		if at := stringFromAny(e["aiTitle"]); at != "" {
			ai = at
		}
	}
	if custom != "" {
		return custom
	}
	if ai != "" {
		return ai
	}
	// First-prompt fallback: reuse the head extractor over a re-serialized
	// JSONL string so skip patterns and truncation match the disk path.
	return extractFirstPromptFromHead(entriesToJSONL(raw))
}

// findSessionFileWithDir finds a session file and its containing project
// directory, returning empty strings when there is none. The fork operation
// needs the project directory so it can write the new file beside the
// source.
func findSessionFileWithDir(sessionID, directory string) (filePath, projectDir string) {
	fileName := sessionID + ".jsonl"

	tryDir := func(dir string) (string, string) {
		if found := statCandidate(dir, fileName); found != "" {
			return found, dir
		}
		return "", ""
	}

	if directory != "" {
		canonical := canonicalizePath(directory)

		if dir := findProjectDir(canonical); dir != "" {
			if path, pdir := tryDir(dir); path != "" {
				return path, pdir
			}
		}

		for _, wt := range getWorktreePaths(canonical) {
			if wt == canonical {
				continue
			}
			if dir := findProjectDir(wt); dir != "" {
				if path, pdir := tryDir(dir); path != "" {
					return path, pdir
				}
			}
		}
		return "", ""
	}

	root := projectsDir(nil)
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", ""
	}
	for _, entry := range entries {
		if path, pdir := tryDir(filepath.Join(root, entry.Name())); path != "" {
			return path, pdir
		}
	}
	return "", ""
}

// marshalSessionEntry encodes a session entry as a compact JSON line with a
// trailing newline.
func marshalSessionEntry(entry map[string]any) (string, error) {
	// Encode with "type" first: Go sorts map keys, but the CLI writes the
	// discriminator first and the lite parse scans for a {"type":"tag" line
	// prefix, so a sorted encoding would make the tag invisible to readers.
	encoded, err := marshalTypeFirst(SessionStoreEntry(entry))
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}

// appendToSession appends data to an existing session file.
//
// It tries candidate paths directly rather than checking for existence
// first, opening with O_APPEND and without O_CREATE so a missing file fails
// the open instead of racing a separate existence check.
func appendToSession(sessionID, data, directory string) error {
	fileName := sessionID + ".jsonl"

	if directory != "" {
		canonical := canonicalizePath(directory)

		// Try the exact or prefix-matched project directory first.
		if projectDir := findProjectDir(canonical); projectDir != "" {
			ok, err := tryAppend(filepath.Join(projectDir, fileName), data)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}

		// Worktree fallback, matching the read helpers: sessions may live
		// under a different worktree root.
		for _, wt := range getWorktreePaths(canonical) {
			if wt == canonical {
				continue // Already tried above.
			}
			if wtProjectDir := findProjectDir(wt); wtProjectDir != "" {
				ok, err := tryAppend(filepath.Join(wtProjectDir, fileName), data)
				if err != nil {
					return err
				}
				if ok {
					return nil
				}
			}
		}

		return sessionNotFoundError(sessionID, directory)
	}

	// No directory: search every project directory by trying each directly.
	root := projectsDir(nil)
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("%w: %s (no projects directory)", ErrSessionNotFound, sessionID)
	}
	for _, entry := range entries {
		ok, err := tryAppend(filepath.Join(root, entry.Name(), fileName), data)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return sessionNotFoundError(sessionID, "")
}

// tryAppend appends to a path, reporting whether the write happened.
//
// It opens with O_APPEND and without O_CREATE, so the open fails when the
// file does not exist and no separate existence check is needed. On POSIX,
// O_APPEND makes the kernel seek to EOF atomically on every write; on
// Windows it maps to FILE_APPEND_DATA, which is also atomic.
//
// It reports false without an error when the file does not exist or is
// zero-byte. A zero-byte .jsonl means "the session is not here, keep
// searching", a signal the read path already honors; without that guard the
// search would stop at an empty stub in one project directory while the real
// file lives in a worktree. Every other error is returned, so real write
// failures surface.
func tryAppend(path, data string) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		// A missing file, or a missing parent directory, both mean "the
		// session is not here" rather than a real failure.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	if fi.Size() == 0 {
		return false, nil
	}
	if _, err := f.WriteString(data); err != nil {
		return false, err
	}
	return true, nil
}

// sessionNotFoundError builds a not-found error naming the search scope.
func sessionNotFoundError(sessionID, directory string) error {
	if directory != "" {
		return fmt.Errorf("%w: %s in the project directory for %s", ErrSessionNotFound, sessionID, directory)
	}
	return fmt.Errorf("%w: %s in any project directory", ErrSessionNotFound, sessionID)
}

// unicodeStripRe removes dangerous Unicode characters by explicit range,
// matching the ranges the Python and TypeScript SDKs strip.
var unicodeStripRe = regexp.MustCompile("[" +
	"\u200b-\u200f" + // Zero-width spaces and LTR/RTL marks
	"\u202a-\u202e" + // Directional formatting characters
	"\u2066-\u2069" + // Directional isolates
	"\ufeff" + // Byte order mark
	"\ue000-\uf8ff" + // Basic Multilingual Plane private use
	"]")

// assignedCategories are the Unicode general-category aggregates covering
// every assigned code point. A rune in none of them is unassigned, which is
// category Cn — the standard library has no table for Cn, because unassigned
// code points appear in no table by construction.
var assignedCategories = []*unicode.RangeTable{
	unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z, unicode.C,
}

// isStrippedCategory reports whether a rune belongs to a category that tag
// sanitization removes: Cf (format), Co (private use), Cs (surrogate), or Cn
// (unassigned).
//
// These are the categories most readily abused for display spoofing and
// injection, and they match the set the Python and TypeScript SDKs strip.
func isStrippedCategory(r rune) bool {
	if unicode.In(r, unicode.Cf, unicode.Co, unicode.Cs) {
		return true
	}
	for _, table := range assignedCategories {
		if unicode.Is(table, r) {
			return false
		}
	}
	// In no assigned category, so Cn.
	return true
}

// sanitizeUnicodeMaxIterations bounds the normalize-and-strip loop. A single
// pass is not enough: NFKC can decompose a character into a sequence that
// includes a format character, and removing a character can let its
// neighbours compose differently on the next pass. The loop runs to a fixed
// point, and the bound keeps a pathological input from spinning.
const sanitizeUnicodeMaxIterations = 10

// sanitizeUnicode removes Unicode characters that are dangerous in a tag:
// format, private-use, surrogate, and unassigned code points, plus the
// explicit ranges above.
//
// It applies NFKC normalization and stripping repeatedly until the result
// stops changing, matching the Python SDK. NFKC folds compatibility
// characters onto their canonical equivalents, so visually confusable forms
// collapse to one representation and a tag cannot smuggle a lookalike past
// a CLI filter comparing plain strings.
func sanitizeUnicode(value string) string {
	current := value
	for i := 0; i < sanitizeUnicodeMaxIterations; i++ {
		previous := current

		current = norm.NFKC.String(current)

		var b strings.Builder
		b.Grow(len(current))
		for _, r := range current {
			if isStrippedCategory(r) {
				continue
			}
			b.WriteRune(r)
		}
		// The explicit ranges are redundant with the category checks above,
		// but are kept so the stripped set stays pinned to the same
		// characters the other SDKs name even if a category table shifts
		// between Unicode revisions.
		current = unicodeStripRe.ReplaceAllString(b.String(), "")

		if current == previous {
			break
		}
	}
	return current
}

// isoNow returns the current UTC time as an ISO-8601 string with a trailing
// Z, matching the CLI's transcript timestamp format.
func isoNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// newUUID returns a random RFC 4122 version 4 UUID.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on supported platforms; fall back to a
		// timestamp-seeded value rather than panicking in a helper.
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", time.Now().UnixNano(), time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4.
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10.
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RenameSessionViaStore renames a session by appending a custom-title entry
// to a SessionStore. It is the store-backed counterpart to RenameSession.
func RenameSessionViaStore(ctx context.Context, store SessionStore, sessionID, title, directory string) error {
	if validateUUID(sessionID) == "" {
		return NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}
	stripped := strings.TrimSpace(title)
	if stripped == "" {
		return NewValidationError("title must be non-empty")
	}

	key := SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID}
	return store.Append(ctx, key, []SessionStoreEntry{{
		"type":        "custom-title",
		"customTitle": stripped,
		"sessionId":   sessionID,
		"uuid":        newUUID(),
		"timestamp":   isoNow(),
	}})
}

// TagSessionViaStore tags a session by appending a tag entry to a
// SessionStore, or clears the tag when tag is empty. It is the store-backed
// counterpart to TagSession, and sanitizes tags the same way.
func TagSessionViaStore(ctx context.Context, store SessionStore, sessionID, tag, directory string) error {
	if validateUUID(sessionID) == "" {
		return NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}
	if tag != "" {
		sanitized := strings.TrimSpace(sanitizeUnicode(tag))
		if sanitized == "" {
			return NewValidationError(`tag must be non-empty (pass "" to clear)`)
		}
		tag = sanitized
	}

	key := SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID}
	return store.Append(ctx, key, []SessionStoreEntry{{
		"type":      "tag",
		"tag":       tag,
		"sessionId": sessionID,
		"uuid":      newUUID(),
		"timestamp": isoNow(),
	}})
}

// DeleteSessionViaStore deletes a session from a SessionStore. It is the
// store-backed counterpart to DeleteSession.
//
// When the store does not implement SessionDeleter, deletion is a no-op,
// which suits WORM and append-only backends and matches the SessionStore
// contract. Whether subagent transcripts under the session are also removed
// depends on the store's delete semantics: InMemorySessionStore cascades,
// but custom stores may not.
func DeleteSessionViaStore(ctx context.Context, store SessionStore, sessionID, directory string) error {
	if validateUUID(sessionID) == "" {
		return NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}
	deleter, ok := store.(SessionDeleter)
	if !ok {
		return nil
	}
	return deleter.Delete(ctx, SessionKey{
		ProjectKey: ProjectKeyForDirectory(directory),
		SessionID:  sessionID,
	})
}

// ForkSessionViaStore forks a session into a new branch with fresh UUIDs via
// a SessionStore. It is the store-backed counterpart to ForkSession.
//
// It runs the fork transform directly over the objects Load returns, with no
// JSONL round trip. A storage-layer copy such as an S3 CopyObject is not
// sufficient: the transform remaps every UUID, rewrites sessionId on each
// entry, and stamps forkedFrom, so the data must pass through this process
// once.
//
// ForkSessionOptions.Directory computes the project key rather than
// selecting a filesystem path.
func ForkSessionViaStore(
	ctx context.Context,
	store SessionStore,
	sessionID string,
	opts ForkSessionOptions,
) (*ForkSessionResult, error) {
	if validateUUID(sessionID) == "" {
		return nil, NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}
	if opts.UpToMessageID != "" && validateUUID(opts.UpToMessageID) == "" {
		return nil, NewValidationError(fmt.Sprintf("invalid up-to message ID: %s", opts.UpToMessageID))
	}

	projectKey := ProjectKeyForDirectory(opts.Directory)
	loaded, err := store.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	if len(loaded) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	// Partition into transcript entries and content-replacement records,
	// mirroring parseForkTranscript for the already-decoded path.
	var transcript []map[string]any
	var contentReplacements []any
	for _, entry := range loaded {
		transcript, contentReplacements = collectForkEntry(entry, sessionID, transcript, contentReplacements)
	}

	forkedSessionID, lines, err := buildForkLines(
		transcript, contentReplacements, sessionID, opts.UpToMessageID, opts.Title,
		func() string { return deriveTitleFromEntries(loaded) })
	if err != nil {
		return nil, err
	}

	// buildForkLines emits compact JSON strings; decode them back to objects
	// so the store receives the same shape the mirror path would deliver.
	entries := make([]SessionStoreEntry, 0, len(lines))
	for _, line := range lines {
		var entry SessionStoreEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	dstKey := SessionKey{ProjectKey: projectKey, SessionID: forkedSessionID}
	if err := store.Append(ctx, dstKey, entries); err != nil {
		return nil, err
	}
	return &ForkSessionResult{SessionID: forkedSessionID}, nil
}
