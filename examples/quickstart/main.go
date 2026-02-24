// Example: quick start
//
// Demonstrates three common patterns: a basic query, a query with custom
// options, and a query that uses tools.
//
// Usage:
//
//	go run ./examples/quickstart
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	basicExample()
	withOptionsExample()
	withToolsExample()
}

func basicExample() {
	fmt.Println("=== Basic Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages, errFn := agentsdk.Query(ctx, "What is 2 + 2?", nil)
	for msg := range messages {
		if m, ok := msg.(*agentsdk.AssistantMessage); ok {
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}

func withOptionsExample() {
	fmt.Println("=== With Options Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	maxTurns := 1
	opts := &agentsdk.ClaudeAgentOptions{
		SystemPrompt: "You are a helpful assistant that explains things simply.",
		MaxTurns:     &maxTurns,
	}

	messages, errFn := agentsdk.Query(ctx, "Explain what Go is in one sentence.", opts)
	for msg := range messages {
		if m, ok := msg.(*agentsdk.AssistantMessage); ok {
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}

func withToolsExample() {
	fmt.Println("=== With Tools Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		AllowedTools: []string{"Read", "Write"},
		SystemPrompt: "You are a helpful file assistant.",
	}

	messages, errFn := agentsdk.Query(ctx, "Create a file called hello.txt with 'Hello, World!' in it", opts)
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
				fmt.Printf("\nCost: $%.4f\n", *m.TotalCostUSD)
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}
