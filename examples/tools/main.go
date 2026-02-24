// Example: tools option
//
// Demonstrates the Tools option for controlling which tools are available
// to Claude: an explicit list of tools, an empty list (disabling all tools),
// and a preset that includes all default Claude Code tools.
//
// Usage:
//
//	go run ./examples/tools
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	toolsArrayExample()
	toolsEmptyArrayExample()
	toolsPresetExample()
}

func toolsArrayExample() {
	fmt.Println("=== Tools Array Example ===")
	fmt.Println("Setting Tools=[\"Read\", \"Glob\", \"Grep\"]")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	maxTurns := 1
	opts := &agentsdk.ClaudeAgentOptions{
		Tools:    []string{"Read", "Glob", "Grep"},
		MaxTurns: &maxTurns,
	}

	messages, errFn := agentsdk.Query(
		ctx,
		"What tools do you have available? Just list them briefly.",
		opts,
	)
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.SystemMessage:
			if m.Subtype == "init" {
				if tools, ok := m.Data["tools"].([]any); ok {
					fmt.Printf("Tools from system message: %v\n\n", tools)
				}
			}
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			if m.TotalCostUSD != nil {
				fmt.Printf("\nCost: $%.4f\n", *m.TotalCostUSD)
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}

func toolsEmptyArrayExample() {
	fmt.Println("=== Tools Empty Array Example ===")
	fmt.Println("Setting Tools=[] (disables all built-in tools)")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	maxTurns := 1
	opts := &agentsdk.ClaudeAgentOptions{
		Tools:    []string{},
		MaxTurns: &maxTurns,
	}

	messages, errFn := agentsdk.Query(
		ctx,
		"What tools do you have available? Just list them briefly.",
		opts,
	)
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.SystemMessage:
			if m.Subtype == "init" {
				if tools, ok := m.Data["tools"].([]any); ok {
					fmt.Printf("Tools from system message: %v\n\n", tools)
				}
			}
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			if m.TotalCostUSD != nil {
				fmt.Printf("\nCost: $%.4f\n", *m.TotalCostUSD)
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}

func toolsPresetExample() {
	fmt.Println("=== Tools Preset Example ===")
	fmt.Println("Setting Tools=ToolsPreset{\"preset\", \"claude_code\"}")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	maxTurns := 1
	opts := &agentsdk.ClaudeAgentOptions{
		Tools: &agentsdk.ToolsPreset{
			Type:   "preset",
			Preset: "claude_code",
		},
		MaxTurns: &maxTurns,
	}

	messages, errFn := agentsdk.Query(
		ctx,
		"What tools do you have available? Just list them briefly.",
		opts,
	)
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.SystemMessage:
			if m.Subtype == "init" {
				if tools, ok := m.Data["tools"].([]any); ok {
					fmt.Printf("Tools from system message (%d tools): ", len(tools))
					if len(tools) > 5 {
						fmt.Printf("%v...\n\n", tools[:5])
					} else {
						fmt.Printf("%v\n\n", tools)
					}
				}
			}
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			if m.TotalCostUSD != nil {
				fmt.Printf("\nCost: $%.4f\n", *m.TotalCostUSD)
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}
