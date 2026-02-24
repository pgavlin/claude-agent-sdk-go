// Example: filesystem-based agents
//
// Demonstrates loading agents defined in .claude/agents/ files using the
// SettingSources option. Unlike inline AgentDefinition objects, these agents
// are loaded from markdown files on disk.
//
// Usage:
//
//	go run ./examples/filesystemagents
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	fmt.Println("=== Filesystem Agents Example ===")
	fmt.Println("Testing: SettingSources=[\"project\"] with .claude/agents/ directory")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		SettingSources: []agentsdk.SettingSource{
			agentsdk.SettingSourceProject,
		},
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Check server info for loaded agents
	serverInfo := client.ServerInfo()
	if agents, ok := serverInfo["agents"].([]any); ok {
		fmt.Printf("Agents loaded: %d\n", len(agents))
		for _, a := range agents {
			switch v := a.(type) {
			case string:
				fmt.Printf("  - %s\n", v)
			case map[string]any:
				if name, ok := v["name"].(string); ok {
					fmt.Printf("  - %s\n", name)
				}
			}
		}
	}

	if err := client.SendQuery(ctx, "Say hello in exactly 3 words", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		os.Exit(1)
	}

	var messageTypes []string
	for msg := range client.ReceiveResponse() {
		switch m := msg.(type) {
		case *agentsdk.SystemMessage:
			messageTypes = append(messageTypes, "SystemMessage")
			if m.Subtype == "init" {
				fmt.Printf("Init message received.\n")
			}
		case *agentsdk.AssistantMessage:
			messageTypes = append(messageTypes, "AssistantMessage")
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Assistant: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			messageTypes = append(messageTypes, "ResultMessage")
			cost := 0.0
			if m.TotalCostUSD != nil {
				cost = *m.TotalCostUSD
			}
			fmt.Printf("Result: subtype=%s, cost=$%.4f\n", m.Subtype, cost)
		}
	}

	fmt.Println()
	fmt.Println("=== Summary ===")
	fmt.Printf("Message types received: %v\n", messageTypes)
	fmt.Printf("Total messages: %d\n", len(messageTypes))
}
