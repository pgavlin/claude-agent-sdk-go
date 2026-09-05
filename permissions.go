package agentsdk

import (
	"context"
	"slices"
	"strings"
)

// PermissionMode controls the CLI's permission behavior.
type PermissionMode string

const (
	// PermissionModeDefault uses the default permission behavior, prompting for each action.
	PermissionModeDefault PermissionMode = "default"
	// PermissionModeAcceptEdits automatically accepts file edits without prompting.
	PermissionModeAcceptEdits PermissionMode = "acceptEdits"
	// PermissionModePlan restricts the CLI to read-only operations.
	PermissionModePlan PermissionMode = "plan"
	// PermissionModeBypassPermissions bypasses all permission checks.
	PermissionModeBypassPermissions PermissionMode = "bypassPermissions"
	// PermissionModeDontAsk denies anything not pre-approved by allow rules
	// instead of prompting.
	PermissionModeDontAsk PermissionMode = "dontAsk"
	// PermissionModeAuto lets a model classifier approve or deny each tool call.
	PermissionModeAuto PermissionMode = "auto"
)

// PermissionBehavior represents the behavior for a permission rule.
type PermissionBehavior string

const (
	// PermissionBehaviorAllow automatically allows the tool.
	PermissionBehaviorAllow PermissionBehavior = "allow"
	// PermissionBehaviorDeny automatically denies the tool.
	PermissionBehaviorDeny PermissionBehavior = "deny"
	// PermissionBehaviorAsk prompts the user for a permission decision.
	PermissionBehaviorAsk PermissionBehavior = "ask"
)

// PermissionUpdateDestination specifies where permission updates are stored.
type PermissionUpdateDestination string

const (
	// PermissionDestinationUser stores permission updates in user settings.
	PermissionDestinationUser PermissionUpdateDestination = "userSettings"
	// PermissionDestinationProject stores permission updates in project settings.
	PermissionDestinationProject PermissionUpdateDestination = "projectSettings"
	// PermissionDestinationLocal stores permission updates in local settings.
	PermissionDestinationLocal PermissionUpdateDestination = "localSettings"
	// PermissionDestinationSession stores permission updates for the current session only.
	PermissionDestinationSession PermissionUpdateDestination = "session"
)

// PermissionUpdateType specifies the type of permission update.
type PermissionUpdateType string

const (
	// PermissionUpdateAddRules adds permission rules to the existing set.
	PermissionUpdateAddRules PermissionUpdateType = "addRules"
	// PermissionUpdateReplaceRules replaces all existing permission rules.
	PermissionUpdateReplaceRules PermissionUpdateType = "replaceRules"
	// PermissionUpdateRemoveRules removes specific permission rules.
	PermissionUpdateRemoveRules PermissionUpdateType = "removeRules"
	// PermissionUpdateSetMode changes the permission mode.
	PermissionUpdateSetMode PermissionUpdateType = "setMode"
	// PermissionUpdateAddDirectories adds directories to the allowed set.
	PermissionUpdateAddDirectories PermissionUpdateType = "addDirectories"
	// PermissionUpdateRemoveDirectories removes directories from the allowed set.
	PermissionUpdateRemoveDirectories PermissionUpdateType = "removeDirectories"
)

// PermissionRuleValue represents a permission rule for a specific tool.
type PermissionRuleValue struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent,omitempty"`
}

// PermissionUpdate represents a change to permission settings.
type PermissionUpdate struct {
	Type        PermissionUpdateType        `json:"type"`
	Rules       []PermissionRuleValue       `json:"rules,omitempty"`
	Behavior    PermissionBehavior          `json:"behavior,omitempty"`
	Mode        PermissionMode              `json:"mode,omitempty"`
	Directories []string                    `json:"directories,omitempty"`
	Destination PermissionUpdateDestination `json:"destination,omitempty"`
}

// ToolPermissionContext provides context for permission decisions.
type ToolPermissionContext struct {
	Signal      any                `json:"signal,omitempty"`
	Suggestions []PermissionUpdate `json:"suggestions,omitempty"`

	// ToolUseID uniquely identifies this tool call within the assistant
	// message. Multiple tool calls in the same message have different IDs.
	// The wire protocol always supplies it for CanUseTool callbacks.
	ToolUseID string `json:"tool_use_id,omitempty"`

	// AgentID is the sub-agent's ID when the call originates inside a
	// sub-agent, and empty on the main thread.
	AgentID string `json:"agent_id,omitempty"`

	// BlockedPath is the file path that triggered the permission request,
	// if applicable — for example a Bash command reaching outside the
	// allowed directories.
	BlockedPath string `json:"blocked_path,omitempty"`

	// DecisionReason explains why this permission request was triggered.
	// A PreToolUse hook returning permissionDecision "ask" forwards its
	// permissionDecisionReason here.
	DecisionReason string `json:"decision_reason,omitempty"`

	// Title is the full permission prompt sentence, e.g. "Claude wants to
	// read foo.txt". Prefer it over reconstructing text from the tool name
	// and input.
	Title string `json:"title,omitempty"`

	// DisplayName is a short noun phrase for the action, e.g. "Read file",
	// suitable for button labels.
	DisplayName string `json:"display_name,omitempty"`

	// Description is a human-readable subtitle for the permission UI.
	Description string `json:"description,omitempty"`
}

// PermissionResult is a sealed interface for permission decisions.
type PermissionResult interface {
	permissionResult()
}

// PermissionAllow grants permission to use a tool.
type PermissionAllow struct {
	UpdatedInput       map[string]any     `json:"updatedInput,omitempty"`
	UpdatedPermissions []PermissionUpdate `json:"updatedPermissions,omitempty"`
}

func (PermissionAllow) permissionResult() {}

// PermissionDeny denies permission to use a tool.
type PermissionDeny struct {
	Message   string `json:"message,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
}

func (PermissionDeny) permissionResult() {}

// CanUseToolFunc is a callback invoked when the CLI requests permission to use a tool.
type CanUseToolFunc func(ctx context.Context, toolName string, input map[string]any, permCtx ToolPermissionContext) (PermissionResult, error)

// configureCanUseTool validates the CanUseTool option and routes permission
// prompts over stdio. It is shared by Query and Client.Connect so both entry
// points enforce the same rules.
//
// It is a no-op when no callback is set. Otherwise it checks the callback is
// not combined with PermissionPromptToolName and sets that name to "stdio",
// so the CLI sends permission requests over the control protocol.
func configureCanUseTool(opts *ClaudeAgentOptions) error {
	if opts == nil || opts.CanUseTool == nil {
		return nil
	}
	if opts.PermissionPromptToolName != "" && opts.PermissionPromptToolName != "stdio" {
		return NewValidationError(
			"CanUseTool callback cannot be used with PermissionPromptToolName; use one or the other")
	}
	opts.PermissionPromptToolName = "stdio"
	return nil
}

// wholeToolAllowed returns the tool an AllowedTools entry allows outright,
// or "".
//
// It mirrors the CLI's rule parser: an entry allows a whole tool when it has
// no specifier ("Read"), or when the specifier is empty or a lone wildcard
// ("Read()", "Read(*)"). A real specifier such as "Bash(ls:*)" only allows
// matching invocations. Malformed entries fall back to the whole string as a
// tool name in the CLI, so they match nothing and are ignored here.
func wholeToolAllowed(entry string) string {
	if strings.TrimSpace(entry) == "" {
		return ""
	}
	openIndex := strings.Index(entry, "(")
	if openIndex == -1 {
		return entry
	}
	if openIndex == 0 || !strings.HasSuffix(entry, ")") {
		return ""
	}
	if spec := entry[openIndex+1 : len(entry)-1]; spec == "" || spec == "*" {
		return entry[:openIndex]
	}
	return ""
}

// CanUseToolShadowedWarning describes options that auto-approve tool calls
// before a CanUseTool callback is consulted. It returns "" when nothing
// shadows the callback.
//
// Shadowing can be intentional — a callback used solely for tools outside
// AllowedTools, say — so this is advisory rather than an error. Allow rules
// from settings files can also shadow the callback but are not visible here.
func CanUseToolShadowedWarning(opts *ClaudeAgentOptions) string {
	if opts == nil || opts.CanUseTool == nil {
		return ""
	}

	if opts.PermissionMode == PermissionModeBypassPermissions {
		return "CanUseTool will not be invoked: PermissionMode bypassPermissions auto-approves " +
			"every tool call, except explicit deny rules, before the callback is consulted. " +
			"To gate every tool call, use a PreToolUse hook instead."
	}

	allowedTools := opts.AllowedTools
	// SkillsAll makes the transport append a bare "Skill" to the effective
	// allowed tools, so it shadows the callback just like a hand-written
	// entry. A list of skill names appends Skill(name) specifiers, which do
	// not.
	if _, all := opts.Skills.(SkillsAll); all && !slices.Contains(allowedTools, "Skill") {
		allowedTools = append(append([]string(nil), allowedTools...), "Skill")
	}

	// Deduplicate while preserving order, so a redundant config such as
	// ["Read", "Read()"] does not report the same tool twice.
	var shadowed []string
	seen := map[string]struct{}{}
	for _, entry := range allowedTools {
		tool := wholeToolAllowed(entry)
		if tool == "" {
			continue
		}
		if _, dup := seen[tool]; dup {
			continue
		}
		seen[tool] = struct{}{}
		shadowed = append(shadowed, tool)
	}
	if len(shadowed) == 0 {
		return ""
	}

	return "CanUseTool will not be invoked for: " + strings.Join(shadowed, ", ") +
		". An AllowedTools entry that allows a whole tool auto-approves it before the " +
		"callback is consulted. To gate every tool call, use a PreToolUse hook, or narrow " +
		"the entry so calls fall through to CanUseTool. Allow rules from settings files can " +
		"also shadow the callback but are not visible here."
}
