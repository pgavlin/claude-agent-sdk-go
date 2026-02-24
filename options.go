package agentsdk

// Effort controls the thinking effort level.
type Effort string

const (
	// EffortLow sets the thinking effort to low.
	EffortLow Effort = "low"
	// EffortMedium sets the thinking effort to medium.
	EffortMedium Effort = "medium"
	// EffortHigh sets the thinking effort to high.
	EffortHigh Effort = "high"
	// EffortMax sets the thinking effort to maximum.
	EffortMax Effort = "max"
)

// SettingSource identifies the source of a setting.
type SettingSource string

const (
	// SettingSourceUser loads settings from the user configuration.
	SettingSourceUser SettingSource = "user"
	// SettingSourceProject loads settings from the project configuration.
	SettingSourceProject SettingSource = "project"
	// SettingSourceLocal loads settings from the local configuration.
	SettingSourceLocal SettingSource = "local"
)

// SdkBeta identifies a beta feature.
type SdkBeta string

const (
	// SdkBetaContext1M enables the 1M context window beta feature.
	SdkBetaContext1M SdkBeta = "context-1m-2025-08-07"
)

// ToolsPreset specifies a preset set of tools.
type ToolsPreset struct {
	Type   string `json:"type"`   // "preset"
	Preset string `json:"preset"` // "claude_code"
}

// SystemPromptPreset specifies a preset system prompt with optional append.
type SystemPromptPreset struct {
	Type   string `json:"type"`   // "preset"
	Preset string `json:"preset"` // "claude_code"
	Append string `json:"append,omitempty"`
}

// AgentDefinition defines a subagent configuration.
type AgentDefinition struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"` // "sonnet", "opus", "haiku", "inherit"
}

// ThinkingConfig controls extended thinking behavior.
// Use one of ThinkingConfigAdaptive, ThinkingConfigEnabled, or ThinkingConfigDisabled.
type ThinkingConfig interface {
	thinkingConfigType() string
}

// ThinkingConfigAdaptive enables adaptive thinking.
type ThinkingConfigAdaptive struct{}

func (ThinkingConfigAdaptive) thinkingConfigType() string { return "adaptive" }

// ThinkingConfigEnabled enables thinking with a specific token budget.
type ThinkingConfigEnabled struct {
	BudgetTokens int `json:"budget_tokens"`
}

func (ThinkingConfigEnabled) thinkingConfigType() string { return "enabled" }

// ThinkingConfigDisabled disables thinking.
type ThinkingConfigDisabled struct{}

func (ThinkingConfigDisabled) thinkingConfigType() string { return "disabled" }

// SandboxNetworkConfig configures sandbox network settings.
type SandboxNetworkConfig struct {
	AllowUnixSockets    []string `json:"allowUnixSockets,omitempty"`
	AllowAllUnixSockets *bool    `json:"allowAllUnixSockets,omitempty"`
	AllowLocalBinding   *bool    `json:"allowLocalBinding,omitempty"`
	HTTPProxyPort       *int     `json:"httpProxyPort,omitempty"`
	SOCKSProxyPort      *int     `json:"socksProxyPort,omitempty"`
}

// SandboxIgnoreViolations configures which sandbox violations to ignore.
type SandboxIgnoreViolations struct {
	File    []string `json:"file,omitempty"`
	Network []string `json:"network,omitempty"`
}

// SandboxSettings configures the sandbox.
type SandboxSettings struct {
	Enabled                   *bool                    `json:"enabled,omitempty"`
	AutoAllowBashIfSandboxed  *bool                    `json:"autoAllowBashIfSandboxed,omitempty"`
	ExcludedCommands          []string                 `json:"excludedCommands,omitempty"`
	AllowUnsandboxedCommands  *bool                    `json:"allowUnsandboxedCommands,omitempty"`
	Network                   *SandboxNetworkConfig    `json:"network,omitempty"`
	IgnoreViolations          *SandboxIgnoreViolations `json:"ignoreViolations,omitempty"`
	EnableWeakerNestedSandbox *bool                    `json:"enableWeakerNestedSandbox,omitempty"`
}

// PluginConfig configures a local plugin.
type PluginConfig struct {
	Type string `json:"type"` // "local"
	Path string `json:"path"`
}

// OutputFormat configures structured output.
type OutputFormat struct {
	JSONSchema map[string]any `json:"json_schema,omitempty"`
}

// ClaudeAgentOptions configures the behavior of Query and Client.
type ClaudeAgentOptions struct {
	// Tool configuration
	Tools           any      `json:"-"` // []string | *ToolsPreset | nil
	AllowedTools    []string `json:"-"`
	DisallowedTools []string `json:"-"`

	// Prompt configuration
	SystemPrompt any `json:"-"` // string | *SystemPromptPreset | nil

	// MCP configuration
	McpServers any `json:"-"` // map[string]McpServerConfig | string | nil

	// Permission configuration
	PermissionMode           PermissionMode `json:"-"`
	PermissionPromptToolName string         `json:"-"`
	CanUseTool               CanUseToolFunc `json:"-"`

	// Session configuration
	ContinueConversation bool   `json:"-"`
	Resume               string `json:"-"`
	ForkSession          bool   `json:"-"`

	// Limits
	MaxTurns     *int     `json:"-"`
	MaxBudgetUSD *float64 `json:"-"`

	// Model configuration
	Model         string `json:"-"`
	FallbackModel string `json:"-"`

	// Beta features
	Betas []SdkBeta `json:"-"`

	// Working directory and CLI path
	CWD     string `json:"-"`
	CLIPath string `json:"-"`

	// Settings
	Settings       string          `json:"-"`
	SettingSources []SettingSource `json:"-"`
	AddDirs        []string        `json:"-"`

	// Environment
	Env       map[string]string `json:"-"`
	ExtraArgs map[string]string `json:"-"`

	// Buffer size
	MaxBufferSize int `json:"-"`

	// Stderr callback
	Stderr func(string) `json:"-"`

	// Hooks
	Hooks map[HookEvent][]HookMatcher `json:"-"`

	// User identification
	User string `json:"-"`

	// Streaming options
	IncludePartialMessages bool `json:"-"`

	// Agent definitions
	Agents map[string]AgentDefinition `json:"-"`

	// Sandbox configuration
	Sandbox *SandboxSettings `json:"-"`

	// Plugin configuration
	Plugins []PluginConfig `json:"-"`

	// Thinking configuration
	MaxThinkingTokens *int           `json:"-"` // deprecated: use Thinking
	Thinking          ThinkingConfig `json:"-"`

	// Effort level
	Effort Effort `json:"-"`

	// Output format
	OutputFormat *OutputFormat `json:"-"`

	// File checkpointing
	EnableFileCheckpointing bool `json:"-"`
}
