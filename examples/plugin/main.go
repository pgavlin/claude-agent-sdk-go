// Example: plugins
//
// Demonstrates how to use plugins with Claude Code SDK. Plugins allow
// extending Claude Code with custom commands, agents, skills, and hooks.
// This example loads a local demo plugin and verifies it in the system
// message.
//
// Usage:
//
//	go run ./examples/plugin
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	fmt.Println("=== Plugin Example ===")
	fmt.Println()

	// Get the path to the demo plugin relative to this file
	_, thisFile, _, _ := runtime.Caller(0)
	pluginPath := filepath.Join(filepath.Dir(thisFile), "plugins", "demo-plugin")

	maxTurns := 1
	opts := &agentsdk.ClaudeAgentOptions{
		Plugins: []agentsdk.PluginConfig{
			{
				Type: "local",
				Path: pluginPath,
			},
		},
		MaxTurns: &maxTurns,
	}

	fmt.Printf("Loading plugin from: %s\n\n", pluginPath)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages, errFn := agentsdk.Query(ctx, "Hello!", opts)
	for msg := range messages {
		if m, ok := msg.(*agentsdk.SystemMessage); ok && m.Subtype == "init" {
			fmt.Println("System initialized!")
			fmt.Printf("System message data keys: %v\n\n", keys(m.Data))

			if plugins, ok := m.Data["plugins"].([]any); ok && len(plugins) > 0 {
				fmt.Println("Plugins loaded:")
				for _, p := range plugins {
					if pm, ok := p.(map[string]any); ok {
						fmt.Printf("  - %s (path: %s)\n", pm["name"], pm["path"])
					}
				}
			} else {
				fmt.Println("Note: Plugin was passed via CLI but may not appear in system message.")
				fmt.Printf("Plugin path configured: %s\n", pluginPath)
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Plugin successfully configured!")
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
