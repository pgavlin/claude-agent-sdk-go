package agentsdk

import (
	"context"
	"strings"
	"sync"
	"time"
)

// keyToString renders a SessionKey as the composite storage key
// InMemorySessionStore uses.
func keyToString(key SessionKey) string {
	parts := []string{key.ProjectKey, key.SessionID}
	if key.Subpath != "" {
		parts = append(parts, key.Subpath)
	}
	return strings.Join(parts, "/")
}

// summaryKey identifies a per-session summary sidecar.
type summaryKey struct {
	projectKey string
	sessionID  string
}

// InMemorySessionStore is an in-memory SessionStore for testing and
// development. It keeps entries in a map keyed by a composite
// projectKey/sessionID string, with an optional /subpath suffix.
//
// It implements every optional capability: SessionLister,
// SessionSummaryLister, SessionDeleter, and SessionSubkeyLister. It is not
// suitable for production, since all data is lost when the process exits.
//
// It is safe for concurrent use.
type InMemorySessionStore struct {
	mu        sync.Mutex
	store     map[string][]SessionStoreEntry
	mtimes    map[string]int64
	summaries map[summaryKey]SessionSummaryEntry
	order     []summaryKey
	lastMtime int64
}

// Compile-time assertions that the in-memory store implements every
// optional capability.
var (
	_ SessionStore         = (*InMemorySessionStore)(nil)
	_ SessionLister        = (*InMemorySessionStore)(nil)
	_ SessionSummaryLister = (*InMemorySessionStore)(nil)
	_ SessionDeleter       = (*InMemorySessionStore)(nil)
	_ SessionSubkeyLister  = (*InMemorySessionStore)(nil)
)

// NewInMemorySessionStore creates an empty in-memory store.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		store:     map[string][]SessionStoreEntry{},
		mtimes:    map[string]int64{},
		summaries: map[summaryKey]SessionSummaryEntry{},
	}
}

// nextMtime returns this adapter's storage write time in Unix epoch
// milliseconds.
//
// It is strictly monotonically increasing across calls within the process,
// so back-to-back appends always produce distinct mtimes. Real storage
// backends — file mtime on modern filesystems, S3 LastModified, Postgres
// updated_at — get that property for free from their commit ordering.
//
// The caller must hold mu.
func (s *InMemorySessionStore) nextMtime() int64 {
	nowMS := time.Now().UnixMilli()
	if nowMS <= s.lastMtime {
		nowMS = s.lastMtime + 1
	}
	s.lastMtime = nowMS
	return nowMS
}

// Append implements SessionStore.
func (s *InMemorySessionStore) Append(_ context.Context, key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := keyToString(key)
	s.store[k] = append(s.store[k], entries...)
	nowMS := s.nextMtime()

	// Maintain the per-session summary sidecar incrementally so
	// ListSessionSummaries never re-reads. Subagent subpaths do not
	// contribute to the main session's summary.
	if key.Subpath == "" {
		sk := summaryKey{projectKey: key.ProjectKey, sessionID: key.SessionID}
		prev, ok := s.summaries[sk]
		var prevPtr *SessionSummaryEntry
		if ok {
			prevPtr = &prev
		} else {
			s.order = append(s.order, sk)
		}
		folded := FoldSessionSummary(prevPtr, key, entries)
		// Stamp the sidecar with this adapter's storage write time, the
		// same clock ListSessions exposes below, so the fast-path
		// staleness check works.
		folded.Mtime = nowMS
		s.summaries[sk] = folded
	}

	s.mtimes[k] = nowMS
	return nil
}

// Load implements SessionStore. It returns nil for a key that was never
// written.
func (s *InMemorySessionStore) Load(_ context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, ok := s.store[keyToString(key)]
	if !ok {
		return nil, nil
	}
	return append([]SessionStoreEntry(nil), entries...), nil
}

// ListSessions implements SessionLister.
func (s *InMemorySessionStore) ListSessions(_ context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []SessionStoreListEntry
	prefix := projectKey + "/"
	for k := range s.store {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		// Include main transcripts only: a subpath adds a second slash.
		if strings.Contains(rest, "/") {
			continue
		}
		results = append(results, SessionStoreListEntry{SessionID: rest, Mtime: s.mtimes[k]})
	}
	return results, nil
}

// ListSessionSummaries implements SessionSummaryLister.
func (s *InMemorySessionStore) ListSessionSummaries(_ context.Context, projectKey string) ([]SessionSummaryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []SessionSummaryEntry
	for _, sk := range s.order {
		if sk.projectKey != projectKey {
			continue
		}
		if summary, ok := s.summaries[sk]; ok {
			results = append(results, summary)
		}
	}
	return results, nil
}

// Delete implements SessionDeleter.
func (s *InMemorySessionStore) Delete(_ context.Context, key SessionKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := keyToString(key)
	delete(s.store, k)
	delete(s.mtimes, k)

	// Deleting the main transcript cascades to its subkeys — subagent
	// transcripts and metadata — so they are not orphaned. A targeted
	// delete with an explicit subpath removes only that one entry.
	if key.Subpath == "" {
		sk := summaryKey{projectKey: key.ProjectKey, sessionID: key.SessionID}
		delete(s.summaries, sk)
		for i, existing := range s.order {
			if existing == sk {
				s.order = append(s.order[:i:i], s.order[i+1:]...)
				break
			}
		}

		prefix := key.ProjectKey + "/" + key.SessionID + "/"
		for storeKey := range s.store {
			if strings.HasPrefix(storeKey, prefix) {
				delete(s.store, storeKey)
				delete(s.mtimes, storeKey)
			}
		}
	}
	return nil
}

// ListSubkeys implements SessionSubkeyLister.
func (s *InMemorySessionStore) ListSubkeys(_ context.Context, key SessionListSubkeysKey) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prefix := key.ProjectKey + "/" + key.SessionID + "/"
	var subkeys []string
	for k := range s.store {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			subkeys = append(subkeys, rest)
		}
	}
	return subkeys, nil
}

// Entries returns every entry stored for a key, or an empty slice when there
// is none. It is a test helper.
func (s *InMemorySessionStore) Entries(key SessionKey) []SessionStoreEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SessionStoreEntry{}, s.store[keyToString(key)]...)
}

// Size returns the number of stored sessions, counting main transcripts
// only. It is a test helper.
func (s *InMemorySessionStore) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for k := range s.store {
		if firstSlash := strings.Index(k, "/"); firstSlash != -1 && !strings.Contains(k[firstSlash+1:], "/") {
			count++
		}
	}
	return count
}

// Clear removes all stored data. It is a test helper.
func (s *InMemorySessionStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.store = map[string][]SessionStoreEntry{}
	s.mtimes = map[string]int64{}
	s.summaries = map[summaryKey]SessionSummaryEntry{}
	s.order = nil
	s.lastMtime = 0
}
