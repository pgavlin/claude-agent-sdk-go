package agentsdk

import (
	"testing"
)

func TestParseMessage_UserMessage(t *testing.T) {
	data := map[string]any{
		"type":               "user",
		"uuid":               "user-123",
		"session_id":         "sess-1",
		"parent_tool_use_id": "tool-1",
		"message": map[string]any{
			"content": "Hello, Claude!",
		},
	}

	msg, err := parseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	um, ok := msg.(*UserMessage)
	if !ok {
		t.Fatalf("expected *UserMessage, got %T", msg)
	}

	if um.Content != "Hello, Claude!" {
		t.Errorf("expected content 'Hello, Claude!', got %v", um.Content)
	}
	if um.UUID != "user-123" {
		t.Errorf("expected uuid 'user-123', got %s", um.UUID)
	}
	if um.SessionID != "sess-1" {
		t.Errorf("expected session_id 'sess-1', got %s", um.SessionID)
	}
	if um.ParentToolUseID != "tool-1" {
		t.Errorf("expected parent_tool_use_id 'tool-1', got %s", um.ParentToolUseID)
	}
}

func TestParseMessage_UserMessageWithContentBlocks(t *testing.T) {
	data := map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "result"},
			},
		},
	}

	msg, err := parseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	um := msg.(*UserMessage)
	blocks, ok := um.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("expected []ContentBlock, got %T", um.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	if tb, ok := blocks[0].(TextBlock); !ok || tb.Text != "hello" {
		t.Errorf("expected TextBlock with 'hello', got %v", blocks[0])
	}
	if tr, ok := blocks[1].(ToolResultBlock); !ok || tr.ToolUseID != "t1" {
		t.Errorf("expected ToolResultBlock with tool_use_id 't1', got %v", blocks[1])
	}
}

func TestParseMessage_AssistantMessage(t *testing.T) {
	data := map[string]any{
		"type":  "assistant",
		"error": "rate_limit",
		"message": map[string]any{
			"model": "claude-opus-4-1-20250805",
			"content": []any{
				map[string]any{"type": "text", "text": "Hello!"},
				map[string]any{"type": "thinking", "thinking": "Let me think...", "signature": "sig123"},
				map[string]any{"type": "tool_use", "id": "tu1", "name": "Bash", "input": map[string]any{"command": "ls"}},
			},
		},
		"parent_tool_use_id": "parent-1",
	}

	msg, err := parseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	am, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("expected *AssistantMessage, got %T", msg)
	}

	if am.Model != "claude-opus-4-1-20250805" {
		t.Errorf("expected model 'claude-opus-4-1-20250805', got %s", am.Model)
	}
	if am.Error != AssistantMessageErrorRateLimit {
		t.Errorf("expected error 'rate_limit', got %s", am.Error)
	}
	if am.ParentToolUseID != "parent-1" {
		t.Errorf("expected parent_tool_use_id 'parent-1', got %s", am.ParentToolUseID)
	}
	if len(am.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(am.Content))
	}

	// Text block
	tb, ok := am.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", am.Content[0])
	}
	if tb.Text != "Hello!" {
		t.Errorf("expected 'Hello!', got %s", tb.Text)
	}

	// Thinking block
	thb, ok := am.Content[1].(ThinkingBlock)
	if !ok {
		t.Fatalf("expected ThinkingBlock, got %T", am.Content[1])
	}
	if thb.Thinking != "Let me think..." {
		t.Errorf("expected 'Let me think...', got %s", thb.Thinking)
	}
	if thb.Signature != "sig123" {
		t.Errorf("expected signature 'sig123', got %s", thb.Signature)
	}

	// Tool use block
	tub, ok := am.Content[2].(ToolUseBlock)
	if !ok {
		t.Fatalf("expected ToolUseBlock, got %T", am.Content[2])
	}
	if tub.Name != "Bash" {
		t.Errorf("expected tool name 'Bash', got %s", tub.Name)
	}
	if tub.Input["command"] != "ls" {
		t.Errorf("expected input command 'ls', got %v", tub.Input["command"])
	}
}

func TestParseMessage_AssistantMessageMissingMessage(t *testing.T) {
	data := map[string]any{
		"type": "assistant",
	}

	_, err := parseMessage(data)
	if err == nil {
		t.Fatal("expected error for missing message field")
	}
	if _, ok := err.(*MessageParseError); !ok {
		t.Fatalf("expected *MessageParseError, got %T", err)
	}
}

func TestParseMessage_SystemMessage(t *testing.T) {
	data := map[string]any{
		"type":    "system",
		"subtype": "start",
		"version": "2.0.0",
	}

	msg, err := parseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sm, ok := msg.(*SystemMessage)
	if !ok {
		t.Fatalf("expected *SystemMessage, got %T", msg)
	}
	if sm.Subtype != "start" {
		t.Errorf("expected subtype 'start', got %s", sm.Subtype)
	}
	if sm.Data["version"] != "2.0.0" {
		t.Errorf("expected version '2.0.0' in data")
	}
}

func TestParseMessage_ResultMessage(t *testing.T) {
	data := map[string]any{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1000),
		"duration_api_ms": float64(500),
		"is_error":        false,
		"num_turns":       float64(2),
		"session_id":      "sess-1",
		"total_cost_usd":  float64(0.01),
		"result":          "Done!",
		"usage": map[string]any{
			"input_tokens":  float64(100),
			"output_tokens": float64(50),
		},
	}

	msg, err := parseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rm, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("expected *ResultMessage, got %T", msg)
	}
	if rm.Subtype != "success" {
		t.Errorf("expected subtype 'success', got %s", rm.Subtype)
	}
	if rm.DurationMS != 1000 {
		t.Errorf("expected duration_ms 1000, got %d", rm.DurationMS)
	}
	if rm.DurationAPIMS != 500 {
		t.Errorf("expected duration_api_ms 500, got %d", rm.DurationAPIMS)
	}
	if rm.IsError {
		t.Error("expected is_error false")
	}
	if rm.NumTurns != 2 {
		t.Errorf("expected num_turns 2, got %d", rm.NumTurns)
	}
	if rm.TotalCostUSD == nil || *rm.TotalCostUSD != 0.01 {
		t.Errorf("expected total_cost_usd 0.01, got %v", rm.TotalCostUSD)
	}
	if rm.Result != "Done!" {
		t.Errorf("expected result 'Done!', got %s", rm.Result)
	}
}

func TestParseMessage_StreamEvent(t *testing.T) {
	data := map[string]any{
		"type":               "stream_event",
		"uuid":               "evt-1",
		"session_id":         "sess-1",
		"parent_tool_use_id": "tu-1",
		"event": map[string]any{
			"type": "content_block_delta",
		},
	}

	msg, err := parseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	se, ok := msg.(*StreamEvent)
	if !ok {
		t.Fatalf("expected *StreamEvent, got %T", msg)
	}
	if se.UUID != "evt-1" {
		t.Errorf("expected uuid 'evt-1', got %s", se.UUID)
	}
	if se.Event["type"] != "content_block_delta" {
		t.Errorf("expected event type 'content_block_delta'")
	}
}

func TestParseMessage_UnknownType(t *testing.T) {
	data := map[string]any{
		"type": "future_type",
		"data": "something",
	}

	msg, err := parseMessage(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil for unknown type, got %T", msg)
	}
}

func TestParseContentBlock_AllTypes(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		expected string // type string
	}{
		{
			name:     "text",
			data:     map[string]any{"type": "text", "text": "hello"},
			expected: "text",
		},
		{
			name:     "thinking",
			data:     map[string]any{"type": "thinking", "thinking": "hmm", "signature": "sig"},
			expected: "thinking",
		},
		{
			name:     "tool_use",
			data:     map[string]any{"type": "tool_use", "id": "1", "name": "Read", "input": map[string]any{}},
			expected: "tool_use",
		},
		{
			name:     "tool_result",
			data:     map[string]any{"type": "tool_result", "tool_use_id": "1", "content": "ok"},
			expected: "tool_result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block, err := parseContentBlock(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if block == nil {
				t.Fatal("expected non-nil block")
			}
			if block.contentBlockType() != tt.expected {
				t.Errorf("expected type %s, got %s", tt.expected, block.contentBlockType())
			}
		})
	}
}

func TestParseContentBlock_ToolResultWithIsError(t *testing.T) {
	data := map[string]any{
		"type":        "tool_result",
		"tool_use_id": "t1",
		"content":     "error occurred",
		"is_error":    true,
	}

	block, err := parseContentBlock(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tr := block.(ToolResultBlock)
	if tr.IsError == nil || !*tr.IsError {
		t.Error("expected is_error to be true")
	}
}

func TestParseContentBlock_UnknownType(t *testing.T) {
	data := map[string]any{
		"type": "future_block_type",
	}

	block, err := parseContentBlock(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block != nil {
		t.Fatal("expected nil for unknown block type")
	}
}

func TestIntFromAny(t *testing.T) {
	tests := []struct {
		input    any
		expected int
	}{
		{float64(42), 42},
		{42, 42},
		{"not a number", 0},
		{nil, 0},
	}

	for _, tt := range tests {
		result := intFromAny(tt.input)
		if result != tt.expected {
			t.Errorf("intFromAny(%v) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}
