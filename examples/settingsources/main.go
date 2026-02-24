// Example: setting sources
//
// Demonstrates how to use the SettingSources option to control which
// settings are loaded. Settings determine available slash commands,
// agents, and other configurations.
//
// When SettingSources is not provided (nil), NO settings are loaded by
// default, creating an isolated environment.
//
// Usage:
//
//	go run ./examples/settingsources            # list examples
//	go run ./examples/settingsources all        # run all
//	go run ./examples/settingsources default    # run specific
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func extractSlashCommands(m *agentsdk.SystemMessage) []any {
	if m.Subtype == "init" {
		if commands, ok := m.Data["slash_commands"].([]any); ok {
			return commands
		}
	}
	return nil
}

func exampleDefault() {
	fmt.Println("=== Default Behavior Example ===")
	fmt.Println("Setting sources: nil (default)")
	fmt.Println("Expected: No custom slash commands will be available")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := agentsdk.NewClient(&agentsdk.ClaudeAgentOptions{})
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	if err := client.SendQuery(ctx, "What is 2 + 2?", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	for msg := range client.ReceiveResponse() {
		if m, ok := msg.(*agentsdk.SystemMessage); ok {
			commands := extractSlashCommands(m)
			fmt.Printf("Available slash commands: %v\n", commands)
			break
		}
	}
	fmt.Println()
}

func exampleUserOnly() {
	fmt.Println("=== User Settings Only Example ===")
	fmt.Println("Setting sources: [\"user\"]")
	fmt.Println("Expected: Project slash commands will NOT be available")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		SettingSources: []agentsdk.SettingSource{
			agentsdk.SettingSourceUser,
		},
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	if err := client.SendQuery(ctx, "What is 2 + 2?", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	for msg := range client.ReceiveResponse() {
		if m, ok := msg.(*agentsdk.SystemMessage); ok {
			commands := extractSlashCommands(m)
			fmt.Printf("Available slash commands: %v\n", commands)
			break
		}
	}
	fmt.Println()
}

func exampleProjectAndUser() {
	fmt.Println("=== Project + User Settings Example ===")
	fmt.Println("Setting sources: [\"user\", \"project\"]")
	fmt.Println("Expected: Project slash commands WILL be available")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		SettingSources: []agentsdk.SettingSource{
			agentsdk.SettingSourceUser,
			agentsdk.SettingSourceProject,
		},
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	if err := client.SendQuery(ctx, "What is 2 + 2?", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	for msg := range client.ReceiveResponse() {
		if m, ok := msg.(*agentsdk.SystemMessage); ok {
			commands := extractSlashCommands(m)
			fmt.Printf("Available slash commands: %v\n", commands)
			break
		}
	}
	fmt.Println()
}

func main() {
	examples := map[string]func(){
		"default":          exampleDefault,
		"user_only":        exampleUserOnly,
		"project_and_user": exampleProjectAndUser,
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run ./examples/settingsources <example_name>")
		fmt.Println()
		fmt.Println("Available examples:")
		fmt.Println("  all              - Run all examples")
		fmt.Println("  default          - No settings loaded (default)")
		fmt.Println("  user_only        - Only user settings")
		fmt.Println("  project_and_user - Both project and user settings")
		os.Exit(0)
	}

	fmt.Println("Starting Claude SDK Setting Sources Examples...")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println()

	name := os.Args[1]
	if name == "all" {
		for _, fn := range []func(){exampleDefault, exampleUserOnly, exampleProjectAndUser} {
			fn()
			fmt.Println(strings.Repeat("-", 50))
			fmt.Println()
		}
		return
	}

	fn, ok := examples[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: Unknown example %q\n", name)
		os.Exit(1)
	}
	fn()
}
