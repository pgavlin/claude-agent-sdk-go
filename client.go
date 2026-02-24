package agentsdk

import (
	"context"
	"fmt"
	"iter"
)

// Client provides a bidirectional, interactive interface to the Claude CLI.
// It supports sending multiple queries, interrupting, and changing settings
// during a session.
type Client struct {
	opts      *ClaudeAgentOptions
	transport Transport
	query     *protocolQuery
	connected bool

	serverInfo map[string]any
}

// NewClient creates a new Client with the default subprocess transport.
func NewClient(opts *ClaudeAgentOptions) *Client {
	if opts == nil {
		opts = &ClaudeAgentOptions{}
	}

	// If canUseTool is set, automatically set permission prompt tool
	if opts.CanUseTool != nil && opts.PermissionPromptToolName == "" {
		opts.PermissionPromptToolName = "stdio"
	}

	return &Client{
		opts:      opts,
		transport: NewSubprocessTransport(opts, "sdk-go-client"),
	}
}

// NewClientWithTransport creates a new Client with the given transport.
func NewClientWithTransport(opts *ClaudeAgentOptions, t Transport) *Client {
	if opts == nil {
		opts = &ClaudeAgentOptions{}
	}

	if opts.CanUseTool != nil && opts.PermissionPromptToolName == "" {
		opts.PermissionPromptToolName = "stdio"
	}

	return &Client{
		opts:      opts,
		transport: t,
	}
}

// Connect establishes the connection to the CLI and performs initialization.
func (c *Client) Connect(ctx context.Context) error {
	if err := c.transport.Connect(ctx); err != nil {
		return err
	}

	c.query = newProtocolQuery(c.transport, c.opts)
	c.query.Start(ctx)

	initResult, err := c.query.Initialize(ctx)
	if err != nil {
		c.transport.Close()
		return err
	}

	c.serverInfo = initResult
	c.connected = true
	return nil
}

// Messages returns a channel of messages from the CLI.
// Messages are available after Connect returns.
func (c *Client) Messages() <-chan Message {
	return c.query.Messages()
}

// SendQuery sends a user message (new query) to the CLI.
func (c *Client) SendQuery(ctx context.Context, prompt string, sessionID string) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.WriteUserMessage(ctx, prompt, sessionID)
}

// Interrupt sends an interrupt request to the CLI.
func (c *Client) Interrupt(ctx context.Context) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.Interrupt(ctx)
}

// SetPermissionMode changes the CLI's permission mode.
func (c *Client) SetPermissionMode(ctx context.Context, mode PermissionMode) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.SetPermissionMode(ctx, mode)
}

// SetModel changes the active model.
func (c *Client) SetModel(ctx context.Context, model string) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.SetModel(ctx, model)
}

// RewindFiles rewinds file changes to a specific message.
func (c *Client) RewindFiles(ctx context.Context, msgID string) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.RewindFiles(ctx, msgID)
}

// McpStatus queries the MCP server status.
func (c *Client) McpStatus(ctx context.Context) (map[string]any, error) {
	if !c.connected {
		return nil, fmt.Errorf("client not connected")
	}
	return c.query.McpStatus(ctx)
}

// ServerInfo returns the initialization result from the CLI.
func (c *Client) ServerInfo() map[string]any {
	return c.serverInfo
}

// ReceiveResponse returns an iterator that yields messages until a ResultMessage
// is received, then stops.
func (c *Client) ReceiveResponse() iter.Seq[Message] {
	return func(yield func(Message) bool) {
		for msg := range c.query.Messages() {
			if !yield(msg) {
				return
			}
			if _, ok := msg.(*ResultMessage); ok {
				return
			}
		}
	}
}

// Close disconnects from the CLI and cleans up resources.
func (c *Client) Close() error {
	c.connected = false
	return c.transport.Close()
}
