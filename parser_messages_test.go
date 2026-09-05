package agentsdk

import "testing"

func TestParseServerToolBlocks(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model": "claude-opus-4-7",
			"content": []any{
				map[string]any{
					"type":  "server_tool_use",
					"id":    "srvtoolu_1",
					"name":  "web_search",
					"input": map[string]any{"query": "go generics"},
				},
				map[string]any{
					"type":        "advisor_tool_result",
					"tool_use_id": "srvtoolu_1",
					"content":     map[string]any{"type": "advisor_result", "text": "ok"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}

	assistant, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("expected an AssistantMessage, got %T", msg)
	}
	if len(assistant.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(assistant.Content))
	}

	use, ok := assistant.Content[0].(ServerToolUseBlock)
	if !ok {
		t.Fatalf("expected a ServerToolUseBlock, got %T", assistant.Content[0])
	}
	if use.Name != ServerToolWebSearch || use.ID != "srvtoolu_1" {
		t.Errorf("unexpected server tool use block: %+v", use)
	}
	if use.Input["query"] != "go generics" {
		t.Errorf("unexpected input: %+v", use.Input)
	}

	result, ok := assistant.Content[1].(ServerToolResultBlock)
	if !ok {
		t.Fatalf("expected a ServerToolResultBlock, got %T", assistant.Content[1])
	}
	if result.ToolUseID != "srvtoolu_1" || result.Content["text"] != "ok" {
		t.Errorf("unexpected server tool result block: %+v", result)
	}
}

func TestParseAssistantMessageExtraFields(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type":       "assistant",
		"uuid":       testUUIDA,
		"session_id": testSessionID,
		"message": map[string]any{
			"id":          "msg_123",
			"model":       "claude-opus-4-7",
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": float64(10)},
			"content":     []any{map[string]any{"type": "text", "text": "hi"}},
		},
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}

	assistant := msg.(*AssistantMessage)
	if assistant.MessageID != "msg_123" {
		t.Errorf("unexpected message ID: %q", assistant.MessageID)
	}
	if assistant.StopReason != "end_turn" {
		t.Errorf("unexpected stop reason: %q", assistant.StopReason)
	}
	if assistant.SessionID != testSessionID || assistant.UUID != testUUIDA {
		t.Errorf("unexpected identifiers: %+v", assistant)
	}
	if assistant.Usage["input_tokens"] != float64(10) {
		t.Errorf("unexpected usage: %+v", assistant.Usage)
	}
}

func TestParseTaskMessages(t *testing.T) {
	started, err := parseMessage(map[string]any{
		"type": "system", "subtype": "task_started",
		"task_id": "task_1", "description": "build", "uuid": testUUIDA,
		"session_id": testSessionID, "tool_use_id": "toolu_1", "task_type": "general",
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}
	ts, ok := started.(*TaskStartedMessage)
	if !ok {
		t.Fatalf("expected a TaskStartedMessage, got %T", started)
	}
	if ts.TaskID != "task_1" || ts.Description != "build" || ts.TaskType != "general" {
		t.Errorf("unexpected task started message: %+v", ts)
	}
	// The embedded SystemMessage keeps the raw payload reachable.
	if ts.Subtype != "task_started" || ts.Data["task_id"] != "task_1" {
		t.Errorf("expected the embedded SystemMessage to carry the raw payload: %+v", ts.SystemMessage)
	}

	progress, err := parseMessage(map[string]any{
		"type": "system", "subtype": "task_progress",
		"task_id": "task_1", "description": "building", "uuid": testUUIDA,
		"session_id": testSessionID, "last_tool_name": "Bash",
		"usage": map[string]any{
			"total_tokens": float64(120), "tool_uses": float64(3), "duration_ms": float64(4500),
		},
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}
	tp := progress.(*TaskProgressMessage)
	if tp.Usage.TotalTokens != 120 || tp.Usage.ToolUses != 3 || tp.Usage.DurationMS != 4500 {
		t.Errorf("unexpected task usage: %+v", tp.Usage)
	}
	if tp.LastToolName != "Bash" {
		t.Errorf("unexpected last tool name: %q", tp.LastToolName)
	}

	notification, err := parseMessage(map[string]any{
		"type": "system", "subtype": "task_notification",
		"task_id": "task_1", "status": "completed", "output_file": "/tmp/out",
		"summary": "done", "uuid": testUUIDA, "session_id": testSessionID,
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}
	tn := notification.(*TaskNotificationMessage)
	if tn.Status != TaskNotificationCompleted || tn.OutputFile != "/tmp/out" || tn.Summary != "done" {
		t.Errorf("unexpected task notification: %+v", tn)
	}
	if tn.Usage != nil {
		t.Error("expected no usage when the payload omits it")
	}

	updated, err := parseMessage(map[string]any{
		"type": "system", "subtype": "task_updated",
		"task_id": "task_1", "session_id": testSessionID,
		"patch": map[string]any{"status": "killed", "end_time": "2026-01-01T00:00:00Z"},
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}
	tu := updated.(*TaskUpdatedMessage)
	if tu.Status != TaskUpdatedKilled {
		t.Errorf("expected the status to be read from the patch, got %q", tu.Status)
	}
	if tu.Patch["end_time"] != "2026-01-01T00:00:00Z" {
		t.Errorf("unexpected patch: %+v", tu.Patch)
	}
}

func TestIsTerminalTaskStatus(t *testing.T) {
	// Both lifecycle vocabularies must be recognized: task_notification
	// reports "stopped" while task_updated reports the raw "killed".
	for _, terminal := range []string{"completed", "failed", "stopped", "killed"} {
		if !IsTerminalTaskStatus(terminal) {
			t.Errorf("expected %q to be terminal", terminal)
		}
	}
	for _, active := range []string{"pending", "running", "paused", ""} {
		if IsTerminalTaskStatus(active) {
			t.Errorf("expected %q not to be terminal", active)
		}
	}
}

func TestParseHookEventMessage(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type": "system", "subtype": "hook_response",
		"hook_event": "PreToolUse", "session_id": testSessionID, "uuid": testUUIDA,
		"exit_code": float64(0),
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}
	hook, ok := msg.(*HookEventMessage)
	if !ok {
		t.Fatalf("expected a HookEventMessage, got %T", msg)
	}
	if hook.HookEventName != "PreToolUse" || hook.Subtype != "hook_response" {
		t.Errorf("unexpected hook event message: %+v", hook)
	}
}

func TestParseUnknownSystemSubtypeStaysSystemMessage(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type": "system", "subtype": "something_new", "field": "value",
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}
	sys, ok := msg.(*SystemMessage)
	if !ok {
		t.Fatalf("expected a plain SystemMessage for an unknown subtype, got %T", msg)
	}
	if sys.Subtype != "something_new" || sys.Data["field"] != "value" {
		t.Errorf("unexpected system message: %+v", sys)
	}
}

func TestParseRateLimitEvent(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type": "rate_limit_event", "uuid": testUUIDA, "session_id": testSessionID,
		"rate_limit_info": map[string]any{
			"status":        "allowed_warning",
			"resetsAt":      float64(1767225600),
			"rateLimitType": "five_hour",
			"utilization":   0.85,
		},
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}

	event, ok := msg.(*RateLimitEvent)
	if !ok {
		t.Fatalf("expected a RateLimitEvent, got %T", msg)
	}
	info := event.RateLimitInfo
	if info.Status != RateLimitAllowedWarning {
		t.Errorf("unexpected status: %q", info.Status)
	}
	if info.RateLimitType != RateLimitFiveHour {
		t.Errorf("unexpected rate limit type: %q", info.RateLimitType)
	}
	if info.ResetsAt == nil || *info.ResetsAt != 1767225600 {
		t.Errorf("unexpected reset time: %v", info.ResetsAt)
	}
	if info.Utilization == nil || *info.Utilization != 0.85 {
		t.Errorf("unexpected utilization: %v", info.Utilization)
	}
	if info.Raw["status"] != "allowed_warning" {
		t.Error("expected the raw payload to be preserved")
	}
}

func TestParseRateLimitEventMissingFields(t *testing.T) {
	if _, err := parseMessage(map[string]any{
		"type": "rate_limit_event", "uuid": testUUIDA, "session_id": testSessionID,
	}); err == nil {
		t.Error("expected an error when rate_limit_info is missing")
	}
	if _, err := parseMessage(map[string]any{
		"type": "rate_limit_event", "session_id": testSessionID,
		"rate_limit_info": map[string]any{"status": "allowed"},
	}); err == nil {
		t.Error("expected an error when uuid is missing")
	}
}

func TestParseConversationReset(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type": "conversation_reset", "new_conversation_id": "conv_1",
		"uuid": testUUIDA, "session_id": testSessionID,
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}
	reset, ok := msg.(*ConversationResetMessage)
	if !ok {
		t.Fatalf("expected a ConversationResetMessage, got %T", msg)
	}
	if reset.NewConversationID != "conv_1" {
		t.Errorf("unexpected conversation ID: %q", reset.NewConversationID)
	}
}

func TestParseResultMessageExtraFields(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type": "result", "subtype": "success",
		"duration_ms": float64(1000), "duration_api_ms": float64(800),
		"is_error": true, "num_turns": float64(2), "session_id": testSessionID,
		"uuid": testUUIDA, "stop_reason": "end_turn",
		"terminal_reason": "api_error", "api_error_status": float64(429),
		"errors": []any{"overloaded", "  ", 42},
		"modelUsage": map[string]any{
			"claude-opus-4-7": map[string]any{
				"inputTokens": float64(100), "outputTokens": float64(50),
				"costUSD": 0.42, "canonicalModel": "claude-opus-4-7", "provider": "firstParty",
			},
		},
		"permission_denials": []any{map[string]any{"tool": "Bash"}},
		"deferred_tool_use": map[string]any{
			"id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "ls"},
		},
		"origin": map[string]any{"kind": "task-notification", "subkind": "scheduled-trigger"},
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}

	result := msg.(*ResultMessage)
	if result.StopReason != "end_turn" || result.TerminalReason != "api_error" {
		t.Errorf("unexpected reasons: %+v", result)
	}
	if result.APIErrorStatus != 429 {
		t.Errorf("unexpected API error status: %d", result.APIErrorStatus)
	}
	// Blank and non-string error entries are dropped.
	if len(result.Errors) != 1 || result.Errors[0] != "overloaded" {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	usage, ok := result.ModelUsage["claude-opus-4-7"]
	if !ok {
		t.Fatal("expected per-model usage")
	}
	if usage.InputTokens != 100 || usage.CostUSD != 0.42 || usage.Provider != "firstParty" {
		t.Errorf("unexpected model usage: %+v", usage)
	}
	if len(result.PermissionDenials) != 1 {
		t.Errorf("unexpected permission denials: %v", result.PermissionDenials)
	}
	if result.DeferredToolUse == nil || result.DeferredToolUse.Name != "Bash" {
		t.Errorf("unexpected deferred tool use: %+v", result.DeferredToolUse)
	}
	if result.Origin == nil || result.Origin.Kind != MessageOriginTaskNotification {
		t.Errorf("unexpected origin: %+v", result.Origin)
	}
	if result.Origin.Subkind != TaskNotificationOriginScheduledTrigger {
		t.Errorf("unexpected origin subkind: %q", result.Origin.Subkind)
	}
}

func TestParseUserMessageOrigin(t *testing.T) {
	msg, err := parseMessage(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": "hello"},
		"origin": map[string]any{
			"kind": "peer", "from": "session-abc", "name": "Alice", "body": "hello",
			"verifiedPeerPid": float64(1234),
		},
	})
	if err != nil {
		t.Fatalf("parseMessage failed: %v", err)
	}

	user := msg.(*UserMessage)
	if user.Origin == nil {
		t.Fatal("expected an origin")
	}
	if user.Origin.Kind != MessageOriginPeer || user.Origin.From != "session-abc" {
		t.Errorf("unexpected origin: %+v", user.Origin)
	}
	if user.Origin.Name != "Alice" || user.Origin.Body != "hello" {
		t.Errorf("unexpected peer fields: %+v", user.Origin)
	}
	if user.Origin.VerifiedPeerPid != 1234 {
		t.Errorf("unexpected verified pid: %d", user.Origin.VerifiedPeerPid)
	}

	// A message with no origin leaves the field nil.
	plain, _ := parseMessage(map[string]any{
		"type": "user", "message": map[string]any{"role": "user", "content": "hi"},
	})
	if plain.(*UserMessage).Origin != nil {
		t.Error("expected no origin when the CLI did not attribute the message")
	}
}
