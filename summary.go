package agentsdk

import "strings"

// lastWinsSummaryFields maps JSONL entry keys to summary data keys for
// last-wins string fields. Each appended entry overwrites the previous value
// when present.
var lastWinsSummaryFields = map[string]string{
	"customTitle": "custom_title",
	"aiTitle":     "ai_title",
	"lastPrompt":  "last_prompt",
	"summary":     "summary_hint",
	"gitBranch":   "git_branch",
}

// FoldSessionSummary folds a batch of appended entries into the running
// summary for a key.
//
// Stores call it from inside Append to keep a SessionSummaryEntry sidecar up
// to date without re-reading the transcript. prev is the previous summary
// for the same key, or nil for the first append.
//
// Do not call it for keys with a Subpath: subagent transcripts must not
// contribute to the main session's summary. Guard on key.Subpath == "".
//
// All derived state lives in the opaque Data map, which stores persist
// verbatim and do not interpret. Every derived field is append-incremental —
// set-once or last-wins — so adapters never need to re-read previously
// appended entries.
//
// Mtime is not touched by the fold: it is the sidecar's storage write time
// and must be stamped by the adapter after persisting, sharing a clock with
// the Mtime SessionLister.ListSessions reports for the same session.
// Deriving it from entry timestamps would make every batched-write sidecar
// appear strictly older than the session's current mtime, defeating the
// fast-path staleness check. For a new session the fold returns Mtime 0 as a
// placeholder, which the adapter is expected to overwrite.
//
// CreatedAt latches the first parseable entry timestamp. The on-disk lite
// parse scans the head buffer for the first timestamp, so the two paths
// agree for any timestamp appearing within the head window.
func FoldSessionSummary(prev *SessionSummaryEntry, key SessionKey, entries []SessionStoreEntry) SessionSummaryEntry {
	summary := SessionSummaryEntry{SessionID: key.SessionID, Data: map[string]any{}}
	if prev != nil {
		summary.SessionID = prev.SessionID
		summary.Mtime = prev.Mtime
		for k, v := range prev.Data {
			summary.Data[k] = v
		}
	}
	data := summary.Data

	for _, entry := range entries {
		ms := isoToEpochMS(stringFromAny(entry["timestamp"]))

		if _, ok := data["is_sidechain"]; !ok {
			isSidechain, _ := entry["isSidechain"].(bool)
			data["is_sidechain"] = isSidechain
		}
		if _, ok := data["created_at"]; !ok && ms != 0 {
			data["created_at"] = ms
		}
		if _, ok := data["cwd"]; !ok {
			if cwd := stringFromAny(entry["cwd"]); cwd != "" {
				data["cwd"] = cwd
			}
		}

		foldFirstPrompt(data, entry)

		for src, dst := range lastWinsSummaryFields {
			if val, ok := entry[src].(string); ok {
				data[dst] = val
			}
		}

		if entry.EntryType() == "tag" {
			if tagVal := stringFromAny(entry["tag"]); tagVal != "" {
				data["tag"] = tagVal
			} else {
				// An empty or absent tag clears the tag.
				delete(data, "tag")
			}
		}
	}

	return summary
}

// foldFirstPrompt replicates extractFirstPromptFromHead for a single parsed
// entry, mutating data in place.
//
// It sets first_prompt and first_prompt_locked on a real match, or stashes a
// command_fallback for slash-command messages. It skips tool_result, isMeta,
// isCompactSummary, and auto-generated patterns.
func foldFirstPrompt(data map[string]any, entry SessionStoreEntry) {
	if locked, _ := data["first_prompt_locked"].(bool); locked {
		return
	}
	if entry.EntryType() != "user" {
		return
	}
	if isMeta, _ := entry["isMeta"].(bool); isMeta {
		return
	}
	if isCompact, _ := entry["isCompactSummary"].(bool); isCompact {
		return
	}

	// Skip user messages that carry a tool_result.
	if message, ok := entry["message"].(map[string]any); ok {
		if content, ok := message["content"].([]any); ok {
			for _, raw := range content {
				if block, ok := raw.(map[string]any); ok && block["type"] == "tool_result" {
					return
				}
			}
		}
	}

	for _, raw := range entryTextBlocks(entry) {
		result := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
		if result == "" {
			continue
		}
		if m := commandNameRe.FindStringSubmatch(result); m != nil {
			if fallback, _ := data["command_fallback"].(string); fallback == "" {
				data["command_fallback"] = m[1]
			}
			continue
		}
		if skipFirstPromptPattern.MatchString(result) {
			continue
		}
		data["first_prompt"] = truncatePrompt(result)
		data["first_prompt_locked"] = true
		return
	}
}

// SummaryEntryToSDKInfo converts a SessionSummaryEntry into an
// SDKSessionInfo.
//
// It returns nil for sidechain sessions and for sessions with no extractable
// summary, matching how the on-disk lite parse filters them.
func SummaryEntryToSDKInfo(entry SessionSummaryEntry, projectPath string) *SDKSessionInfo {
	data := entry.Data
	if isSidechain, _ := data["is_sidechain"].(bool); isSidechain {
		return nil
	}

	firstPrompt := stringFromAny(data["command_fallback"])
	if locked, _ := data["first_prompt_locked"].(bool); locked {
		firstPrompt = stringFromAny(data["first_prompt"])
	}

	customTitle := firstNonEmpty(
		stringFromAny(data["custom_title"]),
		stringFromAny(data["ai_title"]),
	)
	summary := firstNonEmpty(
		customTitle,
		stringFromAny(data["last_prompt"]),
		stringFromAny(data["summary_hint"]),
		firstPrompt,
	)
	if summary == "" {
		return nil
	}

	var createdAt int64
	switch v := data["created_at"].(type) {
	case int64:
		createdAt = v
	case float64:
		createdAt = int64(v)
	case int:
		createdAt = int64(v)
	}

	return &SDKSessionInfo{
		SessionID:    entry.SessionID,
		Summary:      summary,
		LastModified: entry.Mtime,
		// FileSize is a JSONL byte count, meaningful only for the
		// local-disk path; stores have no equivalent.
		FileSize:    nil,
		CustomTitle: customTitle,
		FirstPrompt: firstPrompt,
		GitBranch:   stringFromAny(data["git_branch"]),
		CWD:         firstNonEmpty(stringFromAny(data["cwd"]), projectPath),
		Tag:         stringFromAny(data["tag"]),
		CreatedAt:   createdAt,
	}
}
