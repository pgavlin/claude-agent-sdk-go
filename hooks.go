package agentsdk

import "context"

// HookEvent identifies the type of hook event.
type HookEvent string

const (
	// HookEventPreToolUse fires before a tool is used.
	HookEventPreToolUse HookEvent = "PreToolUse"
	// HookEventPostToolUse fires after a tool is used successfully.
	HookEventPostToolUse HookEvent = "PostToolUse"
	// HookEventPostToolUseFailure fires after a tool use fails.
	HookEventPostToolUseFailure HookEvent = "PostToolUseFailure"
	// HookEventUserPromptSubmit fires when the user submits a prompt.
	HookEventUserPromptSubmit HookEvent = "UserPromptSubmit"
	// HookEventStop fires when the conversation is stopping.
	HookEventStop HookEvent = "Stop"
	// HookEventSubagentStop fires when a subagent stops.
	HookEventSubagentStop HookEvent = "SubagentStop"
	// HookEventPreCompact fires before context compaction.
	HookEventPreCompact HookEvent = "PreCompact"
	// HookEventNotification fires when a notification is triggered.
	HookEventNotification HookEvent = "Notification"
	// HookEventSubagentStart fires when a subagent starts.
	HookEventSubagentStart HookEvent = "SubagentStart"
	// HookEventPermissionRequest fires when a permission request is made.
	HookEventPermissionRequest HookEvent = "PermissionRequest"
)

// BaseHookFields contains fields common to all hook inputs.
type BaseHookFields struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	PermissionMode string `json:"permission_mode,omitempty"`
}

// HookInput is a sealed interface for hook event inputs.
type HookInput interface {
	hookEventName() HookEvent
	BaseFields() BaseHookFields
}

// PreToolUseHookInput is sent before a tool is used.
type PreToolUseHookInput struct {
	BaseHookFields
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	ToolUseID string         `json:"tool_use_id"`
}

func (PreToolUseHookInput) hookEventName() HookEvent     { return HookEventPreToolUse }
func (h PreToolUseHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// PostToolUseHookInput is sent after a tool is used successfully.
type PostToolUseHookInput struct {
	BaseHookFields
	ToolName     string         `json:"tool_name"`
	ToolInput    map[string]any `json:"tool_input"`
	ToolResponse any            `json:"tool_response"`
	ToolUseID    string         `json:"tool_use_id"`
}

func (PostToolUseHookInput) hookEventName() HookEvent     { return HookEventPostToolUse }
func (h PostToolUseHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// PostToolUseFailureHookInput is sent after a tool use fails.
type PostToolUseFailureHookInput struct {
	BaseHookFields
	ToolName    string         `json:"tool_name"`
	ToolInput   map[string]any `json:"tool_input"`
	ToolUseID   string         `json:"tool_use_id"`
	Error       string         `json:"error"`
	IsInterrupt bool           `json:"is_interrupt,omitempty"`
}

func (PostToolUseFailureHookInput) hookEventName() HookEvent     { return HookEventPostToolUseFailure }
func (h PostToolUseFailureHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// UserPromptSubmitHookInput is sent when the user submits a prompt.
type UserPromptSubmitHookInput struct {
	BaseHookFields
	Prompt string `json:"prompt"`
}

func (UserPromptSubmitHookInput) hookEventName() HookEvent     { return HookEventUserPromptSubmit }
func (h UserPromptSubmitHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// StopHookInput is sent when the conversation is stopping.
type StopHookInput struct {
	BaseHookFields
	StopHookActive bool `json:"stop_hook_active"`
}

func (StopHookInput) hookEventName() HookEvent     { return HookEventStop }
func (h StopHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// SubagentStopHookInput is sent when a subagent stops.
type SubagentStopHookInput struct {
	BaseHookFields
	StopHookActive      bool   `json:"stop_hook_active"`
	AgentID             string `json:"agent_id"`
	AgentTranscriptPath string `json:"agent_transcript_path"`
	AgentType           string `json:"agent_type"`
}

func (SubagentStopHookInput) hookEventName() HookEvent     { return HookEventSubagentStop }
func (h SubagentStopHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// PreCompactHookInput is sent before context compaction.
type PreCompactHookInput struct {
	BaseHookFields
	Trigger            string `json:"trigger"` // "manual" or "auto"
	CustomInstructions string `json:"custom_instructions,omitempty"`
}

func (PreCompactHookInput) hookEventName() HookEvent     { return HookEventPreCompact }
func (h PreCompactHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// NotificationHookInput is sent when a notification is triggered.
type NotificationHookInput struct {
	BaseHookFields
	Message          string `json:"message"`
	Title            string `json:"title,omitempty"`
	NotificationType string `json:"notification_type"`
}

func (NotificationHookInput) hookEventName() HookEvent     { return HookEventNotification }
func (h NotificationHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// SubagentStartHookInput is sent when a subagent starts.
type SubagentStartHookInput struct {
	BaseHookFields
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
}

func (SubagentStartHookInput) hookEventName() HookEvent     { return HookEventSubagentStart }
func (h SubagentStartHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// PermissionRequestHookInput is sent when a permission request is made.
type PermissionRequestHookInput struct {
	BaseHookFields
	ToolName              string         `json:"tool_name"`
	ToolInput             map[string]any `json:"tool_input"`
	PermissionSuggestions []any          `json:"permission_suggestions,omitempty"`
}

func (PermissionRequestHookInput) hookEventName() HookEvent     { return HookEventPermissionRequest }
func (h PermissionRequestHookInput) BaseFields() BaseHookFields { return h.BaseHookFields }

// HookOutput represents the output from a hook callback.
// Fields correspond to the CLI's expected JSON format.
type HookOutput struct {
	// Async hook fields
	Async        *bool `json:"async,omitempty"`
	AsyncTimeout *int  `json:"asyncTimeout,omitempty"`

	// Sync hook fields
	Continue           *bool          `json:"continue,omitempty"`
	SuppressOutput     *bool          `json:"suppressOutput,omitempty"`
	StopReason         string         `json:"stopReason,omitempty"`
	Decision           string         `json:"decision,omitempty"` // "block"
	SystemMessage      string         `json:"systemMessage,omitempty"`
	Reason             string         `json:"reason,omitempty"`
	HookSpecificOutput map[string]any `json:"hookSpecificOutput,omitempty"`
}

// HookCallback is a function invoked when a hook event occurs.
type HookCallback func(ctx context.Context, input HookInput, toolUseID string) (*HookOutput, error)

// HookMatcher matches hook events and dispatches to callbacks.
type HookMatcher struct {
	Matcher string         `json:"matcher,omitempty"`
	Hooks   []HookCallback `json:"-"`
	Timeout *float64       `json:"timeout,omitempty"`
}
