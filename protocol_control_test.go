package agentsdk

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// decodeControlRequest parses a control request the protocol layer wrote to
// the transport.
func decodeControlRequest(t *testing.T, line string) (requestID string, request map[string]any) {
	t.Helper()
	var frame map[string]any
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("control frame is not valid JSON: %v", err)
	}
	requestID, _ = frame["request_id"].(string)
	request, _ = frame["request"].(map[string]any)
	return requestID, request
}

// runControlMethod drives a control method against a channel transport,
// answering the request with a canned success response.
func runControlMethod(
	t *testing.T,
	opts *ClaudeAgentOptions,
	response map[string]any,
	invoke func(ctx context.Context, q *protocolQuery) error,
) map[string]any {
	t.Helper()

	ct := newChanTransport()
	q := newProtocolQuery(ct, opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q.Start(ctx)

	var (
		mu      sync.Mutex
		request map[string]any
	)

	go func() {
		select {
		case line := <-ct.writeCh:
			requestID, req := decodeControlRequest(t, line)
			mu.Lock()
			request = req
			mu.Unlock()
			ct.msgCh <- map[string]any{
				"type": "control_response",
				"response": map[string]any{
					"subtype":    "success",
					"request_id": requestID,
					"response":   response,
				},
			}
		case <-ctx.Done():
		}
	}()

	if err := invoke(ctx, q); err != nil {
		t.Fatalf("control method failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	return request
}

func TestProtocolInitializeCarriesSkillsAndFlags(t *testing.T) {
	exclude := true
	opts := &ClaudeAgentOptions{
		SystemPrompt: &SystemPromptPreset{
			Type: "preset", Preset: "claude_code", ExcludeDynamicSections: &exclude,
		},
		Skills:              []string{"pdf"},
		ForwardSubagentText: true,
	}

	request := runControlMethod(t, opts, map[string]any{}, func(ctx context.Context, q *protocolQuery) error {
		_, err := q.Initialize(ctx)
		return err
	})

	if request["subtype"] != "initialize" {
		t.Fatalf("expected an initialize request, got %v", request["subtype"])
	}
	if request["excludeDynamicSections"] != true {
		t.Errorf("expected excludeDynamicSections to be forwarded, got %v", request["excludeDynamicSections"])
	}
	if request["forwardSubagentText"] != true {
		t.Errorf("expected forwardSubagentText to be forwarded, got %v", request["forwardSubagentText"])
	}
	skills, ok := request["skills"].([]any)
	if !ok || len(skills) != 1 || skills[0] != "pdf" {
		t.Errorf("expected an explicit skill list, got %v", request["skills"])
	}
}

func TestProtocolInitializeOmitsSkillsForAll(t *testing.T) {
	// SkillsAll and an omitted value are equivalent on the wire, since
	// neither installs a filter.
	request := runControlMethod(t, &ClaudeAgentOptions{Skills: SkillsAll{}}, map[string]any{},
		func(ctx context.Context, q *protocolQuery) error {
			_, err := q.Initialize(ctx)
			return err
		})

	if _, present := request["skills"]; present {
		t.Errorf("expected no skills field for SkillsAll, got %v", request["skills"])
	}
}

func TestProtocolMcpControlMethods(t *testing.T) {
	request := runControlMethod(t, nil, map[string]any{}, func(ctx context.Context, q *protocolQuery) error {
		return q.ReconnectMcpServer(ctx, "my-server")
	})
	if request["subtype"] != "mcp_reconnect" || request["serverName"] != "my-server" {
		t.Errorf("unexpected reconnect request: %+v", request)
	}

	request = runControlMethod(t, nil, map[string]any{}, func(ctx context.Context, q *protocolQuery) error {
		return q.ToggleMcpServer(ctx, "my-server", false)
	})
	if request["subtype"] != "mcp_toggle" || request["serverName"] != "my-server" {
		t.Errorf("unexpected toggle request: %+v", request)
	}
	if request["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", request["enabled"])
	}

	request = runControlMethod(t, nil, map[string]any{}, func(ctx context.Context, q *protocolQuery) error {
		return q.StopTask(ctx, "task_1")
	})
	if request["subtype"] != "stop_task" || request["task_id"] != "task_1" {
		t.Errorf("unexpected stop task request: %+v", request)
	}
}

func TestProtocolGetContextUsage(t *testing.T) {
	var got map[string]any
	request := runControlMethod(t, nil,
		map[string]any{"totalTokens": float64(1234), "model": "claude-opus-4-7"},
		func(ctx context.Context, q *protocolQuery) error {
			resp, err := q.GetContextUsage(ctx)
			got = resp
			return err
		})

	if request["subtype"] != "get_context_usage" {
		t.Errorf("unexpected request: %+v", request)
	}
	if got["totalTokens"] != float64(1234) {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestDecodeControlResponseTypes(t *testing.T) {
	var status McpStatusResponse
	err := decodeControlResponse(map[string]any{
		"mcpServers": []any{
			map[string]any{
				"name": "srv", "status": "connected",
				"serverInfo": map[string]any{"name": "srv", "version": "1.0"},
				"tools":      []any{map[string]any{"name": "do_thing"}},
			},
		},
	}, &status)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(status.McpServers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(status.McpServers))
	}
	server := status.McpServers[0]
	if server.Status != McpServerConnected || server.ServerInfo == nil || server.ServerInfo.Version != "1.0" {
		t.Errorf("unexpected server status: %+v", server)
	}
	if len(server.Tools) != 1 || server.Tools[0].Name != "do_thing" {
		t.Errorf("unexpected tools: %+v", server.Tools)
	}

	var usage ContextUsageResponse
	err = decodeControlResponse(map[string]any{
		"totalTokens": float64(100), "maxTokens": float64(200), "percentage": 50.0,
		"model": "claude-opus-4-7", "isAutoCompactEnabled": true,
		"categories": []any{map[string]any{"name": "tools", "tokens": float64(40), "color": "blue"}},
	}, &usage)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if usage.TotalTokens != 100 || usage.Percentage != 50.0 || !usage.IsAutoCompactEnabled {
		t.Errorf("unexpected context usage: %+v", usage)
	}
	if len(usage.Categories) != 1 || usage.Categories[0].Name != "tools" {
		t.Errorf("unexpected categories: %+v", usage.Categories)
	}
}

func TestProtocolControlCancelAbandonsHandler(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	opts := &ClaudeAgentOptions{
		CanUseTool: func(ctx context.Context, name string, input map[string]any, c ToolPermissionContext) (PermissionResult, error) {
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return PermissionAllow{}, nil
			}
		},
	}

	ct := newChanTransport()
	q := newProtocolQuery(ct, opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q.Start(ctx)

	ct.msgCh <- map[string]any{
		"type": "control_request", "request_id": "req_1",
		"request": map[string]any{
			"subtype": "can_use_tool", "tool_name": "Bash",
			"input": map[string]any{"command": "ls"}, "tool_use_id": "toolu_1",
		},
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the permission handler never started")
	}

	// Cancelling must abandon the handler without writing a response.
	ct.msgCh <- map[string]any{"type": "control_cancel_request", "request_id": "req_1"}

	select {
	case line := <-ct.writeCh:
		t.Fatalf("expected no response for a cancelled request, got %s", line)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
}

func TestProtocolCanUseToolContextFields(t *testing.T) {
	got := make(chan ToolPermissionContext, 1)

	opts := &ClaudeAgentOptions{
		CanUseTool: func(ctx context.Context, name string, input map[string]any, c ToolPermissionContext) (PermissionResult, error) {
			got <- c
			return PermissionAllow{}, nil
		},
	}

	ct := newChanTransport()
	q := newProtocolQuery(ct, opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q.Start(ctx)

	ct.msgCh <- map[string]any{
		"type": "control_request", "request_id": "req_1",
		"request": map[string]any{
			"subtype": "can_use_tool", "tool_name": "Read",
			"input":           map[string]any{"file_path": "/etc/hosts"},
			"tool_use_id":     "toolu_9",
			"agent_id":        "agent-1",
			"blocked_path":    "/etc/hosts",
			"decision_reason": "outside the allowed directories",
			"title":           "Claude wants to read /etc/hosts",
			"display_name":    "Read file",
			"description":     "Reads a file outside the workspace",
		},
	}

	select {
	case permCtx := <-got:
		if permCtx.ToolUseID != "toolu_9" || permCtx.AgentID != "agent-1" {
			t.Errorf("unexpected identifiers: %+v", permCtx)
		}
		if permCtx.BlockedPath != "/etc/hosts" || permCtx.DecisionReason != "outside the allowed directories" {
			t.Errorf("unexpected decision context: %+v", permCtx)
		}
		if permCtx.Title == "" || permCtx.DisplayName != "Read file" || permCtx.Description == "" {
			t.Errorf("unexpected prompt fields: %+v", permCtx)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the permission callback was never invoked")
	}

	// Drain the response so the transport does not block.
	select {
	case <-ct.writeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected a control response")
	}
}

func TestProtocolPeelsTranscriptMirrorFrames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	store := &recordingStore{}

	ct := newChanTransport()
	q := newProtocolQuery(ct, &ClaudeAgentOptions{})
	batcher := newTranscriptMirrorBatcher(store, root, SessionStoreFlushBatched, nil)
	q.setMirrorBatcher(batcher)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q.Start(ctx)

	ct.msgCh <- map[string]any{
		"type":     "transcript_mirror",
		"filePath": filepath.Join(root, "proj", testSessionID+".jsonl"),
		"entries":  []any{map[string]any{"type": "user", "uuid": testUUIDA}},
	}
	// A result flushes the batcher before it is yielded, so a consumer
	// observing the result can rely on the store being up to date.
	ct.msgCh <- map[string]any{
		"type": "result", "subtype": "success", "session_id": testSessionID,
		"duration_ms": float64(1), "duration_api_ms": float64(1),
		"is_error": false, "num_turns": float64(1),
	}

	select {
	case msg := <-q.Messages():
		if _, ok := msg.(*ResultMessage); !ok {
			t.Fatalf("expected a ResultMessage, got %T", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result message arrived")
	}

	// The mirror frame must not surface as a message.
	select {
	case msg := <-q.Messages():
		t.Fatalf("expected mirror frames to be peeled off, got %T", msg)
	case <-time.After(200 * time.Millisecond):
	}

	appends := store.recorded()
	if len(appends) != 1 {
		t.Fatalf("expected the frame to be flushed before the result, got %d appends", len(appends))
	}
	if appends[0].key.SessionID != testSessionID {
		t.Errorf("unexpected key: %+v", appends[0].key)
	}
}

func TestProtocolReportMirrorError(t *testing.T) {
	ct := newChanTransport()
	q := newProtocolQuery(ct, &ClaudeAgentOptions{})

	key := &SessionKey{ProjectKey: "proj", SessionID: testSessionID}
	q.reportMirrorError(key, "adapter unavailable")

	select {
	case msg := <-q.Messages():
		mirrorErr, ok := msg.(*MirrorErrorMessage)
		if !ok {
			t.Fatalf("expected a MirrorErrorMessage, got %T", msg)
		}
		if mirrorErr.Error != "adapter unavailable" {
			t.Errorf("unexpected error text: %q", mirrorErr.Error)
		}
		if mirrorErr.Key == nil || mirrorErr.Key.SessionID != testSessionID {
			t.Errorf("unexpected key: %+v", mirrorErr.Key)
		}
		if mirrorErr.Subtype != "mirror_error" {
			t.Errorf("expected the embedded subtype to be set, got %q", mirrorErr.Subtype)
		}
	case <-time.After(time.Second):
		t.Fatal("no mirror error message was emitted")
	}
}

func TestProtocolReportMirrorErrorDoesNotBlock(t *testing.T) {
	ct := newChanTransport()
	q := newProtocolQuery(ct, &ClaudeAgentOptions{})

	// Fill the buffer, then report once more: the extra report is dropped
	// rather than back-pressuring the read loop.
	for i := 0; i < messageChannelCap; i++ {
		q.messages <- &SystemMessage{Subtype: "filler"}
	}

	done := make(chan struct{})
	go func() {
		q.reportMirrorError(nil, "dropped")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reportMirrorError blocked on a full buffer")
	}
}

func TestNewUUIDIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newUUID()
		if validateUUID(id) == "" {
			t.Fatalf("newUUID produced an invalid UUID: %q", id)
		}
		// Version 4, variant 10.
		if id[14] != '4' {
			t.Errorf("expected a version 4 UUID, got %q", id)
		}
		if !strings.ContainsRune("89ab", rune(id[19])) {
			t.Errorf("expected an RFC 4122 variant, got %q", id)
		}
		if seen[id] {
			t.Fatalf("newUUID produced a duplicate: %q", id)
		}
		seen[id] = true
	}
}
