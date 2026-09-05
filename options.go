package agentsdk

// Effort controls how much effort Claude puts into its response. It works
// with adaptive thinking to guide thinking depth.
//
// See https://docs.anthropic.com/en/docs/build-with-claude/effort.
type Effort string

const (
	// EffortLow requests minimal thinking and the fastest responses.
	EffortLow Effort = "low"
	// EffortMedium requests moderate thinking.
	EffortMedium Effort = "medium"
	// EffortHigh requests deep reasoning. This is the default.
	EffortHigh Effort = "high"
	// EffortXHigh requests extended reasoning depth. Opus 4.7 only; it
	// falls back to EffortHigh on other models.
	EffortXHigh Effort = "xhigh"
	// EffortMax requests maximum effort.
	EffortMax Effort = "max"
)

// SettingSource identifies the source of a setting.
type SettingSource string

const (
	// SettingSourceUser loads global user settings (~/.claude/settings.json).
	SettingSourceUser SettingSource = "user"
	// SettingSourceProject loads project settings (.claude/settings.json).
	SettingSourceProject SettingSource = "project"
	// SettingSourceLocal loads local settings (.claude/settings.local.json).
	SettingSourceLocal SettingSource = "local"
)

// SdkBeta identifies a beta feature.
// See https://docs.anthropic.com/en/api/beta-headers.
type SdkBeta string

const (
	// SdkBetaContext1M enables the 1M context window beta feature
	// (Sonnet 4/4.5 only).
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

	// ExcludeDynamicSections strips per-user dynamic sections (working
	// directory, auto-memory, git status) from the system prompt so it
	// stays static and cacheable across users. The stripped content is
	// re-injected into the first user message, so the model still has
	// access to it.
	//
	// Use this when many users share the same preset system prompt and the
	// prompt-caching prefix should hit cross-user. Requires a Claude Code
	// CLI version that supports the option; older CLIs ignore it.
	ExcludeDynamicSections *bool `json:"exclude_dynamic_sections,omitempty"`
}

// SystemPromptFile loads the system prompt from a file.
type SystemPromptFile struct {
	Type string `json:"type"` // "file"
	Path string `json:"path"`
}

// TaskBudget is an API-side task budget in tokens.
//
// When set, the model is made aware of its remaining token budget so it can
// pace tool use and wrap up before the limit. It is sent as
// output_config.task_budget with the task-budgets-2026-03-13 beta header.
type TaskBudget struct {
	Total int `json:"total"`
}

// AgentDefinition defines a subagent configuration.
type AgentDefinition struct {
	Description string `json:"description"`
	Prompt      string `json:"prompt"`

	// Tools restricts the tools available to the agent.
	//
	// Deprecated: passing "Skill" here is deprecated; use Skills instead.
	Tools []string `json:"tools,omitempty"`

	// DisallowedTools removes tools from the agent's context.
	DisallowedTools []string `json:"disallowedTools,omitempty"`

	// Model is a model alias ("sonnet", "opus", "haiku", "inherit") or a
	// full model ID.
	Model string `json:"model,omitempty"`

	// Skills lists skills to enable for the agent.
	Skills []string `json:"skills,omitempty"`

	// Memory selects which memory scope to load: "user", "project", or "local".
	Memory string `json:"memory,omitempty"`

	// McpServers lists MCP servers available to the agent. Each entry is
	// either a server name (string) or an inline map[string]any of
	// {name: config}.
	McpServers []any `json:"mcpServers,omitempty"`

	// InitialPrompt seeds the agent's first turn.
	InitialPrompt string `json:"initialPrompt,omitempty"`

	// MaxTurns caps the agent's conversation turns.
	MaxTurns *int `json:"maxTurns,omitempty"`

	// Background runs the agent as a background task.
	Background *bool `json:"background,omitempty"`

	// Effort is an Effort level or an int token budget.
	Effort any `json:"effort,omitempty"`

	// PermissionMode overrides the session permission mode for the agent.
	PermissionMode PermissionMode `json:"permissionMode,omitempty"`
}

// ThinkingDisplay controls whether thinking text is returned summarized or
// omitted. Opus 4.7+ defaults to "omitted" (signature only); pass
// ThinkingDisplaySummarized to receive text.
type ThinkingDisplay string

const (
	// ThinkingDisplaySummarized returns summarized thinking text.
	ThinkingDisplaySummarized ThinkingDisplay = "summarized"
	// ThinkingDisplayOmitted returns only thinking signatures.
	ThinkingDisplayOmitted ThinkingDisplay = "omitted"
)

// ThinkingConfig controls extended thinking behavior. Use one of
// ThinkingConfigAdaptive, ThinkingConfigEnabled, or ThinkingConfigDisabled.
//
// See https://docs.anthropic.com/en/docs/build-with-claude/adaptive-thinking.
type ThinkingConfig interface {
	thinkingConfigType() string
}

// ThinkingConfigAdaptive lets Claude decide when and how much to think
// (Opus 4.6+). It is the default for models that support it.
type ThinkingConfigAdaptive struct {
	Display ThinkingDisplay `json:"display,omitempty"`
}

func (ThinkingConfigAdaptive) thinkingConfigType() string { return "adaptive" }

// ThinkingConfigEnabled enables thinking with a fixed token budget, for
// older models.
type ThinkingConfigEnabled struct {
	BudgetTokens int             `json:"budget_tokens"`
	Display      ThinkingDisplay `json:"display,omitempty"`
}

func (ThinkingConfigEnabled) thinkingConfigType() string { return "enabled" }

// ThinkingConfigDisabled disables extended thinking.
type ThinkingConfigDisabled struct{}

func (ThinkingConfigDisabled) thinkingConfigType() string { return "disabled" }

// SandboxNetworkConfig configures sandbox network settings.
type SandboxNetworkConfig struct {
	// AllowedDomains are domain names sandboxed processes can access.
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	// DeniedDomains are always blocked, even if matched by AllowedDomains.
	DeniedDomains []string `json:"deniedDomains,omitempty"`
	// AllowManagedDomainsOnly, when true in managed settings, honors only
	// managed-settings AllowedDomains.
	AllowManagedDomainsOnly *bool `json:"allowManagedDomainsOnly,omitempty"`
	// AllowUnixSockets are Unix socket paths accessible in the sandbox,
	// such as SSH agents.
	AllowUnixSockets []string `json:"allowUnixSockets,omitempty"`
	// AllowAllUnixSockets allows all Unix sockets. Less secure.
	AllowAllUnixSockets *bool `json:"allowAllUnixSockets,omitempty"`
	// AllowLocalBinding allows binding to localhost ports. macOS only.
	AllowLocalBinding *bool `json:"allowLocalBinding,omitempty"`
	// AllowMachLookup lists XPC/Mach service names to allow, with support
	// for a trailing wildcard. macOS only.
	AllowMachLookup []string `json:"allowMachLookup,omitempty"`
	// HTTPProxyPort is the HTTP proxy port, when bringing your own proxy.
	HTTPProxyPort *int `json:"httpProxyPort,omitempty"`
	// SOCKSProxyPort is the SOCKS5 proxy port, when bringing your own proxy.
	SOCKSProxyPort *int `json:"socksProxyPort,omitempty"`
}

// SandboxIgnoreViolations configures which sandbox violations to ignore.
type SandboxIgnoreViolations struct {
	// File lists file paths whose violations are ignored.
	File []string `json:"file,omitempty"`
	// Network lists network hosts whose violations are ignored.
	Network []string `json:"network,omitempty"`
}

// SandboxSettings controls how Claude Code sandboxes bash commands for
// filesystem and network isolation.
//
// Filesystem and network restrictions are configured via permission rules,
// not here: Read deny rules for filesystem reads, Edit allow/deny rules for
// filesystem writes, and WebFetch allow/deny rules for network access.
//
// See https://docs.anthropic.com/en/docs/claude-code/settings#sandbox-settings.
type SandboxSettings struct {
	// Enabled turns on bash sandboxing. macOS and Linux only. Default false.
	Enabled *bool `json:"enabled,omitempty"`
	// AutoAllowBashIfSandboxed auto-approves bash commands when sandboxed.
	// Default true.
	AutoAllowBashIfSandboxed *bool `json:"autoAllowBashIfSandboxed,omitempty"`
	// ExcludedCommands run outside the sandbox, e.g. ["git", "docker"].
	ExcludedCommands []string `json:"excludedCommands,omitempty"`
	// AllowUnsandboxedCommands lets commands bypass the sandbox via
	// dangerouslyDisableSandbox. When false, every command must run
	// sandboxed or appear in ExcludedCommands. Default true.
	AllowUnsandboxedCommands *bool                    `json:"allowUnsandboxedCommands,omitempty"`
	Network                  *SandboxNetworkConfig    `json:"network,omitempty"`
	IgnoreViolations         *SandboxIgnoreViolations `json:"ignoreViolations,omitempty"`
	// EnableWeakerNestedSandbox enables a weaker sandbox for unprivileged
	// Docker environments. Linux only, and it reduces security.
	// Default false.
	EnableWeakerNestedSandbox *bool `json:"enableWeakerNestedSandbox,omitempty"`
}

// PluginConfig configures a local plugin. Only local plugins are supported.
type PluginConfig struct {
	Type string `json:"type"` // "local"
	Path string `json:"path"`
}

// OutputFormat configures structured output. It mirrors the Messages API
// structure, e.g. {"type": "json_schema", "schema": {...}}.
type OutputFormat struct {
	Type   string         `json:"type"` // "json_schema"
	Schema map[string]any `json:"schema,omitempty"`
}

// SkillsAll is the ClaudeAgentOptions.Skills value meaning "every
// discovered skill". Assign it directly: Skills: agentsdk.SkillsAll{}.
type SkillsAll struct{}

// SessionStoreFlushMode controls when transcript-mirror entries are flushed
// to a SessionStore.
type SessionStoreFlushMode string

const (
	// SessionStoreFlushBatched buffers entries and flushes once per turn
	// (on the result message), or when the pending buffer exceeds 500
	// entries or 1 MiB. It keeps adapter latency off the streaming hot
	// path, and is the default.
	SessionStoreFlushBatched SessionStoreFlushMode = "batched"
	// SessionStoreFlushEager triggers a background flush after every
	// transcript_mirror frame so SessionStore.Append sees entries in near
	// real time. Appends are still serialized in enqueue order; a slow
	// adapter will not stall the read loop but will see frames coalesced
	// while it is busy.
	SessionStoreFlushEager SessionStoreFlushMode = "eager"
)

// ClaudeAgentOptions configures the behavior of Query and Client.
type ClaudeAgentOptions struct {
	// Tools specifies the base set of available built-in tools. It is
	// []string for specific tool names (e.g. []string{"Bash", "Read"}), an
	// empty non-nil []string to disable all built-in tools, *ToolsPreset to
	// use all default Claude Code tools, or nil for the CLI default.
	//
	// To restrict which tools the model may call without being prompted,
	// use AllowedTools instead.
	Tools any `json:"-"` // []string | *ToolsPreset | nil

	// AllowedTools names tools that are auto-allowed without prompting.
	// To restrict which tools are available at all, use Tools.
	//
	// Deprecated: passing "Skill" here is deprecated. Use Skills instead,
	// which configures everything needed, including allowing the Skill tool.
	AllowedTools []string `json:"-"`

	// DisallowedTools names tools removed from the model's context, which
	// cannot be used even if they would otherwise be allowed.
	DisallowedTools []string `json:"-"`

	// SystemPrompt is a string for a custom prompt, *SystemPromptPreset for
	// Claude Code's default prompt (optionally with an append), a
	// *SystemPromptFile to load one from disk, or nil.
	SystemPrompt any `json:"-"` // string | *SystemPromptPreset | *SystemPromptFile | nil

	// McpServers configures MCP servers. It is a
	// map[string]McpServerConfig keyed by server name, or a string path to
	// an MCP config JSON file.
	McpServers any `json:"-"` // map[string]McpServerConfig | string | nil

	// StrictMcpConfig uses only the servers passed via McpServers, ignoring
	// every other MCP configuration the CLI would otherwise load (project
	// .mcp.json, user and global settings, plugin-provided servers).
	StrictMcpConfig bool `json:"-"`

	// PermissionMode selects the permission behavior for the session.
	PermissionMode PermissionMode `json:"-"`

	// PermissionPromptToolName routes permission requests through this MCP
	// tool instead of the default handler. It cannot be combined with
	// CanUseTool.
	PermissionPromptToolName string `json:"-"`

	// CanUseTool handles tool calls that would otherwise prompt the user.
	//
	// It is invoked when the CLI's permission rules evaluate to "ask" — it
	// is the SDK replacement for the interactive permission prompt. It is
	// not invoked for calls already permitted by AllowedTools, by
	// PermissionMode (such as acceptEdits or bypassPermissions), or by
	// permissions.allow rules in settings, since those never reach a
	// prompt. To observe or gate every tool call regardless of permission
	// rules, use a PreToolUse hook instead — though a PreToolUse hook
	// returning an allow decision also skips this callback.
	CanUseTool CanUseToolFunc `json:"-"`

	// ContinueConversation continues the most recent conversation in the
	// current directory instead of starting a new one. It is mutually
	// exclusive with Resume.
	ContinueConversation bool `json:"-"`

	// Resume is the session ID to resume, loading that session's history.
	Resume string `json:"-"`

	// SessionID uses a specific session ID instead of an auto-generated
	// one. It must be a valid UUID, and cannot be combined with
	// ContinueConversation or Resume unless ForkSession is also set.
	SessionID string `json:"-"`

	// ForkSession makes a resumed session fork to a new session ID rather
	// than continuing the previous one. Use it with Resume.
	ForkSession bool `json:"-"`

	// ResumeSessionAt loads the conversation only up to and including the
	// message with this UUID. Use it with Resume, and usually ForkSession,
	// to branch from an earlier point.
	//
	// It accepts any transcript-entry UUID — typically an
	// AssistantMessage.UUID observed live, or a SessionMessage.UUID from
	// GetSessionMessages. See ResumeDropsTurn for how to choose the fork
	// point. For an offline copy truncated at a message, without resuming,
	// see ForkSession.
	ResumeSessionAt string `json:"-"`

	// ResumeDropsTurn is the UUID of the user prompt whose turn a
	// truncating resume intends to discard, used with ResumeSessionAt.
	//
	// When set, the CLI validates at load time that every transcript entry
	// after the ResumeSessionAt point is attributable to that turn, and
	// refuses the resume otherwise — for example when the discarded range
	// contains a queued user message or task notification the session
	// absorbed mid-turn that the caller had not yet observed. A refusal
	// surfaces as a ProcessError whose message contains "Resume rejected by
	// --resume-drops-turn:". Treat it as deterministic: clear the pending
	// fork target and resume plainly rather than retrying.
	//
	// Rule of thumb: set ResumeSessionAt to the last transcript entry of
	// the turn being kept, whatever its type, and ResumeDropsTurn to the
	// prompt UUID of the turn immediately after it. With structured output
	// or end-turn MCP tools, a kept turn ends on entries after its last
	// assistant message, so forking at the assistant UUID is refused by
	// design.
	//
	// A pointer so an empty string can be forwarded deliberately: the CLI
	// then rejects it as malformed rather than the SDK silently disarming
	// a guard the caller believes is armed. Leave nil to keep the
	// unvalidated truncation behavior.
	ResumeDropsTurn *string `json:"-"`

	// MaxTurns caps conversation turns before the query stops. A turn is a
	// user message and an assistant response.
	MaxTurns *int `json:"-"`

	// MaxBudgetUSD caps spend for the query. Exceeding it stops the query
	// with an error_max_budget_usd result.
	MaxBudgetUSD *float64 `json:"-"`

	// Model is the Claude model to use, e.g. "claude-sonnet-4-5". It
	// defaults to the CLI default model.
	Model string `json:"-"`

	// FallbackModel is used if the primary model fails or is unavailable.
	FallbackModel string `json:"-"`

	// Betas enables beta features.
	Betas []SdkBeta `json:"-"`

	// CWD is the working directory for the session. It defaults to the
	// process working directory.
	CWD string `json:"-"`

	// CLIPath is the path to the Claude Code CLI executable. The bundled
	// executable is used when empty.
	CLIPath string `json:"-"`

	// Settings is a path to an additional settings JSON file, or an inline
	// JSON object. It loads into the flag-settings layer, the highest
	// priority among user-controlled settings.
	Settings string `json:"-"`

	// SettingSources controls which filesystem settings to load. Nil loads
	// all sources, matching CLI defaults; an empty non-nil slice disables
	// filesystem settings (SDK isolation mode). It must include
	// SettingSourceProject to load CLAUDE.md files.
	SettingSources []SettingSource `json:"-"`

	// AddDirs lists additional directories Claude can access beyond CWD.
	// Paths should be absolute.
	AddDirs []string `json:"-"`

	// Env sets environment variables for the CLI subprocess. Consumers can
	// identify their app in the User-Agent header via
	// CLAUDE_AGENT_SDK_CLIENT_APP, e.g. "my-app/1.0.0".
	Env map[string]string `json:"-"`

	// ExtraArgs passes additional CLI arguments. Keys are argument names
	// without the leading dashes; an empty value makes a boolean flag.
	ExtraArgs map[string]string `json:"-"`

	// MaxBufferSize caps bytes buffered when reading the CLI's stdout.
	MaxBufferSize int `json:"-"`

	// Stderr receives stderr output from the CLI subprocess, line by line.
	Stderr func(string) `json:"-"`

	// Hooks registers hook callbacks for events during execution.
	//
	// Multiple matchers registered on the same event are dispatched
	// concurrently by the CLI: all hook_callback control requests for an
	// event fire in parallel, not sequentially. Design each hook to be
	// independent.
	//
	// See https://docs.anthropic.com/en/docs/claude-code/hooks.
	Hooks map[HookEvent][]HookMatcher `json:"-"`

	// User is an optional user identifier associated with the session.
	User string `json:"-"`

	// IncludePartialMessages emits partial/streaming message events.
	IncludePartialMessages bool `json:"-"`

	// IncludeHookEvents emits hook lifecycle events into the message
	// stream as HookEventMessage values.
	IncludeHookEvents bool `json:"-"`

	// ForwardSubagentText forwards subagent text and thinking blocks as
	// messages in the stream.
	//
	// By default only tool_use and tool_result blocks from subagents
	// (spawned via the Agent tool) are emitted, as AssistantMessage and
	// UserMessage values whose ParentToolUseID is the spawning Agent
	// tool_use ID — enough for a progress heartbeat. When true, the
	// subagent's text and thinking blocks are forwarded the same way, so
	// consumers can render the full nested transcript.
	ForwardSubagentText bool `json:"-"`

	// Agents programmatically defines custom subagents invokable via the
	// Agent tool, keyed by agent name.
	Agents map[string]AgentDefinition `json:"-"`

	// Skills selects skills to enable for the main session. It is the
	// single place to turn skills on: there is no need to add "Skill" to
	// AllowedTools or set SettingSources, since the SDK does both.
	//
	// A nil value applies no SDK auto-configuration; the CLI's own defaults
	// still apply, so this is not "skills off" — use an empty non-nil
	// []string to suppress every skill from the listing. SkillsAll{}
	// enables every discovered skill. A []string enables only the listed
	// skills, matching the SKILL.md name or directory name, or
	// "plugin:skill" for plugin-qualified skills. Names must be exact:
	// wildcards, delimiters, and surrounding whitespace are rejected at
	// connect time.
	//
	// This is a context filter, not a sandbox: unlisted skills are hidden
	// from the model's listing and rejected by the Skill tool, but their
	// files remain on disk and are reachable via Read and Bash. Do not
	// store secrets in skill files.
	Skills any `json:"-"` // []string | SkillsAll | nil

	// Sandbox isolates command execution. See SandboxSettings.
	Sandbox *SandboxSettings `json:"-"`

	// Plugins loads plugins that provide custom commands, agents, skills,
	// and hooks. Only local plugins are supported.
	Plugins []PluginConfig `json:"-"`

	// MaxThinkingTokens caps tokens for the model's thinking process.
	//
	// Deprecated: use Thinking. On newer models this is treated as on/off
	// (0 disables, any other value is adaptive). For explicit control use
	// ThinkingConfigAdaptive or ThinkingConfigEnabled.
	MaxThinkingTokens *int `json:"-"`

	// Thinking controls Claude's thinking behavior, and takes precedence
	// over the deprecated MaxThinkingTokens.
	Thinking ThinkingConfig `json:"-"`

	// Effort controls how much effort Claude puts into its response.
	Effort Effort `json:"-"`

	// OutputFormat requests structured output matching a schema.
	OutputFormat *OutputFormat `json:"-"`

	// EnableFileCheckpointing backs files up before modification so they
	// can be rewound to their state at any user message via
	// Client.RewindFiles. It cannot be combined with SessionStore.
	EnableFileCheckpointing bool `json:"-"`

	// SessionStore mirrors session transcripts to external storage. When
	// set, every transcript line written locally is also passed to
	// SessionStore.Append, and Resume can materialize from the store when
	// the local file is absent.
	SessionStore SessionStore `json:"-"`

	// SessionStoreFlush controls when mirrored entries are flushed to
	// SessionStore. It defaults to SessionStoreFlushBatched, and is ignored
	// when SessionStore is nil.
	SessionStoreFlush SessionStoreFlushMode `json:"-"`

	// LoadTimeoutMS bounds each SessionStore.Load and ListSubkeys call
	// during resume materialization. If the adapter does not settle within
	// the window the query fails with a clear error instead of hanging. A
	// value of 0 means the default (60s); use a large value to effectively
	// disable it, or LoadTimeoutImmediate for an immediate timeout.
	LoadTimeoutMS int `json:"-"`

	// TaskBudget sets an API-side task budget in tokens, making the model
	// aware of its remaining budget so it can pace tool use.
	TaskBudget *TaskBudget `json:"-"`
}

// LoadTimeoutImmediate is the LoadTimeoutMS value requesting an immediate
// timeout. A zero LoadTimeoutMS selects the default instead, so this
// sentinel expresses "time out at once".
const LoadTimeoutImmediate = -1

// defaultLoadTimeoutMS is used when LoadTimeoutMS is zero.
const defaultLoadTimeoutMS = 60_000

// loadTimeoutMS resolves the effective load timeout in milliseconds.
func (o *ClaudeAgentOptions) loadTimeoutMS() int {
	switch {
	case o.LoadTimeoutMS == 0:
		return defaultLoadTimeoutMS
	case o.LoadTimeoutMS < 0:
		return 0
	default:
		return o.LoadTimeoutMS
	}
}
