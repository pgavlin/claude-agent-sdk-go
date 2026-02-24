package agentsdk

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestClient_EndToEnd(t *testing.T) {
	ct := newChanTransport()

	client := NewClientWithTransport(&ClaudeAgentOptions{}, ct)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// Handle initialize
		written := ct.waitForWrite(ctx)
		var msg map[string]any
		json.Unmarshal([]byte(written), &msg)

		ct.send(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": msg["request_id"].(string),
				"response":   map[string]any{"server_version": "2.0.0"},
			},
		})
	}()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	// Verify server info
	if client.ServerInfo()["server_version"] != "2.0.0" {
		t.Error("expected server_version '2.0.0'")
	}

	// Send a query and receive response
	go func() {
		// Wait for the user message
		written := ct.waitForWrite(ctx)
		var msg map[string]any
		json.Unmarshal([]byte(written), &msg)

		if msg["type"] != "user" {
			t.Errorf("expected user message, got %v", msg["type"])
		}

		// Send assistant response
		ct.send(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"model":   "claude-opus-4-1-20250805",
				"content": []any{map[string]any{"type": "text", "text": "Hi there!"}},
			},
		})

		// Send result
		ct.send(map[string]any{
			"type":            "result",
			"subtype":         "success",
			"duration_ms":     float64(200),
			"duration_api_ms": float64(100),
			"is_error":        false,
			"num_turns":       float64(1),
			"session_id":      "sess-1",
			"result":          "Hi there!",
		})
	}()

	if err := client.SendQuery(ctx, "Hello!", "default"); err != nil {
		t.Fatalf("SendQuery failed: %v", err)
	}

	// Use ReceiveResponse to get messages until result
	var msgs []Message
	for msg := range client.ReceiveResponse() {
		msgs = append(msgs, msg)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	am, ok := msgs[0].(*AssistantMessage)
	if !ok {
		t.Fatalf("expected *AssistantMessage, got %T", msgs[0])
	}
	if len(am.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(am.Content))
	}
	tb, ok := am.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", am.Content[0])
	}
	if tb.Text != "Hi there!" {
		t.Errorf("expected 'Hi there!', got %s", tb.Text)
	}

	rm, ok := msgs[1].(*ResultMessage)
	if !ok {
		t.Fatalf("expected *ResultMessage, got %T", msgs[1])
	}
	if rm.Result != "Hi there!" {
		t.Errorf("expected result 'Hi there!', got %s", rm.Result)
	}
}

func TestClient_NotConnected(t *testing.T) {
	client := NewClientWithTransport(&ClaudeAgentOptions{}, newChanTransport())

	ctx := context.Background()

	if err := client.SendQuery(ctx, "test", ""); err == nil {
		t.Error("expected error when not connected")
	}
	if err := client.Interrupt(ctx); err == nil {
		t.Error("expected error when not connected")
	}
	if err := client.SetPermissionMode(ctx, PermissionModeDefault); err == nil {
		t.Error("expected error when not connected")
	}
	if err := client.SetModel(ctx, "test"); err == nil {
		t.Error("expected error when not connected")
	}
	if err := client.RewindFiles(ctx, "msg-1"); err == nil {
		t.Error("expected error when not connected")
	}
	if _, err := client.McpStatus(ctx); err == nil {
		t.Error("expected error when not connected")
	}
}

func TestClient_CanUseTool_SetsPermissionPromptTool(t *testing.T) {
	opts := &ClaudeAgentOptions{
		CanUseTool: func(ctx context.Context, toolName string, input map[string]any, permCtx ToolPermissionContext) (PermissionResult, error) {
			return PermissionAllow{}, nil
		},
	}

	client := NewClientWithTransport(opts, newChanTransport())
	if opts.PermissionPromptToolName != "stdio" {
		t.Errorf("expected PermissionPromptToolName 'stdio', got %s", opts.PermissionPromptToolName)
	}
	_ = client
}

func TestNewClientWithTransport_NilOpts(t *testing.T) {
	// Should not panic with nil opts
	ct := newChanTransport()
	client := NewClientWithTransport(nil, ct)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}
