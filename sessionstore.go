package agentsdk

import "context"

// SessionKey identifies a session transcript, or a subagent transcript, in a
// store. Main transcripts have no Subpath; subagent transcripts carry one
// like "subagents/agent-{id}" that mirrors the on-disk directory structure.
type SessionKey struct {
	// ProjectKey is a caller-defined scope, defaulting to the sanitized
	// working directory. Multi-tenant deployments should set it to a tenant
	// ID or project name. Paths longer than 200 characters are truncated
	// and suffixed with a portable djb2 hash, so the same path yields the
	// same key across runtimes.
	ProjectKey string `json:"project_key"`

	SessionID string `json:"session_id"`

	// Subpath is empty for the main transcript and set for subagent files.
	// It is opaque to the adapter, which should use it as a storage key
	// suffix.
	Subpath string `json:"subpath,omitempty"`
}

// SessionStoreEntry is one JSONL transcript line as a SessionStore adapter
// observes it.
//
// The concrete shape is the CLI's on-disk transcript format, a large
// discriminated union that is internal to the CLI. Adapters should treat
// entries as pass-through blobs; round-tripping through JSON is the only
// required invariant. Every entry has a "type" key, and most also carry
// "uuid" and "timestamp".
type SessionStoreEntry map[string]any

// EntryType returns the entry's "type" field, or an empty string when it is
// absent or not a string.
func (e SessionStoreEntry) EntryType() string {
	s, _ := e["type"].(string)
	return s
}

// EntryUUID returns the entry's "uuid" field, or an empty string when it is
// absent or not a string. Adapters should treat a non-empty UUID as an
// idempotency key; entries without one (titles, tags, mode markers) should
// be appended without deduplication.
func (e SessionStoreEntry) EntryUUID() string {
	s, _ := e["uuid"].(string)
	return s
}

// SessionStoreListEntry is returned by SessionLister.ListSessions.
type SessionStoreListEntry struct {
	SessionID string `json:"session_id"`

	// Mtime is the last-modified time in Unix epoch milliseconds. Adapters
	// without a native modification time (Redis, for instance) must
	// maintain their own index.
	Mtime int64 `json:"mtime"`
}

// SessionSummaryEntry is an incrementally maintained session summary.
//
// Stores obtain it from FoldSessionSummary inside Append and persist it
// verbatim, then return the full set from
// SessionSummaryLister.ListSessionSummaries. Data is opaque SDK-owned state
// that stores must not interpret.
type SessionSummaryEntry struct {
	SessionID string `json:"session_id"`

	// Mtime is the storage write time of the sidecar, in Unix epoch
	// milliseconds. It must use the same clock source as the Mtime
	// SessionLister.ListSessions reports for this session — typically file
	// mtime, S3 LastModified, Postgres updated_at, or whatever native
	// timestamp the adapter surfaces.
	//
	// Do not derive it from entry ISO timestamps: adapters that write in
	// batches with any persist latency (that is, every real backend) would
	// report storage times strictly later than the last entry's timestamp,
	// making every sidecar look stale and defeating the fast-path
	// staleness check in ListSessionsFromStore. FoldSessionSummary
	// preserves whatever Mtime the caller passes in via prev and never
	// sets it itself, so stamp it after persisting.
	Mtime int64 `json:"mtime"`

	// Data is opaque SDK-owned summary state. Persist it verbatim.
	Data map[string]any `json:"data"`
}

// SessionListSubkeysKey is the key argument to
// SessionSubkeyLister.ListSubkeys. It has no subpath.
type SessionListSubkeysKey struct {
	ProjectKey string `json:"project_key"`
	SessionID  string `json:"session_id"`
}

// SessionStore mirrors session transcripts to external storage.
//
// The subprocess still writes to local disk — set CLAUDE_CONFIG_DIR to a
// temporary directory for an ephemeral local copy — and the adapter receives
// a secondary copy.
//
// The SDK never deletes from a store unless DeleteSessionViaStore is called
// on a store that implements SessionDeleter. Retention is the adapter's
// responsibility: implement TTL, object-storage lifecycle policies, or
// scheduled cleanup according to your compliance requirements. Local-disk
// transcripts under CLAUDE_CONFIG_DIR are swept by the CLI's existing
// cleanupPeriodDays setting, independently of the adapter.
//
// Only Append and Load are required. The optional capabilities live in the
// SessionLister, SessionSummaryLister, SessionDeleter, and
// SessionSubkeyLister interfaces; the SDK type-asserts for each before use,
// so an adapter implements only what it supports.
type SessionStore interface {
	// Append mirrors a batch of transcript entries. It is called after the
	// subprocess's local write succeeds, so durability is already
	// guaranteed locally.
	//
	// Batches arrive at roughly a 100ms cadence during active turns.
	// Entries are JSON-safe plain objects, one per line in the local JSONL
	// file. Within a single process, persist entries in append-call order;
	// across concurrent processes, order is by storage commit time, not
	// call time.
	//
	// Most entries carry a stable uuid that adapters should treat as an
	// idempotency key (upsert, or ignore duplicates). Entries without one
	// should be appended without deduplication. Errors are logged and the
	// subprocess continues unaffected: failed batches are retried (3
	// attempts total) with short backoff before being dropped and surfaced
	// as a MirrorErrorMessage. A context deadline is not retried, since
	// the in-flight call may still land.
	Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error

	// Load returns a full session for resume. It is called once, in the
	// SDK parent, before subprocess spawn. The result is materialized to a
	// temporary JSONL file, and the subprocess resumes from that file
	// using its existing resume code.
	//
	// Return a nil slice for a key that was never written; adapters that
	// cannot distinguish "never written" from "emptied" may return nil for
	// both. Returned entries must be deep-equal to what was appended.
	// Byte-equal serialization is not required — Postgres JSONB may
	// reorder object keys, for instance — since the SDK never hashes or
	// byte-compares entries.
	Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error)
}

// SessionLister is the optional SessionStore capability for listing the
// sessions in a project.
type SessionLister interface {
	// ListSessions returns session IDs and modification times for a
	// project key. Mtime is Unix epoch milliseconds. Result order is
	// unspecified: the SDK sorts by Mtime descending.
	ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error)
}

// SessionSummaryLister is the optional SessionStore capability for fetching
// every session summary in one call.
type SessionSummaryLister interface {
	// ListSessionSummaries returns incrementally maintained summaries for
	// all sessions in a project. Stores should maintain them via
	// FoldSessionSummary inside Append, skipping keys that have a Subpath
	// so subagent transcripts do not contribute to the main session's
	// summary. Results are scoped to one project key and exclude subpath
	// entries.
	//
	// When a store does not implement this, ListSessionsFromStore falls
	// back to ListSessions plus a Load per session.
	//
	// Stores that maintain summaries inside Append must serialize sidecar
	// writes if Append calls can race for the same session — wrap the
	// read-fold-write in a transaction or compare-and-swap, or hold a
	// per-session lock. FoldSessionSummary is pure; concurrency control is
	// the store's responsibility.
	ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionSummaryEntry, error)
}

// SessionDeleter is the optional SessionStore capability for deleting
// sessions. Omitting it makes deletion a no-op, which suits WORM and
// append-only backends such as object storage.
type SessionDeleter interface {
	// Delete removes a session. Deleting a main-transcript key (one with
	// no Subpath) must cascade to every subkey under that session so
	// subagent transcripts are not orphaned. A targeted delete with an
	// explicit Subpath removes only that one entry.
	Delete(ctx context.Context, key SessionKey) error
}

// SessionSubkeyLister is the optional SessionStore capability for
// discovering subagent transcripts. Omitting it makes resume materialize
// only the main transcript.
type SessionSubkeyLister interface {
	// ListSubkeys returns every subpath key under a session, such as
	// subagent transcripts. Resume uses it to discover and materialize all
	// subagent data.
	ListSubkeys(ctx context.Context, key SessionListSubkeysKey) ([]string, error)
}
