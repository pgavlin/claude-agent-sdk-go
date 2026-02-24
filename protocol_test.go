package agentsdk

import (
	"context"
	"encoding/json"
	"iter"
	"sync"
	"testing"
	"time"
)

// mockTransport is a test Transport that replays pre-programmed messages.
type mockTransport struct {
	mu       sync.Mutex
	messages []map[string]any
	written  []string
	ready    bool
	closed   bool
	inputEnd bool

	// onWrite is called when Write is called, allowing tests to
	// react to messages sent by the protocol layer.
	onWrite func(data string)
}

func newMockTransport(messages ...map[string]any) *mockTransport {
	return &mockTransport{
		messages: messages,
	}
}

func (m *mockTransport) Connect(ctx context.Context) error {
	m.ready = true
	return nil
}

func (m *mockTransport) Write(ctx context.Context, data string) error {
	m.mu.Lock()
	m.written = append(m.written, data)
	cb := m.onWrite
	m.mu.Unlock()
	if cb != nil {
		cb(data)
	}
	return nil
}

func (m *mockTransport) ReadMessages(ctx context.Context) iter.Seq2[map[string]any, error] {
	return func(yield func(map[string]any, error) bool) {
		for _, msg := range m.messages {
			if !yield(msg, nil) {
				return
			}
		}
	}
}

func (m *mockTransport) Close() error {
	m.closed = true
	m.ready = false
	return nil
}

func (m *mockTransport) EndInput() error {
	m.inputEnd = true
	return nil
}

func (m *mockTransport) IsReady() bool {
	return m.ready
}

func (m *mockTransport) addMessage(msg map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
}

func TestProtocol_MessageRouting(t *testing.T) {
	// Simulate: init response, then assistant message, then result
	mt := newMockTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// We need to respond to the initialize control request.
	// The protocol sends a control_request and expects a control_response.
	// Our mock transport needs to produce the response.
	var initRequestID string
	var extraMessages []map[string]any

	mt.onWrite = func(data string) {
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return
		}
		if msg["type"] == "control_request" {
			req, _ := msg["request"].(map[string]any)
			if req["subtype"] == "initialize" {
				initRequestID = msg["request_id"].(string)
			}
		}
	}

	// Pre-load messages that will be read by the protocol
	// We need a dynamic transport for this test pattern.
	// Let's use a channel-based approach instead.

	ct := newChanTransport()
	q := newProtocolQuery(ct, &ClaudeAgentOptions{})
	q.Start(ctx)

	// Send initialize request
	go func() {
		// Wait for initialize request to be written
		written := ct.waitForWrite(ctx)
		var msg map[string]any
		json.Unmarshal([]byte(written), &msg)

		reqID := msg["request_id"].(string)

		// Send init response
		ct.send(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": reqID,
				"response":   map[string]any{"version": "2.0.0"},
			},
		})

		// Send an assistant message
		ct.send(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"model": "claude-opus-4-1-20250805",
				"content": []any{
					map[string]any{"type": "text", "text": "Hello!"},
				},
			},
		})

		// Send a result
		ct.send(map[string]any{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(100),
			"duration_api_ms": float64(50),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-1",
		})

		// Close the transport
		ct.close()
	}()

	result, err := q.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if result["version"] != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %v", result["version"])
	}

	// Collect messages
	var msgs []Message
	for msg := range q.Messages() {
		msgs = append(msgs, msg)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	if _, ok := msgs[0].(*AssistantMessage); !ok {
		t.Errorf("expected AssistantMessage, got %T", msgs[0])
	}
	if _, ok := msgs[1].(*ResultMessage); !ok {
		t.Errorf("expected ResultMessage, got %T", msgs[1])
	}

	_ = initRequestID
	_ = extraMessages
}

func TestProtocol_CanUseTool(t *testing.T) {
	ct := newChanTransport()
	opts := &ClaudeAgentOptions{
		CanUseTool: func(ctx context.Context, toolName string, input map[string]any, permCtx ToolPermissionContext) (PermissionResult, error) {
			if toolName == "Bash" {
				return PermissionDeny{Message: "not allowed", Interrupt: false}, nil
			}
			return PermissionAllow{}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	q := newProtocolQuery(ct, opts)
	q.Start(ctx)

	go func() {
		// Respond to initialize
		written := ct.waitForWrite(ctx)
		var msg map[string]any
		json.Unmarshal([]byte(written), &msg)
		ct.send(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": msg["request_id"].(string),
				"response":   map[string]any{},
			},
		})

		// Send can_use_tool request
		ct.send(map[string]any{
			"type":       "control_request",
			"request_id": "cli-req-1",
			"request": map[string]any{
				"subtype":   "can_use_tool",
				"tool_name": "Bash",
				"input":     map[string]any{"command": "rm -rf /"},
			},
		})

		// Wait for the permission response
		resp := ct.waitForWrite(ctx)
		var respMsg map[string]any
		json.Unmarshal([]byte(resp), &respMsg)

		// Verify it's a deny
		respPayload := respMsg["response"].(map[string]any)
		innerResp := respPayload["response"].(map[string]any)
		if innerResp["behavior"] != "deny" {
			t.Errorf("expected deny, got %v", innerResp["behavior"])
		}
		if innerResp["message"] != "not allowed" {
			t.Errorf("expected 'not allowed', got %v", innerResp["message"])
		}

		ct.close()
	}()

	_, err := q.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Drain messages
	for range q.Messages() {
	}
}

func TestProtocol_HookCallback(t *testing.T) {
	hookCalled := false

	ct := newChanTransport()
	opts := &ClaudeAgentOptions{
		Hooks: map[HookEvent][]HookMatcher{
			HookEventPreToolUse: {
				{
					Hooks: []HookCallback{
						func(ctx context.Context, input HookInput, toolUseID string) (*HookOutput, error) {
							hookCalled = true
							cont := true
							return &HookOutput{Continue: &cont}, nil
						},
					},
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	q := newProtocolQuery(ct, opts)
	q.Start(ctx)

	go func() {
		// Respond to initialize
		written := ct.waitForWrite(ctx)
		var msg map[string]any
		json.Unmarshal([]byte(written), &msg)

		// Verify hooks config was sent
		req := msg["request"].(map[string]any)
		if req["hooks"] == nil {
			t.Error("expected hooks in initialize request")
		}

		ct.send(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": msg["request_id"].(string),
				"response":   map[string]any{},
			},
		})

		// Send hook callback request
		ct.send(map[string]any{
			"type":       "control_request",
			"request_id": "cli-req-2",
			"request": map[string]any{
				"subtype":     "hook_callback",
				"callback_id": "hook_0",
				"tool_use_id": "tu-1",
				"input": map[string]any{
					"hook_event_name": "PreToolUse",
					"session_id":      "sess-1",
					"transcript_path": "/tmp/transcript",
					"cwd":             "/tmp",
					"tool_name":       "Bash",
					"tool_input":      map[string]any{"command": "ls"},
					"tool_use_id":     "tu-1",
				},
			},
		})

		// Wait for response
		ct.waitForWrite(ctx)

		ct.close()
	}()

	_, err := q.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	for range q.Messages() {
	}

	if !hookCalled {
		t.Error("expected hook to be called")
	}
}

func TestParseHookInput_AllEvents(t *testing.T) {
	tests := []struct {
		name      string
		eventName string
		extra     map[string]any
	}{
		{"PreToolUse", "PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{}, "tool_use_id": "1"}},
		{"PostToolUse", "PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{}, "tool_response": "ok", "tool_use_id": "1"}},
		{"PostToolUseFailure", "PostToolUseFailure", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{}, "tool_use_id": "1", "error": "fail"}},
		{"UserPromptSubmit", "UserPromptSubmit", map[string]any{"prompt": "hello"}},
		{"Stop", "Stop", map[string]any{"stop_hook_active": true}},
		{"SubagentStop", "SubagentStop", map[string]any{"stop_hook_active": false, "agent_id": "a1", "agent_transcript_path": "/tmp", "agent_type": "task"}},
		{"PreCompact", "PreCompact", map[string]any{"trigger": "auto"}},
		{"Notification", "Notification", map[string]any{"message": "done", "notification_type": "info"}},
		{"SubagentStart", "SubagentStart", map[string]any{"agent_id": "a2", "agent_type": "explore"}},
		{"PermissionRequest", "PermissionRequest", map[string]any{"tool_name": "Write", "tool_input": map[string]any{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := map[string]any{
				"hook_event_name": tt.eventName,
				"session_id":      "sess",
				"transcript_path": "/tmp/t",
				"cwd":             "/tmp",
			}
			for k, v := range tt.extra {
				data[k] = v
			}

			input, err := parseHookInput(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if input.hookEventName() != HookEvent(tt.eventName) {
				t.Errorf("expected event %s, got %s", tt.eventName, input.hookEventName())
			}
			base := input.BaseFields()
			if base.SessionID != "sess" {
				t.Errorf("expected session_id 'sess', got %s", base.SessionID)
			}
		})
	}
}

func TestParsePermissionUpdate(t *testing.T) {
	data := map[string]any{
		"type":        "addRules",
		"behavior":    "allow",
		"destination": "session",
		"rules": []any{
			map[string]any{"toolName": "Bash", "ruleContent": "ls only"},
		},
	}

	update := parsePermissionUpdateFromMap(data)
	if update.Type != PermissionUpdateAddRules {
		t.Errorf("expected type addRules, got %s", update.Type)
	}
	if update.Behavior != PermissionBehaviorAllow {
		t.Errorf("expected behavior allow, got %s", update.Behavior)
	}
	if len(update.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(update.Rules))
	}
	if update.Rules[0].ToolName != "Bash" {
		t.Errorf("expected tool name 'Bash', got %s", update.Rules[0].ToolName)
	}
}

// chanTransport is a channel-based transport for testing interactive protocols.
type chanTransport struct {
	msgCh   chan map[string]any
	writeCh chan string
	ready   bool
	closed  bool
}

func newChanTransport() *chanTransport {
	return &chanTransport{
		msgCh:   make(chan map[string]any, 100),
		writeCh: make(chan string, 100),
	}
}

func (ct *chanTransport) Connect(ctx context.Context) error {
	ct.ready = true
	return nil
}

func (ct *chanTransport) Write(ctx context.Context, data string) error {
	ct.writeCh <- data
	return nil
}

func (ct *chanTransport) ReadMessages(ctx context.Context) iter.Seq2[map[string]any, error] {
	return func(yield func(map[string]any, error) bool) {
		for {
			select {
			case msg, ok := <-ct.msgCh:
				if !ok {
					return
				}
				if !yield(msg, nil) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}
}

func (ct *chanTransport) Close() error {
	ct.closed = true
	ct.ready = false
	return nil
}

func (ct *chanTransport) EndInput() error {
	return nil
}

func (ct *chanTransport) IsReady() bool {
	return ct.ready
}

func (ct *chanTransport) send(msg map[string]any) {
	ct.msgCh <- msg
}

func (ct *chanTransport) close() {
	close(ct.msgCh)
}

func (ct *chanTransport) waitForWrite(ctx context.Context) string {
	select {
	case data := <-ct.writeCh:
		return data
	case <-ctx.Done():
		return ""
	}
}
