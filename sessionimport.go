package agentsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImportSessionOptions configures ImportSessionToStore.
type ImportSessionOptions struct {
	// Directory is the project directory, with the same semantics as
	// ListSessionsOptions.Directory. When empty, every project directory is
	// searched for the session file.
	Directory string

	// ExcludeSubagents skips subagent transcripts under
	// <sessionID>/subagents/** and their .meta.json sidecars. They are
	// imported by default.
	ExcludeSubagents bool

	// BatchSize caps the entries per Append call. Zero selects the default
	// of maxPendingMirrorEntries.
	BatchSize int
}

// ImportSessionToStore replays a local session transcript into a
// SessionStore.
//
// It is the inverse of MaterializeResumeSession: where that reads a store
// and writes a temporary ~/.claude tree, this reads the local
// ~/.claude/projects/<dir>/<sessionID>.jsonl, plus subagent transcripts, and
// replays each line into Append.
//
// The on-disk JSONL is streamed line by line, calling Append every BatchSize
// entries or 1 MiB of line bytes, whichever comes first. This is useful for
// migrating existing local sessions to a remote store, or for catching a
// store up after a MirrorErrorMessage reported a live-mirror gap. Adapters
// should treat an entry's uuid as an idempotency key, which makes re-import
// duplicate-safe.
//
// The destination project key is the name of the on-disk project directory
// the session file was found in — the same key the transcript mirror batcher
// would produce for that file — so an imported session is indistinguishable
// from a live-mirrored one, and is resumable from the original working
// directory.
//
// It returns an error wrapping ErrSessionNotFound when the session JSONL
// cannot be found on disk.
func ImportSessionToStore(
	ctx context.Context,
	sessionID string,
	store SessionStore,
	opts ImportSessionOptions,
) error {
	if validateUUID(sessionID) == "" {
		return NewValidationError(fmt.Sprintf("invalid session ID: %s", sessionID))
	}

	resolved := resolveSessionFilePath(sessionID, opts.Directory)
	if resolved == "" {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	// Key under the on-disk project directory name, matching what the
	// mirror batcher produces, even when the resolver's search or worktree
	// fallback found the file somewhere other than Directory.
	projectKey := filepath.Base(filepath.Dir(resolved))

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = maxPendingMirrorEntries
	}

	mainKey := SessionKey{ProjectKey: projectKey, SessionID: sessionID}
	if err := appendJSONLFileInBatches(ctx, resolved, mainKey, store, batchSize); err != nil {
		return err
	}

	if opts.ExcludeSubagents {
		return nil
	}

	// Subagent transcripts live at <projectDir>/<sessionID>/subagents/**.
	sessionDir := strings.TrimSuffix(resolved, ".jsonl")
	for _, filePath := range collectJSONLFiles(filepath.Join(sessionDir, "subagents")) {
		rel, err := filepath.Rel(sessionDir, filePath)
		if err != nil {
			continue
		}
		// The subpath is the path relative to the session directory,
		// joined with "/" and without the .jsonl suffix — for example
		// subagents/agent-abc or subagents/workflows/run-1/agent-def. That
		// matches what the mirror batcher derives, so ListSubkeys and
		// GetSubagentMessagesFromStore round-trip.
		subpath := strings.TrimSuffix(filepath.ToSlash(rel), ".jsonl")

		subKey := SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}
		if err := appendJSONLFileInBatches(ctx, filePath, subKey, store, batchSize); err != nil {
			return err
		}

		// The on-disk .jsonl carries no agent_metadata entries: those are
		// only sent to live mirrors, and persisted in the .meta.json
		// sidecar. Import the sidecar so a later resume can recreate it and
		// resumed subagents keep their agent type and worktree path. A
		// missing, corrupt, or non-object sidecar is treated as absent, and
		// the transcript is still imported.
		if meta := readAgentMetadataSidecar(filePath); meta != nil {
			entry := make(SessionStoreEntry, len(meta)+1)
			for k, v := range meta {
				entry[k] = v
			}
			// The synthetic discriminator is set last, so a stray "type"
			// key in the CLI-owned sidecar can never shadow it.
			entry["type"] = "agent_metadata"
			if err := store.Append(ctx, subKey, []SessionStoreEntry{entry}); err != nil {
				return err
			}
		}
	}

	return nil
}

// appendJSONLFileInBatches streams a JSONL file line by line, parsing each
// line and flushing to Append in batches of batchSize entries, or
// maxPendingMirrorBytes of line text, whichever comes first. Blank lines are
// skipped.
func appendJSONLFileInBatches(
	ctx context.Context,
	filePath string,
	key SessionKey,
	store SessionStore,
	batchSize int,
) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var batch []SessionStoreEntry
	nbytes := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxPendingMirrorBytes)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry SessionStoreEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return NewJSONDecodeError(
				fmt.Sprintf("failed to parse transcript line in %s", filePath), line, err)
		}
		batch = append(batch, entry)
		nbytes += len(line)

		if len(batch) >= batchSize || nbytes >= maxPendingMirrorBytes {
			if err := store.Append(ctx, key, batch); err != nil {
				return err
			}
			batch = nil
			nbytes = 0
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	if len(batch) > 0 {
		return store.Append(ctx, key, batch)
	}
	return nil
}

// collectJSONLFiles recursively collects every .jsonl file under baseDir,
// returning nothing when baseDir does not exist. Entries are sorted per
// directory so import order is deterministic across platforms.
func collectJSONLFiles(baseDir string) []string {
	var results []string

	var walk func(dir string)
	walk = func(dir string) {
		// ReadDir returns entries sorted by filename.
		dirents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range dirents {
			path := filepath.Join(dir, entry.Name())
			switch {
			case entry.IsDir():
				walk(path)
			case strings.HasSuffix(entry.Name(), ".jsonl"):
				results = append(results, path)
			}
		}
	}
	walk(baseDir)

	return results
}
