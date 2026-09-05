package agentsdk

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// storeListLoadConcurrency bounds concurrent store Load calls issued by
// ListSessionsFromStore, so large project listings do not exhaust adapter
// connection pools or trip backend rate limits.
const storeListLoadConcurrency = 16

// entriesToJSONL serializes store entries to a JSONL string, one line per
// entry.
//
// The SessionStore.Load contract permits adapters to reorder object keys
// (Postgres JSONB, for instance), but parseSessionInfoFromLite scans for
// {"type":"tag" as a line prefix. Hoisting type to the front makes the store
// path match the byte shape the disk path produces.
func entriesToJSONL(entries []SessionStoreEntry) string {
	var b strings.Builder
	for _, entry := range entries {
		encoded, err := marshalTypeFirst(entry)
		if err != nil {
			continue
		}
		b.Write(encoded)
		b.WriteByte('\n')
	}
	return b.String()
}

// marshalTypeFirst encodes an entry with its "type" key first, so
// prefix-scanning matches the CLI's on-disk byte shape.
func marshalTypeFirst(entry SessionStoreEntry) ([]byte, error) {
	entryType, hasType := entry["type"]
	if !hasType {
		return json.Marshal(map[string]any(entry))
	}

	rest := make(map[string]any, len(entry))
	for k, v := range entry {
		if k != "type" {
			rest[k] = v
		}
	}

	typeJSON, err := json.Marshal(entryType)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString(`{"type":`)
	b.Write(typeJSON)
	if len(rest) > 0 {
		restJSON, err := json.Marshal(rest)
		if err != nil {
			return nil, err
		}
		// Splice the remaining object in, dropping its opening brace.
		b.WriteByte(',')
		b.Write(restJSON[1:])
		return []byte(b.String()), nil
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// jsonlToLite builds the head, tail, and size lite shape from an in-memory
// JSONL string. It matches readSessionLite's byte semantics, so the store
// path exposes the same slice to parseSessionInfoFromLite as the disk path
// would for the same transcript.
func jsonlToLite(jsonl string, mtime int64) *liteSessionFile {
	buf := []byte(jsonl)
	size := int64(len(buf))

	head := string(buf[:min(int(size), liteReadBufSize)])
	tail := head
	if size > liteReadBufSize {
		tail = string(buf[size-liteReadBufSize:])
	}

	return &liteSessionFile{mtime: mtime, size: size, head: head, tail: tail}
}

// mtimeFromJSONLTail derives a best-effort mtime by parsing the last entry's
// timestamp, falling back to the current wall-clock time when it is absent
// or unparseable.
func mtimeFromJSONLTail(jsonl string) int64 {
	trimmed := strings.TrimRight(jsonl, "\n \t\r")
	lastLine := trimmed[strings.LastIndex(trimmed, "\n")+1:]

	var obj map[string]any
	if err := json.Unmarshal([]byte(lastLine), &obj); err == nil {
		if ms := isoToEpochMS(stringFromAny(obj["timestamp"])); ms != 0 {
			return ms
		}
	}
	return time.Now().UnixMilli()
}

// filterTranscriptEntries filters store-loaded entries down to transcript
// message types that carry a uuid.
//
// It mirrors parseTranscriptEntries for the already-decoded path, so chain
// building never sees metadata-only entries such as custom-title, tag, or
// agent_metadata.
func filterTranscriptEntries(entries []SessionStoreEntry) []transcriptEntry {
	var result []transcriptEntry
	for _, e := range entries {
		if _, ok := transcriptEntryTypes[e.EntryType()]; !ok {
			continue
		}
		if _, ok := e["uuid"].(string); !ok {
			continue
		}
		result = append(result, transcriptEntry(e))
	}
	return result
}

// loadStoreEntriesAsJSONL loads a session's entries from a store and
// serializes them to JSONL, returning "" when the session has no entries.
func loadStoreEntriesAsJSONL(ctx context.Context, store SessionStore, sessionID, directory string) (string, error) {
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID}
	entries, err := store.Load(ctx, key)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	return entriesToJSONL(entries), nil
}

// storeSlot is one candidate row while assembling a store-backed listing.
// info is nil for a slot that still needs a gap-fill load.
type storeSlot struct {
	mtime     int64
	sessionID string
	info      *SDKSessionInfo
}

// ListSessionsFromStore lists sessions from a SessionStore, sorted by
// LastModified descending. It is the store-backed counterpart to
// ListSessions.
//
// It loads each session's entries to derive a real summary via the same lite
// parse the filesystem path uses, so disk and store paths produce identical
// results for the same transcript content. directory computes the project
// key and defaults to the process working directory. A limit of zero means
// no limit.
//
// The store must implement SessionSummaryLister or SessionLister, or both.
// With SessionSummaryLister this is one batch summary call plus one cheap
// SessionLister enumeration to gap-fill sessions missing a sidecar or whose
// sidecar is stale, so there are zero per-session Load calls when sidecars
// are complete and fresh. Otherwise it falls back to one Load per session,
// bounded at storeListLoadConcurrency concurrent calls, which on remote
// backends with many or large sessions can be expensive.
//
// Gap-fill requires SessionLister: a store implementing SessionSummaryLister
// but not SessionLister cannot discover sessions without a sidecar, and they
// will be absent from the result.
//
// Worktrees are a filesystem concept and are not honored here, since the
// store operates on a single project key.
func ListSessionsFromStore(
	ctx context.Context,
	store SessionStore,
	directory string,
	limit, offset int,
) ([]SDKSessionInfo, error) {
	projectPath := canonicalizePath(directoryOrCWD(directory))
	projectKey := sanitizePath(projectPath)

	lister, hasListSessions := store.(SessionLister)

	// Fast path: when the store maintains incremental summaries, fetch them
	// in one call instead of a load per session.
	if summaryLister, ok := store.(SessionSummaryLister); ok {
		summaries, err := summaryLister.ListSessionSummaries(ctx, projectKey)
		if err != nil {
			return nil, err
		}

		var listing []SessionStoreListEntry
		knownMtimes := map[string]int64{}
		if hasListSessions {
			listing, err = lister.ListSessions(ctx, projectKey)
			if err != nil {
				return nil, err
			}
			for _, e := range listing {
				knownMtimes[e.SessionID] = e.Mtime
			}
		} else {
			log.Print("agentsdk: SessionSummaryLister without SessionLister: gap-fill skipped, " +
				"so sessions lacking a sidecar will be omitted")
		}

		// Build a unified slot list. Fresh summaries get their info up
		// front; sessions the listing reports but whose sidecar is missing
		// or stale get a placeholder routed through gap-fill, so the fold
		// is recomputed from source entries.
		//
		// Summary-backed sidechain and no-summary sessions are dropped here
		// — free, since it is already determined — so they do not consume
		// offset or limit positions, matching the disk path's
		// filter-then-paginate semantics.
		var slots []storeSlot
		freshSummaryIDs := map[string]struct{}{}
		for _, s := range summaries {
			if hasListSessions {
				known, ok := knownMtimes[s.SessionID]
				if !ok {
					// A summary for a session the listing no longer
					// reports: drop it.
					continue
				}
				if s.Mtime < known {
					// A stale sidecar: let gap-fill re-fold from source.
					continue
				}
			}
			freshSummaryIDs[s.SessionID] = struct{}{}
			info := SummaryEntryToSDKInfo(s, projectPath)
			if info == nil {
				continue
			}
			slots = append(slots, storeSlot{mtime: s.Mtime, sessionID: s.SessionID, info: info})
		}
		if hasListSessions {
			for _, e := range listing {
				if _, fresh := freshSummaryIDs[e.SessionID]; fresh {
					continue
				}
				slots = append(slots, storeSlot{mtime: e.Mtime, sessionID: e.SessionID})
			}
		}

		// Paginate before the per-session load, so the gap-fill load count
		// is bounded by page size rather than by the total missing: 500
		// sessions lacking sidecars with a limit of 10 issues at most 10
		// loads, not 500.
		sort.SliceStable(slots, func(i, j int) bool { return slots[i].mtime > slots[j].mtime })
		page := slots
		if offset > 0 {
			if offset >= len(page) {
				page = nil
			} else {
				page = page[offset:]
			}
		}
		if limit > 0 && limit < len(page) {
			page = page[:limit]
		}

		var toFill []storeSlot
		for _, sl := range page {
			if sl.info == nil {
				toFill = append(toFill, sl)
			}
		}
		if len(toFill) > 0 {
			filled := deriveInfosViaLoad(ctx, store, toFill, directory, projectPath)
			bySID := make(map[string]*SDKSessionInfo, len(filled))
			for i := range filled {
				bySID[filled[i].SessionID] = &filled[i]
			}
			for i := range page {
				if page[i].info == nil {
					page[i].info = bySID[page[i].sessionID]
				}
			}
		}

		// Gap-fill placeholders that resolved to nothing — a sidechain, or
		// no extractable summary after load — are dropped here, after
		// pagination, so that case alone can short-page. Summary-backed
		// slots were pre-filtered above, so a store with complete and fresh
		// sidecars never short-pages.
		var results []SDKSessionInfo
		for _, sl := range page {
			if sl.info != nil {
				results = append(results, *sl.info)
			}
		}
		return results, nil
	}

	if !hasListSessions {
		return nil, NewValidationError(
			"session store implements neither SessionSummaryLister nor SessionLister, so sessions " +
				"cannot be listed; provide a store with at least one of them")
	}

	listing, err := lister.ListSessions(ctx, projectKey)
	if err != nil {
		return nil, err
	}

	slots := make([]storeSlot, 0, len(listing))
	for _, e := range listing {
		slots = append(slots, storeSlot{mtime: e.Mtime, sessionID: e.SessionID})
	}

	// Derive a real summary per session by loading its entries and reusing
	// the filesystem path's lite parse. Filtering happens before pagination
	// so limit and offset index the same filtered set as the disk path.
	results := deriveInfosViaLoad(ctx, store, slots, directory, projectPath)
	return applySortLimitOffset(results, limit, offset), nil
}

// deriveInfosViaLoad derives an SDKSessionInfo for each slot via a
// per-session store Load plus a lite parse.
//
// Loads run concurrently with a fixed bound so large listings do not exhaust
// adapter connection pools or hit backend rate limits. An adapter error
// degrades that row to an empty summary instead of failing the whole
// listing, and sidechain and no-summary sessions are dropped.
func deriveInfosViaLoad(
	ctx context.Context,
	store SessionStore,
	slots []storeSlot,
	directory, projectPath string,
) []SDKSessionInfo {
	type outcome struct {
		jsonl string
		err   error
	}
	settled := make([]outcome, len(slots))

	sem := make(chan struct{}, storeListLoadConcurrency)
	var wg sync.WaitGroup
	for i, sl := range slots {
		wg.Add(1)
		go func(i int, sessionID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			jsonl, err := loadStoreEntriesAsJSONL(ctx, store, sessionID, directory)
			settled[i] = outcome{jsonl: jsonl, err: err}
		}(i, sl.sessionID)
	}
	wg.Wait()

	var results []SDKSessionInfo
	for i, sl := range slots {
		out := settled[i]
		if out.err != nil {
			results = append(results, SDKSessionInfo{SessionID: sl.sessionID, LastModified: sl.mtime})
			continue
		}
		if out.jsonl == "" {
			continue
		}
		parsed := parseSessionInfoFromLite(sl.sessionID, jsonlToLite(out.jsonl, sl.mtime), projectPath)
		if parsed == nil {
			// A sidechain, or no extractable summary: drop it, matching
			// the filesystem path.
			continue
		}
		parsed.LastModified = sl.mtime
		results = append(results, *parsed)
	}
	return results
}

// GetSessionInfoFromStore reads metadata for a single session from a
// SessionStore. It is the store-backed counterpart to GetSessionInfo.
//
// It returns nil when the session is not found, sessionID is not a valid
// UUID, the session is a sidechain session, or it has no extractable
// summary.
func GetSessionInfoFromStore(
	ctx context.Context,
	store SessionStore,
	sessionID, directory string,
) (*SDKSessionInfo, error) {
	if validateUUID(sessionID) == "" {
		return nil, nil
	}
	jsonl, err := loadStoreEntriesAsJSONL(ctx, store, sessionID, directory)
	if err != nil {
		return nil, err
	}
	if jsonl == "" {
		return nil, nil
	}
	lite := jsonlToLite(jsonl, mtimeFromJSONLTail(jsonl))
	return parseSessionInfoFromLite(sessionID, lite, canonicalizePath(directoryOrCWD(directory))), nil
}

// GetSessionMessagesFromStore reads a session's conversation messages from a
// SessionStore. It is the store-backed counterpart to GetSessionMessages,
// feeding Load results straight into the chain builder with no JSONL round
// trip. A limit of zero means no limit.
func GetSessionMessagesFromStore(
	ctx context.Context,
	store SessionStore,
	sessionID, directory string,
	limit, offset int,
) ([]SessionMessage, error) {
	if validateUUID(sessionID) == "" {
		return nil, nil
	}
	key := SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID}
	entries, err := store.Load(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entriesToSessionMessages(filterTranscriptEntries(entries), limit, offset), nil
}

// ListSubagentsFromStore lists subagent IDs for a session from a
// SessionStore. It is the store-backed counterpart to ListSubagents, and
// requires the store to implement SessionSubkeyLister.
func ListSubagentsFromStore(
	ctx context.Context,
	store SessionStore,
	sessionID, directory string,
) ([]string, error) {
	if validateUUID(sessionID) == "" {
		return nil, nil
	}
	subkeyLister, ok := store.(SessionSubkeyLister)
	if !ok {
		return nil, NewValidationError(
			"session store does not implement SessionSubkeyLister, so subagents cannot be listed; " +
				"provide a store with a ListSubkeys method")
	}

	subkeys, err := subkeyLister.ListSubkeys(ctx, SessionListSubkeysKey{
		ProjectKey: ProjectKeyForDirectory(directory),
		SessionID:  sessionID,
	})
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	var ids []string
	for _, subpath := range subkeys {
		if !strings.HasPrefix(subpath, "subagents/") {
			continue
		}
		last := subpath[strings.LastIndex(subpath, "/")+1:]
		agentID, ok := strings.CutPrefix(last, "agent-")
		if !ok {
			continue
		}
		if _, dup := seen[agentID]; dup {
			continue
		}
		seen[agentID] = struct{}{}
		ids = append(ids, agentID)
	}
	return ids, nil
}

// GetSubagentMessagesFromStore reads a subagent's conversation messages from
// a SessionStore. It is the store-backed counterpart to GetSubagentMessages.
//
// Subagents may live at subagents/agent-<id> or nested under
// subagents/workflows/<runID>/agent-<id>. Subkeys are scanned when the store
// implements SessionSubkeyLister, and the direct path is tried otherwise.
// ParentToolUseID and ParentAgentID come from the subagent's agent_metadata
// entry in the store, and are empty when it is absent. A limit of zero means
// no limit.
func GetSubagentMessagesFromStore(
	ctx context.Context,
	store SessionStore,
	sessionID, agentID, directory string,
	limit, offset int,
) ([]SessionMessage, error) {
	if validateUUID(sessionID) == "" || agentID == "" {
		return nil, nil
	}
	projectKey := ProjectKeyForDirectory(directory)

	subpath := "subagents/agent-" + agentID
	if subkeyLister, ok := store.(SessionSubkeyLister); ok {
		subkeys, err := subkeyLister.ListSubkeys(ctx, SessionListSubkeysKey{
			ProjectKey: projectKey,
			SessionID:  sessionID,
		})
		if err != nil {
			return nil, err
		}
		target := "agent-" + agentID
		match := ""
		for _, sk := range subkeys {
			if strings.HasPrefix(sk, "subagents/") && sk[strings.LastIndex(sk, "/")+1:] == target {
				match = sk
				break
			}
		}
		if match == "" {
			return nil, nil
		}
		subpath = match
	}

	entries, err := store.Load(ctx, SessionKey{
		ProjectKey: projectKey,
		SessionID:  sessionID,
		Subpath:    subpath,
	})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	// The synthetic agent_metadata entry — the store's copy of the
	// .meta.json sidecar — records which Agent tool_use spawned this
	// subagent. Recover the parent IDs from it, last one winning since the
	// metadata is rewritten on resume, then drop it: it is not a transcript
	// line.
	metaEntry, transcript := splitAgentMetadata(entries)
	if len(transcript) == 0 {
		return nil, nil
	}
	parentToolUseID, parentAgentID := parentIDsFromAgentMetadata(metaEntry)

	return entriesToSubagentMessages(
		filterTranscriptEntries(transcript), limit, offset, parentToolUseID, parentAgentID), nil
}

// directoryOrCWD returns the directory, or "." when it is empty, matching
// the Python SDK's default of the current working directory.
func directoryOrCWD(directory string) string {
	if directory == "" {
		return "."
	}
	return directory
}
