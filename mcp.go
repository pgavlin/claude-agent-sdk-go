package agentsdk

// McpServerConfig is a sealed interface for MCP server configurations.
type McpServerConfig interface {
	mcpServerType() string
}

// McpStdioServerConfig configures an MCP server using stdio transport.
type McpStdioServerConfig struct {
	Type    string            `json:"type,omitempty"` // "stdio"
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (McpStdioServerConfig) mcpServerType() string { return "stdio" }

// McpSSEServerConfig configures an MCP server using Server-Sent Events.
type McpSSEServerConfig struct {
	Type    string            `json:"type"` // "sse"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (McpSSEServerConfig) mcpServerType() string { return "sse" }

// McpHttpServerConfig configures an MCP server using HTTP transport.
type McpHttpServerConfig struct {
	Type    string            `json:"type"` // "http"
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (McpHttpServerConfig) mcpServerType() string { return "http" }

// McpSdkServerConfigStatus is an SDK MCP server config as it appears in
// status responses. Unlike an in-process SDK server config it carries only
// serializable fields.
type McpSdkServerConfigStatus struct {
	Type string `json:"type"` // "sdk"
	Name string `json:"name"`
}

func (McpSdkServerConfigStatus) mcpServerType() string { return "sdk" }

// McpClaudeAIProxyServerConfig describes a server proxied through Claude.ai.
// It is output-only and appears in status responses.
type McpClaudeAIProxyServerConfig struct {
	Type string `json:"type"` // "claudeai-proxy"
	URL  string `json:"url"`
	ID   string `json:"id"`
}

func (McpClaudeAIProxyServerConfig) mcpServerType() string { return "claudeai-proxy" }

// McpToolAnnotations describes a tool's properties as reported in MCP server
// status. Field names follow the CLI's camelCase wire format.
type McpToolAnnotations struct {
	ReadOnly    *bool `json:"readOnly,omitempty"`
	Destructive *bool `json:"destructive,omitempty"`
	OpenWorld   *bool `json:"openWorld,omitempty"`
}

// McpToolInfo describes a tool provided by an MCP server.
type McpToolInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Annotations *McpToolAnnotations `json:"annotations,omitempty"`
}

// McpServerInfo is the server info from the MCP initialize handshake,
// available when the server is connected.
type McpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// McpServerConnectionStatus is the connection status of an MCP server.
type McpServerConnectionStatus string

const (
	// McpServerConnected indicates the server is connected.
	McpServerConnected McpServerConnectionStatus = "connected"
	// McpServerFailed indicates the connection failed.
	McpServerFailed McpServerConnectionStatus = "failed"
	// McpServerNeedsAuth indicates the server requires authentication.
	McpServerNeedsAuth McpServerConnectionStatus = "needs-auth"
	// McpServerPending indicates the connection is still being established.
	McpServerPending McpServerConnectionStatus = "pending"
	// McpServerDisabled indicates the server is disabled.
	McpServerDisabled McpServerConnectionStatus = "disabled"
)

// McpServerStatus describes an MCP server connection, as returned by
// Client.GetMcpStatus in the McpServers list.
type McpServerStatus struct {
	// Name is the server name as configured.
	Name string `json:"name"`

	// Status is the current connection status.
	Status McpServerConnectionStatus `json:"status"`

	// ServerInfo is populated from the MCP handshake when connected.
	ServerInfo *McpServerInfo `json:"serverInfo,omitempty"`

	// Error is set when Status is McpServerFailed.
	Error string `json:"error,omitempty"`

	// Config is the raw server configuration, including the URL for
	// HTTP and SSE servers. Its shape varies with the server type, so it
	// is left as a map rather than a typed union.
	Config map[string]any `json:"config,omitempty"`

	// Scope is the configuration scope, e.g. project, user, local,
	// claudeai, or managed.
	Scope string `json:"scope,omitempty"`

	// Tools lists the tools this server provides, available when connected.
	Tools []McpToolInfo `json:"tools,omitempty"`
}

// McpStatusResponse is the response from Client.GetMcpStatus.
type McpStatusResponse struct {
	McpServers []McpServerStatus `json:"mcpServers"`
}

// ContextUsageCategory is a single context usage category, such as the
// system prompt, tools, or messages.
type ContextUsageCategory struct {
	Name       string `json:"name"`
	Tokens     int    `json:"tokens"`
	Color      string `json:"color"`
	IsDeferred *bool  `json:"isDeferred,omitempty"`
}

// ContextUsageResponse is the response from Client.GetContextUsage. It
// breaks down current context window usage by category, matching the data
// the CLI's /context command shows.
type ContextUsageResponse struct {
	// Categories breaks token usage down by category.
	Categories []ContextUsageCategory `json:"categories"`

	// TotalTokens is the number of tokens currently in the context window.
	TotalTokens int `json:"totalTokens"`

	// MaxTokens is the effective maximum, which the autocompact buffer may
	// reduce below RawMaxTokens.
	MaxTokens int `json:"maxTokens"`

	// RawMaxTokens is the raw model context window size.
	RawMaxTokens int `json:"rawMaxTokens"`

	// Percentage is the share of the context window used, 0 to 100.
	Percentage float64 `json:"percentage"`

	// Model is the model the usage is calculated for.
	Model string `json:"model"`

	// IsAutoCompactEnabled reports whether autocompact is on for the session.
	IsAutoCompactEnabled bool `json:"isAutoCompactEnabled"`

	// MemoryFiles lists CLAUDE.md and memory files loaded, with path, type,
	// and token counts.
	MemoryFiles []map[string]any `json:"memoryFiles,omitempty"`

	// McpTools lists MCP tools with name, serverName, tokens, and isLoaded.
	McpTools []map[string]any `json:"mcpTools,omitempty"`

	// Agents lists agent definitions with agentType, source, and token counts.
	Agents []map[string]any `json:"agents,omitempty"`

	// GridRows is the visual grid the CLI context display uses.
	GridRows [][]map[string]any `json:"gridRows,omitempty"`

	// AutoCompactThreshold is the token threshold at which autocompact
	// triggers.
	AutoCompactThreshold *int `json:"autoCompactThreshold,omitempty"`

	// DeferredBuiltinTools lists built-in tools deferred from the initial
	// tool list.
	DeferredBuiltinTools []map[string]any `json:"deferredBuiltinTools,omitempty"`

	// SystemTools lists built-in tools with name and token counts.
	SystemTools []map[string]any `json:"systemTools,omitempty"`

	// SystemPromptSections lists system prompt sections with name and
	// token counts.
	SystemPromptSections []map[string]any `json:"systemPromptSections,omitempty"`

	// SlashCommands summarizes slash command usage.
	SlashCommands map[string]any `json:"slashCommands,omitempty"`

	// Skills summarizes skill usage, with a frontmatter breakdown.
	Skills map[string]any `json:"skills,omitempty"`

	// MessageBreakdown details message tokens by type, such as tool calls
	// and results.
	MessageBreakdown map[string]any `json:"messageBreakdown,omitempty"`

	// APIUsage is cumulative API usage for the session.
	APIUsage map[string]any `json:"apiUsage,omitempty"`
}
