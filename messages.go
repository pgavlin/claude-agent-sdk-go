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

// MessageOriginKind identifies how a user-role message reached the session.
// Newer CLI versions may emit kinds not listed here; treat anything
// unrecognized as "not human".
type MessageOriginKind string

const (
	// MessageOriginHuman marks a turn submitted by the application.
	MessageOriginHuman MessageOriginKind = "human"
	// MessageOriginChannel marks a message that arrived on an MCP server channel.
	MessageOriginChannel MessageOriginKind = "channel"
	// MessageOriginPeer marks a message relayed from a peer session.
	MessageOriginPeer MessageOriginKind = "peer"
	// MessageOriginTaskNotification marks a background-task notification.
	MessageOriginTaskNotification MessageOriginKind = "task-notification"
	// MessageOriginCoordinator marks a coordinator-injected message.
	MessageOriginCoordinator MessageOriginKind = "coordinator"
	// MessageOriginUnclassified marks a message the CLI could not classify.
	MessageOriginUnclassified MessageOriginKind = "unclassified"
	// MessageOriginObserver marks a message from an observer.
	MessageOriginObserver MessageOriginKind = "observer"
	// MessageOriginAutoContinuation marks an automatic continuation turn.
	MessageOriginAutoContinuation MessageOriginKind = "auto-continuation"
	// MessageOriginObserverActivity marks an observer activity message.
	MessageOriginObserverActivity MessageOriginKind = "observer-activity"
)

// TaskNotificationOriginSubkind refines a MessageOriginTaskNotification.
type TaskNotificationOriginSubkind string

const (
	// TaskNotificationOriginScheduledTrigger marks the fired prompt of a scheduled task.
	TaskNotificationOriginScheduledTrigger TaskNotificationOriginSubkind = "scheduled-trigger"
	// TaskNotificationOriginPeerSendMessage marks a message sent from another session.
	TaskNotificationOriginPeerSendMessage TaskNotificationOriginSubkind = "peer-send-message"
)

// MessageOrigin describes the provenance of a user-role message, and on a
// ResultMessage the provenance of the message that triggered that turn.
//
// In streaming-input mode one connection interleaves the turns the caller
// sends with turns the session injects on its own — background-task
// notifications, scheduled-task prompts, MCP channel messages, messages
// relayed from peer sessions. Origin tells them apart, e.g. to decide whether
// a ResultMessage answers the caller's own prompt:
//
//	if msg.Origin == nil || msg.Origin.Kind == agentsdk.MessageOriginHuman {
//		// a turn this application submitted
//	}
//
// Only Kind is always present; the remaining fields depend on Kind. A nil
// Origin means the CLI did not attribute the message: prompts sent through
// Query or Client.Query arrive that way unless the caller stamps
// {"kind": "human"} on the message itself (only the human kind is honored
// from an SDK host). Raw carries the unmodified dict from the CLI, which may
// include undocumented keys.
type MessageOrigin struct {
	// Kind is the discriminator.
	Kind MessageOriginKind `json:"kind"`

	// Server is the MCP server name a channel message arrived on.
	// Set when Kind is MessageOriginChannel.
	Server string `json:"server,omitempty"`

	// From is the sender address for peer and observer messages. It is
	// sender-asserted: use it for reply routing or display, never as proof
	// of identity.
	From string `json:"from,omitempty"`

	// Name is the sender display name for peer messages, already normalized
	// by the CLI (control characters stripped, trimmed, length-capped).
	Name string `json:"name,omitempty"`

	// FromSession is the sender's host-openable session ID for peer
	// messages, when its host provided one. A navigation target only.
	FromSession string `json:"fromSession,omitempty"`

	// SenderTaskID is the task ID of the in-process background subagent
	// that sent a peer or observer message. Empty for cross-session peers.
	SenderTaskID string `json:"senderTaskId,omitempty"`

	// Body is the decoded peer message body with the envelope stripped,
	// byte-exact with what the model saw. Render this rather than
	// re-parsing the message text.
	Body string `json:"body,omitempty"`

	// VerifiedPeerPid is the kernel-verified pid of the process that
	// connected to this session's local messaging socket — for relayed
	// traffic, the relay. Zero when unverifiable.
	VerifiedPeerPid int `json:"verifiedPeerPid,omitempty"`

	// Subkind is set on task notifications that are a scheduled task's
	// fired prompt or a message from another of the user's sessions.
	// Empty for ordinary background-task notifications.
	Subkind TaskNotificationOriginSubkind `json:"subkind,omitempty"`

	// Raw is the unmodified origin dict from the CLI.
	Raw map[string]any `json:"-"`
}

// UserMessage represents a message from the user.
type UserMessage struct {
	Content         any            `json:"content"` // string | []ContentBlock
	UUID            string         `json:"uuid,omitempty"`
	ParentToolUseID string         `json:"parent_tool_use_id,omitempty"`
	ToolUseResult   map[string]any `json:"tool_use_result,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`

	// Origin is the provenance of this message, or nil when the CLI did not
	// attribute it. Populated on injected turns (task notifications,
	// channel and peer messages) and on user messages the CLI replays;
	// tool-result messages never carry it.
	Origin *MessageOrigin `json:"origin,omitempty"`
}

func (UserMessage) messageType() string { return "user" }

// AssistantMessage represents a message from the assistant.
type AssistantMessage struct {
	Content         []ContentBlock        `json:"content"`
	Model           string                `json:"model"`
	ParentToolUseID string                `json:"parent_tool_use_id,omitempty"`
	Error           AssistantMessageError `json:"error,omitempty"`
	Usage           map[string]any        `json:"usage,omitempty"`
	MessageID       string                `json:"message_id,omitempty"`
	StopReason      string                `json:"stop_reason,omitempty"`
	SessionID       string                `json:"session_id,omitempty"`
	UUID            string                `json:"uuid,omitempty"`
}

func (AssistantMessage) messageType() string { return "assistant" }

// SystemMessage represents a system-level message.
//
// Several system subtypes parse into richer types that embed SystemMessage:
// TaskStartedMessage, TaskProgressMessage, TaskNotificationMessage,
// TaskUpdatedMessage, HookEventMessage, and MirrorErrorMessage. Unlike the
// Python SDK, where those are subclasses, a Go type switch on *SystemMessage
// will not match them — switch on the specific type, or read the embedded
// SystemMessage field for the raw Subtype and Data.
type SystemMessage struct {
	Subtype string         `json:"subtype"`
	Data    map[string]any `json:"data"`
}

func (SystemMessage) messageType() string { return "system" }

// TaskUsage reports usage statistics carried by task_progress and
// task_notification messages.
type TaskUsage struct {
	TotalTokens int `json:"total_tokens"`
	ToolUses    int `json:"tool_uses"`
	DurationMS  int `json:"duration_ms"`
}

// TaskNotificationStatus is the status reported by a task_notification message.
type TaskNotificationStatus string

const (
	// TaskNotificationCompleted indicates the task finished successfully.
	TaskNotificationCompleted TaskNotificationStatus = "completed"
	// TaskNotificationFailed indicates the task failed.
	TaskNotificationFailed TaskNotificationStatus = "failed"
	// TaskNotificationStopped indicates the task was stopped.
	TaskNotificationStopped TaskNotificationStatus = "stopped"
)

// TaskUpdatedStatus is the status reported inside a task_updated patch.
// Pending, running, and paused are non-terminal; completed, failed, and
// killed are terminal. A task_updated patch reports the raw "killed"; the
// CLI maps that to "stopped" only when it emits a task_notification.
type TaskUpdatedStatus string

const (
	// TaskUpdatedPending indicates the task has not started yet.
	TaskUpdatedPending TaskUpdatedStatus = "pending"
	// TaskUpdatedRunning indicates the task is running.
	TaskUpdatedRunning TaskUpdatedStatus = "running"
	// TaskUpdatedPaused indicates the task is paused.
	TaskUpdatedPaused TaskUpdatedStatus = "paused"
	// TaskUpdatedCompleted indicates the task finished successfully.
	TaskUpdatedCompleted TaskUpdatedStatus = "completed"
	// TaskUpdatedFailed indicates the task failed.
	TaskUpdatedFailed TaskUpdatedStatus = "failed"
	// TaskUpdatedKilled indicates the task was killed.
	TaskUpdatedKilled TaskUpdatedStatus = "killed"
)

// terminalTaskStatuses spans both lifecycle vocabularies: a
// task_notification reports "stopped" (the CLI's mapped form of a killed
// task) while a task_updated patch reports the raw "killed".
var terminalTaskStatuses = map[string]struct{}{
	"completed": {},
	"failed":    {},
	"stopped":   {},
	"killed":    {},
}

// IsTerminalTaskStatus reports whether a task status means the task has
// finished and should be cleared from any "active task" tracking. It accepts
// both TaskNotificationStatus and TaskUpdatedStatus values, since a
// background task's terminal state can arrive as either message.
func IsTerminalTaskStatus(status string) bool {
	_, ok := terminalTaskStatuses[status]
	return ok
}

// TaskStartedMessage is emitted when a task starts. It embeds SystemMessage,
// whose Subtype and Data carry the raw payload.
type TaskStartedMessage struct {
	SystemMessage
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	UUID        string `json:"uuid"`
	SessionID   string `json:"session_id"`
	ToolUseID   string `json:"tool_use_id,omitempty"`
	TaskType    string `json:"task_type,omitempty"`
}

// TaskProgressMessage is emitted while a task is in progress. It embeds
// SystemMessage, whose Subtype and Data carry the raw payload.
type TaskProgressMessage struct {
	SystemMessage
	TaskID       string    `json:"task_id"`
	Description  string    `json:"description"`
	Usage        TaskUsage `json:"usage"`
	UUID         string    `json:"uuid"`
	SessionID    string    `json:"session_id"`
	ToolUseID    string    `json:"tool_use_id,omitempty"`
	LastToolName string    `json:"last_tool_name,omitempty"`
}

// TaskNotificationMessage is emitted when a task completes, fails, or is
// stopped. It embeds SystemMessage, whose Subtype and Data carry the raw
// payload.
//
// Not every terminal task emits this message: background tasks may report
// completion only via a TaskUpdatedMessage whose Status is terminal.
// Consumers tracking active task IDs should clear them on a terminal status
// from either message — see IsTerminalTaskStatus.
type TaskNotificationMessage struct {
	SystemMessage
	TaskID     string                 `json:"task_id"`
	Status     TaskNotificationStatus `json:"status"`
	OutputFile string                 `json:"output_file"`
	Summary    string                 `json:"summary"`
	UUID       string                 `json:"uuid"`
	SessionID  string                 `json:"session_id"`
	ToolUseID  string                 `json:"tool_use_id,omitempty"`
	Usage      *TaskUsage             `json:"usage,omitempty"`
}

// TaskUpdatedMessage is emitted when a background task's state changes.
// Patch carries the changed fields; when Status is terminal the task has
// finished. It embeds SystemMessage, whose Subtype and Data carry the raw
// payload.
//
// A background task's terminal state can arrive only as a TaskUpdatedMessage
// with no accompanying TaskNotificationMessage — a task stopped via StopTask
// reports Status "killed" here and the matching notification is sometimes
// suppressed. Clear active task IDs on a terminal status from either message.
type TaskUpdatedMessage struct {
	SystemMessage
	TaskID    string            `json:"task_id"`
	Patch     map[string]any    `json:"patch"`
	Status    TaskUpdatedStatus `json:"status,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	UUID      string            `json:"uuid,omitempty"`
}

// HookEventMessage is a hook lifecycle event emitted by the CLI when
// ClaudeAgentOptions.IncludeHookEvents is set. It embeds SystemMessage,
// whose Subtype is "hook_started" or "hook_response" and whose Data carries
// the raw payload, including event-specific fields not modeled here (a
// hook_response also carries output, exit_code, and outcome keys).
type HookEventMessage struct {
	SystemMessage
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id,omitempty"`
	UUID          string `json:"uuid,omitempty"`
}

// MirrorErrorMessage is emitted when a SessionStore.Append call fails. It is
// non-fatal: the local-disk transcript is already durable, so the session
// continues unaffected, but the mirrored copy is missing the failed batch.
// It embeds SystemMessage, whose Subtype is "mirror_error".
type MirrorErrorMessage struct {
	SystemMessage
	Key   *SessionKey `json:"key,omitempty"`
	Error string      `json:"error"`
}

// DeferredToolUse is a tool use deferred by a PreToolUse hook that returned
// permissionDecision "defer". The run stops and the result message carries
// the deferred call so the caller can inspect it and decide whether to
// resume.
type DeferredToolUse struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ModelUsage is a per-model token usage and cost breakdown. Field names
// match the CLI's modelUsage wire format, which the SDK passes through
// verbatim.
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`

	// CanonicalModel is the canonical model ID used for the pricing lookup,
	// e.g. "claude-opus-4-7". It may differ from the raw model string this
	// entry is keyed by (provider-specific IDs, aliases).
	CanonicalModel string `json:"canonicalModel,omitempty"`

	// Provider is the API provider that served this model: "firstParty",
	// "bedrock", "vertex", "foundry", "anthropicAws",
	// "anthropicGoogleCloud", "mantle", or "gateway".
	Provider string `json:"provider,omitempty"`
}

// ResultMessage represents the final result of a conversation turn.
type ResultMessage struct {
	Subtype          string         `json:"subtype"`
	DurationMS       int            `json:"duration_ms"`
	DurationAPIMS    int            `json:"duration_api_ms"`
	IsError          bool           `json:"is_error"`
	NumTurns         int            `json:"num_turns"`
	SessionID        string         `json:"session_id"`
	StopReason       string         `json:"stop_reason,omitempty"`
	TotalCostUSD     *float64       `json:"total_cost_usd,omitempty"`
	Usage            map[string]any `json:"usage,omitempty"`
	Result           string         `json:"result,omitempty"`
	StructuredOutput any            `json:"structured_output,omitempty"`

	// ModelUsage breaks usage and cost down per model, keyed by model string.
	ModelUsage map[string]ModelUsage `json:"model_usage,omitempty"`

	// PermissionDenials lists permission denials that occurred during the turn.
	PermissionDenials []any `json:"permission_denials,omitempty"`

	// DeferredToolUse is set when a PreToolUse hook deferred a tool call.
	DeferredToolUse *DeferredToolUse `json:"deferred_tool_use,omitempty"`

	// Errors lists error strings reported for the turn.
	Errors []string `json:"errors,omitempty"`

	// APIErrorStatus is the HTTP status code (e.g. 429, 500, 529) of the
	// failing API call when IsError is true and Subtype is "success", and
	// zero otherwise. Emitted by the CLI since v2.1.110. Safe to log: it
	// carries no message content.
	APIErrorStatus int `json:"api_error_status,omitempty"`

	UUID string `json:"uuid,omitempty"`

	// TerminalReason says why the query loop terminated, e.g. "completed",
	// "max_turns", or "aborted_streaming". A value of "aborted_streaming"
	// or "aborted_tools" means the turn was cancelled, via Client.Interrupt
	// or an interrupt control request. Empty when the CLI did not report a
	// terminal reason (older CLI versions, or a result that bypassed the
	// query loop such as a local slash command).
	TerminalReason string `json:"terminal_reason,omitempty"`

	// Origin is the provenance of the user message that triggered this
	// turn. It lets a streaming-input consumer distinguish the result of
	// its own prompt (nil, or MessageOriginHuman if it stamped that) from
	// results of injected turns such as background-task notifications.
	Origin *MessageOrigin `json:"origin,omitempty"`
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

// RateLimitStatus is the current rate limit status.
type RateLimitStatus string

const (
	// RateLimitAllowed indicates the request is within limits.
	RateLimitAllowed RateLimitStatus = "allowed"
	// RateLimitAllowedWarning indicates the limit is being approached.
	RateLimitAllowedWarning RateLimitStatus = "allowed_warning"
	// RateLimitRejected indicates the limit has been hit.
	RateLimitRejected RateLimitStatus = "rejected"
)

// RateLimitType identifies which rate limit window applies.
type RateLimitType string

const (
	// RateLimitFiveHour is the five-hour rate limit window.
	RateLimitFiveHour RateLimitType = "five_hour"
	// RateLimitSevenDay is the seven-day rate limit window.
	RateLimitSevenDay RateLimitType = "seven_day"
	// RateLimitSevenDayOpus is the seven-day Opus rate limit window.
	RateLimitSevenDayOpus RateLimitType = "seven_day_opus"
	// RateLimitSevenDaySonnet is the seven-day Sonnet rate limit window.
	RateLimitSevenDaySonnet RateLimitType = "seven_day_sonnet"
	// RateLimitOverage is the overage rate limit window.
	RateLimitOverage RateLimitType = "overage"
)

// RateLimitInfo is the rate limit status the CLI emits when rate limit state
// changes.
type RateLimitInfo struct {
	// Status is the current rate limit status. RateLimitAllowedWarning
	// means the limit is being approached; RateLimitRejected means it has
	// been hit.
	Status RateLimitStatus `json:"status"`

	// ResetsAt is the Unix timestamp when the rate limit window resets.
	ResetsAt *int64 `json:"resets_at,omitempty"`

	// RateLimitType is which rate limit window applies.
	RateLimitType RateLimitType `json:"rate_limit_type,omitempty"`

	// Utilization is the fraction of the rate limit consumed, 0.0 to 1.0.
	Utilization *float64 `json:"utilization,omitempty"`

	// OverageStatus is the status of overage/pay-as-you-go usage, if applicable.
	OverageStatus RateLimitStatus `json:"overage_status,omitempty"`

	// OverageResetsAt is the Unix timestamp when the overage window resets.
	OverageResetsAt *int64 `json:"overage_resets_at,omitempty"`

	// OverageDisabledReason says why overage is unavailable when the status
	// is rejected.
	OverageDisabledReason string `json:"overage_disabled_reason,omitempty"`

	// Raw is the full dict from the CLI, including fields not modeled above.
	Raw map[string]any `json:"-"`
}

// RateLimitEvent is emitted whenever the rate limit status transitions, for
// example from allowed to allowed_warning. Use it to warn users before they
// hit a hard limit, or to back off gracefully when Status is
// RateLimitRejected.
type RateLimitEvent struct {
	RateLimitInfo RateLimitInfo `json:"rate_limit_info"`
	UUID          string        `json:"uuid"`
	SessionID     string        `json:"session_id"`
}

func (RateLimitEvent) messageType() string { return "rate_limit_event" }

// ConversationResetMessage is emitted when the session's conversation is
// replaced without ending the connection — after /clear, or any other flow
// that discards the transcript mid-session.
//
// In streaming-input mode a single connection can carry many user turns, and
// a reset clears the conversation history and zeroes the running totals
// reported on subsequent ResultMessage values, such as TotalCostUSD. Snapshot
// those totals when this message arrives if the caller accumulates them.
type ConversationResetMessage struct {
	// NewConversationID is an opaque identifier for the fresh conversation,
	// for UIs to key an empty transcript on (and to discard any cached
	// session title). It is not the SessionID of subsequent messages — read
	// that from the next message.
	NewConversationID string `json:"new_conversation_id"`

	// UUID is the unique ID of this message.
	UUID string `json:"uuid"`

	// SessionID is the ID of the session that was reset — the outgoing
	// session. Messages after the reset carry a new session ID.
	SessionID string `json:"session_id"`
}

func (ConversationResetMessage) messageType() string { return "conversation_reset" }
