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
