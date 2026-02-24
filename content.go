package agentsdk

// ContentBlock is a sealed interface representing a block of content
// within a message. Discriminated by the "type" field in JSON.
type ContentBlock interface {
	contentBlockType() string
}

// TextBlock represents a text content block.
type TextBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

func (TextBlock) contentBlockType() string { return "text" }

// ThinkingBlock represents a thinking/reasoning content block.
type ThinkingBlock struct {
	Type      string `json:"type"` // "thinking"
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

func (ThinkingBlock) contentBlockType() string { return "thinking" }

// ToolUseBlock represents a tool invocation content block.
type ToolUseBlock struct {
	Type  string         `json:"type"` // "tool_use"
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func (ToolUseBlock) contentBlockType() string { return "tool_use" }

// ToolResultBlock represents the result of a tool invocation.
type ToolResultBlock struct {
	Type      string `json:"type"` // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   any    `json:"content,omitempty"` // string | []map[string]any | nil
	IsError   *bool  `json:"is_error,omitempty"`
}

func (ToolResultBlock) contentBlockType() string { return "tool_result" }
