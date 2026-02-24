// Example: MCP server configuration
//
// Demonstrates how to configure MCP (Model Context Protocol) servers
// with Claude. The Go SDK supports stdio, SSE, and HTTP transports for
// connecting to external MCP servers.
//
// Note: Unlike the Python SDK, the Go SDK does not support in-process
// MCP servers. MCP servers must be external processes or services.
//
// Usage:
//
//	go run ./examples/mcpserver
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	stdioExample()
	sseExample()
}

func stdioExample() {
	fmt.Println("=== Stdio MCP Server Example ===")
	fmt.Println("Configuring a filesystem MCP server via stdio transport.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Configure an MCP server using stdio transport.
	// This example uses npx to run the filesystem MCP server.
	// The server must be installed: npm install -g @anthropic-ai/mcp-server-filesystem
	opts := &agentsdk.ClaudeAgentOptions{
		McpServers: map[string]agentsdk.McpServerConfig{
			"filesystem": agentsdk.McpStdioServerConfig{
				Type:    "stdio",
				Command: "npx",
				Args:    []string{"-y", "@anthropic-ai/mcp-server-filesystem", "/tmp"},
			},
		},
		AllowedTools: []string{
			"mcp__filesystem__read_file",
			"mcp__filesystem__list_directory",
		},
	}

	messages, errFn := agentsdk.Query(ctx, "List the files in /tmp using the filesystem MCP server", opts)
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			if m.TotalCostUSD != nil && *m.TotalCostUSD > 0 {
				fmt.Printf("Cost: $%.6f\n", *m.TotalCostUSD)
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}

func sseExample() {
	fmt.Println("=== SSE MCP Server Example (configuration only) ===")
	fmt.Println("Shows how to configure an SSE-based MCP server.")
	fmt.Println()

	// This demonstrates the configuration types; it won't actually connect
	// unless there's a server running at the specified URL.

	// SSE transport:
	sseConfig := agentsdk.McpSSEServerConfig{
		Type: "sse",
		URL:  "http://localhost:8080/sse",
		Headers: map[string]string{
			"Authorization": "Bearer my-token",
		},
	}
	fmt.Println("SSE server configured with:")
	fmt.Printf("  Type: %s\n", sseConfig.Type)
	fmt.Printf("  URL: %s\n", sseConfig.URL)
	fmt.Println()

	// HTTP transport:
	httpConfig := agentsdk.McpHttpServerConfig{
		Type: "http",
		URL:  "http://localhost:8080/mcp",
	}
	fmt.Println("HTTP server configured with:")
	fmt.Printf("  Type: %s\n", httpConfig.Type)
	fmt.Printf("  URL: %s\n", httpConfig.URL)
	fmt.Println()
}
