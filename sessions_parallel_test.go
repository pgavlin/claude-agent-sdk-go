package agentsdk

import (
	"fmt"
	"maps"
	"reflect"
	"testing"
)

// parallelToolTurn builds one assistant turn that makes n parallel tool
// calls, as the CLI writes it: one assistant entry per content block, all
// sharing the response's message.id and chained one after another, and one
// tool_result entry per call whose parentUuid is that call's own assistant
// entry. resultOrder is the order the results are written in. It returns the
// calls and the results, both in file order.
func parallelToolTurn(parent string, n int, resultOrder []int) (calls, results []map[string]any) {
	for i := 0; i < n; i++ {
		callParent := parent
		if i > 0 {
			callParent = calls[i-1]["uuid"].(string)
		}
		calls = append(calls, map[string]any{
			"type":       "assistant",
			"uuid":       fmt.Sprintf("call-%d", i),
			"parentUuid": callParent,
			"sessionId":  testSessionID,
			"timestamp":  fmt.Sprintf("2026-01-01T00:00:01.%03dZ", i),
			"message": map[string]any{
				"id":   "msg_parallel",
				"role": "assistant",
				"content": []any{map[string]any{
					"type": "tool_use", "id": fmt.Sprintf("toolu_%d", i), "name": "Read", "input": map[string]any{},
				}},
			},
		})
	}
	for position, i := range resultOrder {
		results = append(results, map[string]any{
			"type":                    "user",
			"uuid":                    fmt.Sprintf("result-%d", i),
			"parentUuid":              fmt.Sprintf("call-%d", i),
			"sessionId":               testSessionID,
			"sourceToolAssistantUUID": fmt.Sprintf("call-%d", i),
			"timestamp":               fmt.Sprintf("2026-01-01T00:00:02.%03dZ", position),
			"message": map[string]any{
				"role": "user",
				"content": []any{map[string]any{
					"type": "tool_result", "tool_use_id": fmt.Sprintf("toolu_%d", i), "content": "ok",
				}},
			},
		})
	}
	return calls, results
}

func chainEntries(parts ...any) []transcriptEntry {
	var entries []transcriptEntry
	for _, p := range parts {
		switch p := p.(type) {
		case map[string]any:
			entries = append(entries, p)
		case []map[string]any:
			entries = append(entries, p...)
		}
	}
	return entries
}

func chainUUIDs(chain []transcriptEntry) []string {
	uuids := make([]string, len(chain))
	for i, entry := range chain {
		uuids[i], _ = entry["uuid"].(string)
	}
	return uuids
}

func expectChain(t *testing.T, entries []transcriptEntry, want ...string) {
	t.Helper()
	if got := chainUUIDs(buildConversationChain(entries)); !reflect.DeepEqual(got, want) {
		t.Errorf("chain = %v, want %v", got, want)
	}
}

func bareEntry(typ, uuid, parent string) map[string]any {
	entry := map[string]any{"type": typ, "uuid": uuid}
	if parent != "" {
		entry["parentUuid"] = parent
	}
	return entry
}

func TestGetSessionMessagesParallelToolCalls(t *testing.T) {
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {0, 2, 1}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			root := withProjectsDir(t)
			calls, results := parallelToolTurn("prompt", 3, order)
			// The next response continues from the last result written.
			last := results[len(results)-1]["uuid"].(string)
			writeTranscript(t, root, "proj", testSessionID, chainEntries(
				userEntry("prompt", "", "read them"), calls, results, assistantEntry("answer", last, "done"),
			))

			messages := GetSessionMessages(testSessionID, "", 0, 0)
			if len(messages) != 8 {
				t.Fatalf("expected 8 messages, got %d", len(messages))
			}
			var types []string
			for _, m := range messages {
				types = append(types, m.Type)
			}
			want := []string{"user", "assistant", "assistant", "assistant", "user", "user", "user", "assistant"}
			if !reflect.DeepEqual(types, want) {
				t.Errorf("types = %v, want %v", types, want)
			}
			if messages[0].UUID != "prompt" || messages[7].UUID != "answer" {
				t.Errorf("unexpected ends: %q ... %q", messages[0].UUID, messages[7].UUID)
			}
			for i := 0; i < 3; i++ {
				if got, want := messages[1+i].UUID, fmt.Sprintf("call-%d", i); got != want {
					t.Errorf("message %d = %q, want %q", 1+i, got, want)
				}
			}
		})
	}
}

func TestGetSessionMessagesParallelToolCallsAsLastTurn(t *testing.T) {
	root := withProjectsDir(t)
	calls, results := parallelToolTurn("prompt", 2, []int{1, 0})
	writeTranscript(t, root, "proj", testSessionID, chainEntries(userEntry("prompt", "", "read them"), calls, results))

	var got []string
	for _, m := range GetSessionMessages(testSessionID, "", 0, 0) {
		got = append(got, m.UUID)
	}
	if want := []string{"prompt", "call-0", "call-1", "result-1", "result-0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
}

func TestGetSessionMessagesParallelToolCallsLimitOffset(t *testing.T) {
	root := withProjectsDir(t)
	calls, results := parallelToolTurn("prompt", 3, []int{0, 1, 2})
	writeTranscript(t, root, "proj", testSessionID, chainEntries(
		userEntry("prompt", "", "read them"), calls, results, assistantEntry("answer", "result-2", "k"),
	))

	var got []string
	for _, m := range GetSessionMessages(testSessionID, "", 3, 4) {
		got = append(got, m.UUID)
	}
	if want := []string{"result-0", "result-1", "result-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("page = %v, want %v", got, want)
	}
}

func TestBuildConversationChainRecoversSiblingCalls(t *testing.T) {
	calls, results := parallelToolTurn("prompt", 3, []int{1, 2, 0})
	expectChain(t, chainEntries(bareEntry("user", "prompt", ""), calls, results, bareEntry("assistant", "answer", "result-0")),
		"prompt", "call-0", "call-1", "call-2", "result-1", "result-2", "result-0", "answer")
}

func TestBuildConversationChainRecoveredResultsInCompletionOrder(t *testing.T) {
	// The second call finished first.
	calls, results := parallelToolTurn("prompt", 3, []int{1, 0, 2})
	expectChain(t, chainEntries(bareEntry("user", "prompt", ""), calls, results, bareEntry("assistant", "answer", "result-2")),
		"prompt", "call-0", "call-1", "call-2", "result-1", "result-0", "result-2", "answer")
}

func TestBuildConversationChainRecoveredCallsInTimestampOrder(t *testing.T) {
	calls, results := parallelToolTurn("prompt", 3, []int{1, 2, 0})
	// call-2 reached the file before call-1.
	chain := buildConversationChain(chainEntries(
		bareEntry("user", "prompt", ""), calls[0], calls[2], calls[1], results, bareEntry("assistant", "answer", "result-0"),
	))
	if got := chainUUIDs(chain)[1:4]; !reflect.DeepEqual(got, []string{"call-0", "call-1", "call-2"}) {
		t.Errorf("calls = %v", got)
	}
}

func TestBuildConversationChainSequentialToolCallsUnchanged(t *testing.T) {
	call := func(uuid, parent, id string) map[string]any {
		e := bareEntry("assistant", uuid, parent)
		e["message"] = map[string]any{"id": "msg_1", "content": []any{map[string]any{"type": "tool_use", "id": id}}}
		return e
	}
	result := func(uuid, parent, id string) map[string]any {
		e := bareEntry("user", uuid, parent)
		e["message"] = map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id}}}
		return e
	}
	expectChain(t, chainEntries(
		bareEntry("user", "prompt", ""),
		call("call-0", "prompt", "toolu_0"),
		result("result-0", "call-0", "toolu_0"),
		call("call-1", "result-0", "toolu_1"),
		result("result-1", "call-1", "toolu_1"),
	), "prompt", "call-0", "result-0", "call-1", "result-1")
}

func TestBuildConversationChainAbandonedBranchNotRecovered(t *testing.T) {
	calls, results := parallelToolTurn("prompt", 2, []int{0, 1})
	old := bareEntry("assistant", "old-call", "prompt")
	old["message"] = map[string]any{"id": "msg_abandoned", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_old"}}}
	oldResult := bareEntry("user", "old-result", "old-call")
	oldResult["message"] = map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_old"}}}
	expectChain(t, chainEntries(bareEntry("user", "prompt", ""), old, oldResult, calls, results),
		"prompt", "call-0", "call-1", "result-0", "result-1")
}

func TestBuildConversationChainRecoversResultWithParentOutsideTurn(t *testing.T) {
	calls, results := parallelToolTurn("prompt", 2, []int{0, 1})
	results[0]["parentUuid"] = "missing"
	delete(results[0], "sourceToolAssistantUUID")
	expectChain(t, chainEntries(bareEntry("user", "prompt", ""), calls, results, bareEntry("assistant", "answer", "result-1")),
		"prompt", "call-0", "call-1", "result-0", "result-1", "answer")
}

func TestBuildConversationChainDoesNotDuplicateAnsweredCall(t *testing.T) {
	calls, results := parallelToolTurn("prompt", 2, []int{0, 1})
	duplicate := maps.Clone(results[1])
	duplicate["uuid"] = "result-1-copy"
	duplicate["parentUuid"] = "elsewhere"
	delete(duplicate, "sourceToolAssistantUUID")
	expectChain(t, chainEntries(bareEntry("user", "prompt", ""), calls, results, duplicate, bareEntry("assistant", "answer", "result-1")),
		"prompt", "call-0", "call-1", "result-0", "result-1", "answer")
}

func TestBuildConversationChainDoesNotPullInAnotherAgentsResult(t *testing.T) {
	calls, results := parallelToolTurn("prompt", 2, []int{0, 1})
	results[0]["parentUuid"] = "sidechain-call"
	results[0]["isSidechain"] = true
	results[0]["agentId"] = "agent-1"
	delete(results[0], "sourceToolAssistantUUID")
	expectChain(t, chainEntries(bareEntry("user", "prompt", ""), calls, results, bareEntry("assistant", "answer", "result-1")),
		"prompt", "call-0", "call-1", "result-1", "answer")
}

func TestBuildConversationChainRecoveryToleratesMalformedEntries(t *testing.T) {
	calls, results := parallelToolTurn("prompt", 2, []int{0, 1})
	results[0]["timestamp"] = 12345
	odd := bareEntry("assistant", "odd", "no-message")
	odd["message"] = map[string]any{"id": 7, "content": []any{"text", nil, map[string]any{"type": "tool_use"}}}
	oddResult := bareEntry("user", "odd-result", "odd")
	oddResult["message"] = map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": 3}}}
	expectChain(t, chainEntries(
		bareEntry("user", "prompt", ""), calls, results, bareEntry("assistant", "no-message", "result-1"), odd, oddResult,
	), "prompt", "call-0", "call-1", "result-0", "result-1", "no-message", "odd", "odd-result")
}
