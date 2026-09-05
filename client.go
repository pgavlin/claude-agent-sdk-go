package agentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"path/filepath"
)

// Client provides a bidirectional, interactive interface to the Claude CLI.
// It supports sending multiple queries, interrupting, and changing settings
// during a session.
type Client struct {
	opts      *ClaudeAgentOptions
	transport Transport
	query     *protocolQuery
	mirror    *transcriptMirrorBatcher
	connected bool

	// ownsTransport records that the client built the default subprocess
	// transport itself, so it may rebuild it after a store-backed resume
	// repoints the options at a temporary config directory.
	ownsTransport bool

	// materialized holds the temporary config directory a store-backed
	// resume wrote, to be cleaned up on Close.
	materialized *MaterializedResume

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
		opts:          opts,
		transport:     NewSubprocessTransport(opts, "sdk-go-client"),
		ownsTransport: true,
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
	if err := configureCanUseTool(c.opts); err != nil {
		return err
	}
	if warning := CanUseToolShadowedWarning(c.opts); warning != "" {
		log.Print("agentsdk: " + warning)
	}

	// A store-backed resume has no local transcript to resume from, so
	// materialize one into a temporary config directory and repoint the
	// options at it before spawning.
	materialized, err := MaterializeResumeSession(ctx, c.opts)
	if err != nil {
		return err
	}
	if materialized != nil {
		c.materialized = materialized
		c.opts = ApplyMaterializedOptions(c.opts, materialized)
		if c.ownsTransport {
			c.transport = NewSubprocessTransport(c.opts, "sdk-go-client")
		}
	}

	if err := c.transport.Connect(ctx); err != nil {
		c.cleanupMaterialized()
		return err
	}

	c.query = newProtocolQuery(c.transport, c.opts)

	// Mirror transcripts to the configured store. The batcher receives the
	// transcript_mirror frames the read loop peels off stdout.
	if c.opts.SessionStore != nil {
		q := c.query
		// Resolve the projects directory to the materialized temporary
		// directory when there is one, so file-path-to-key resolution
		// matches what the subprocess actually writes.
		mirrorProjectsDir := projectsDir(c.opts.Env)
		if c.materialized != nil {
			mirrorProjectsDir = filepath.Join(c.materialized.ConfigDir, "projects")
		}
		c.mirror = newTranscriptMirrorBatcher(
			c.opts.SessionStore,
			mirrorProjectsDir,
			c.opts.SessionStoreFlush,
			func(key *SessionKey, message string) { q.reportMirrorError(key, message) },
		)
		c.query.setMirrorBatcher(c.mirror)
	}

	c.query.Start(ctx)

	initResult, err := c.query.Initialize(ctx)
	if err != nil {
		c.transport.Close()
		c.cleanupMaterialized()
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

// McpStatus queries the MCP server status as a raw map.
//
// Prefer GetMcpStatus, which decodes the same response into typed values.
func (c *Client) McpStatus(ctx context.Context) (map[string]any, error) {
	if !c.connected {
		return nil, fmt.Errorf("client not connected")
	}
	return c.query.McpStatus(ctx)
}

// GetMcpStatus returns the status of every configured MCP server.
func (c *Client) GetMcpStatus(ctx context.Context) (*McpStatusResponse, error) {
	raw, err := c.McpStatus(ctx)
	if err != nil {
		return nil, err
	}
	var status McpStatusResponse
	if err := decodeControlResponse(raw, &status); err != nil {
		return nil, fmt.Errorf("failed to decode MCP status response: %w", err)
	}
	return &status, nil
}

// GetContextUsage returns a breakdown of the current context window usage by
// category, matching the data the CLI's /context command shows.
func (c *Client) GetContextUsage(ctx context.Context) (*ContextUsageResponse, error) {
	if !c.connected {
		return nil, fmt.Errorf("client not connected")
	}
	raw, err := c.query.GetContextUsage(ctx)
	if err != nil {
		return nil, err
	}
	var usage ContextUsageResponse
	if err := decodeControlResponse(raw, &usage); err != nil {
		return nil, fmt.Errorf("failed to decode context usage response: %w", err)
	}
	return &usage, nil
}

// ReconnectMcpServer reconnects a disconnected or failed MCP server.
func (c *Client) ReconnectMcpServer(ctx context.Context, serverName string) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.ReconnectMcpServer(ctx, serverName)
}

// ToggleMcpServer enables or disables an MCP server.
func (c *Client) ToggleMcpServer(ctx context.Context, serverName string, enabled bool) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.ToggleMcpServer(ctx, serverName, enabled)
}

// StopTask stops a running task, identified by the task ID carried on
// TaskStartedMessage and TaskNotificationMessage.
func (c *Client) StopTask(ctx context.Context, taskID string) error {
	if !c.connected {
		return fmt.Errorf("client not connected")
	}
	return c.query.StopTask(ctx, taskID)
}

// decodeControlResponse re-encodes a control response map into a typed
// value. The control protocol hands back generic maps, and round-tripping
// through JSON keeps the field mapping in the struct tags.
func decodeControlResponse(raw map[string]any, out any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, out)
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
//
// When a SessionStore is configured, pending transcript entries are flushed
// before teardown so an immediate disconnect does not drop the current turn.
func (c *Client) Close() error {
	c.connected = false
	if c.mirror != nil {
		c.mirror.close()
	}
	err := c.transport.Close()
	c.cleanupMaterialized()
	return err
}

// cleanupMaterialized removes the temporary config directory a store-backed
// resume created, if any.
func (c *Client) cleanupMaterialized() {
	if c.materialized != nil {
		c.materialized.Cleanup()
		c.materialized = nil
	}
}
