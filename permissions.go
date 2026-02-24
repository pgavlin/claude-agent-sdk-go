package agentsdk

import "context"

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
