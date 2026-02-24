package agentsdk

// Message is a sealed interface representing a message in the conversation.
// Discriminated by the "type" field in JSON.
type Message interface {
	messageType() string
}

// AssistantMessageError represents error types that can occur on assistant messages.
type AssistantMessageError string

const (
	// AssistantMessageErrorAuthFailed indicates an authentication failure.
	AssistantMessageErrorAuthFailed AssistantMessageError = "authentication_failed"
	// AssistantMessageErrorBilling indicates a billing error.
	AssistantMessageErrorBilling AssistantMessageError = "billing_error"
	// AssistantMessageErrorRateLimit indicates the request was rate limited.
	AssistantMessageErrorRateLimit AssistantMessageError = "rate_limit"
	// AssistantMessageErrorInvalidRequest indicates the request was invalid.
	AssistantMessageErrorInvalidRequest AssistantMessageError = "invalid_request"
	// AssistantMessageErrorServer indicates an internal server error.
	AssistantMessageErrorServer AssistantMessageError = "server_error"
	// AssistantMessageErrorUnknown indicates an unknown error.
	AssistantMessageErrorUnknown AssistantMessageError = "unknown"
)

// UserMessage represents a message from the user.
type UserMessage struct {
	Content         any            `json:"content"` // string | []ContentBlock
	UUID            string         `json:"uuid,omitempty"`
	ParentToolUseID string         `json:"parent_tool_use_id,omitempty"`
	ToolUseResult   map[string]any `json:"tool_use_result,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
}

func (UserMessage) messageType() string { return "user" }

// AssistantMessage represents a message from the assistant.
type AssistantMessage struct {
	Content         []ContentBlock        `json:"content"`
	Model           string                `json:"model"`
	ParentToolUseID string                `json:"parent_tool_use_id,omitempty"`
	Error           AssistantMessageError `json:"error,omitempty"`
}

func (AssistantMessage) messageType() string { return "assistant" }

// SystemMessage represents a system-level message.
type SystemMessage struct {
	Subtype string         `json:"subtype"`
	Data    map[string]any `json:"data"`
}

func (SystemMessage) messageType() string { return "system" }

// ResultMessage represents the final result of a conversation turn.
type ResultMessage struct {
	Subtype          string         `json:"subtype"`
	DurationMS       int            `json:"duration_ms"`
	DurationAPIMS    int            `json:"duration_api_ms"`
	IsError          bool           `json:"is_error"`
	NumTurns         int            `json:"num_turns"`
	SessionID        string         `json:"session_id"`
	TotalCostUSD     *float64       `json:"total_cost_usd,omitempty"`
	Usage            map[string]any `json:"usage,omitempty"`
	Result           string         `json:"result,omitempty"`
	StructuredOutput any            `json:"structured_output,omitempty"`
}

func (ResultMessage) messageType() string { return "result" }

// StreamEvent represents a raw Anthropic API stream event.
type StreamEvent struct {
	UUID            string         `json:"uuid"`
	SessionID       string         `json:"session_id"`
	Event           map[string]any `json:"event"`
	ParentToolUseID string         `json:"parent_tool_use_id,omitempty"`
}

func (StreamEvent) messageType() string { return "stream_event" }
