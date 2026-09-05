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

// ServerToolName identifies a server-side tool executed by the API.
type ServerToolName string

const (
	// ServerToolAdvisor is the advisor server tool.
	ServerToolAdvisor ServerToolName = "advisor"
	// ServerToolWebSearch is the web search server tool.
	ServerToolWebSearch ServerToolName = "web_search"
	// ServerToolWebFetch is the web fetch server tool.
	ServerToolWebFetch ServerToolName = "web_fetch"
	// ServerToolCodeExecution is the code execution server tool.
	ServerToolCodeExecution ServerToolName = "code_execution"
	// ServerToolBashCodeExecution is the bash code execution server tool.
	ServerToolBashCodeExecution ServerToolName = "bash_code_execution"
	// ServerToolTextEditorCodeExecution is the text editor code execution server tool.
	ServerToolTextEditorCodeExecution ServerToolName = "text_editor_code_execution"
	// ServerToolSearchToolRegex is the regex tool-search server tool.
	ServerToolSearchToolRegex ServerToolName = "tool_search_tool_regex"
	// ServerToolSearchToolBM25 is the BM25 tool-search server tool.
	ServerToolSearchToolBM25 ServerToolName = "tool_search_tool_bm25"
)

// ServerToolUseBlock represents a server-side tool invocation, such as
// advisor, web_search, or web_fetch. The API executes these on the model's
// behalf, so they appear alongside regular tool_use blocks but the caller
// never returns a result. Branch on Name to identify the tool.
type ServerToolUseBlock struct {
	Type  string         `json:"type"` // "server_tool_use"
	ID    string         `json:"id"`
	Name  ServerToolName `json:"name"`
	Input map[string]any `json:"input"`
}

func (ServerToolUseBlock) contentBlockType() string { return "server_tool_use" }

// ServerToolResultBlock represents the result of a server-side tool call.
// Content is the raw dict from the API and is opaque to the SDK; callers
// that care about a specific tool's result schema can inspect its "type" key.
type ServerToolResultBlock struct {
	Type      string         `json:"type"` // "advisor_tool_result"
	ToolUseID string         `json:"tool_use_id"`
	Content   map[string]any `json:"content"`
}

func (ServerToolResultBlock) contentBlockType() string { return "advisor_tool_result" }
