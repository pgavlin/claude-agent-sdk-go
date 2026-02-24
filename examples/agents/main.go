// Example: custom agents
//
// Demonstrates how to define and use custom agents with specific tools,
// prompts, and models using AgentDefinition.
//
// Usage:
//
//	go run ./examples/agents
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	codeReviewerExample()
	documentationWriterExample()
	multipleAgentsExample()
}

func codeReviewerExample() {
	fmt.Println("=== Code Reviewer Agent Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		Agents: map[string]agentsdk.AgentDefinition{
			"code-reviewer": {
				Description: "Reviews code for best practices and potential issues",
				Prompt: "You are a code reviewer. Analyze code for bugs, performance issues, " +
					"security vulnerabilities, and adherence to best practices. " +
					"Provide constructive feedback.",
				Tools: []string{"Read", "Grep"},
				Model: "sonnet",
			},
		},
	}

	messages, errFn := agentsdk.Query(
		ctx,
		"Use the code-reviewer agent to review the code in main.go",
		opts,
	)
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

func documentationWriterExample() {
	fmt.Println("=== Documentation Writer Agent Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		Agents: map[string]agentsdk.AgentDefinition{
			"doc-writer": {
				Description: "Writes comprehensive documentation",
				Prompt: "You are a technical documentation expert. Write clear, comprehensive " +
					"documentation with examples. Focus on clarity and completeness.",
				Tools: []string{"Read", "Write", "Edit"},
				Model: "sonnet",
			},
		},
	}

	messages, errFn := agentsdk.Query(
		ctx,
		"Use the doc-writer agent to explain what AgentDefinition is used for",
		opts,
	)
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

func multipleAgentsExample() {
	fmt.Println("=== Multiple Agents Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		Agents: map[string]agentsdk.AgentDefinition{
			"analyzer": {
				Description: "Analyzes code structure and patterns",
				Prompt:      "You are a code analyzer. Examine code structure, patterns, and architecture.",
				Tools:       []string{"Read", "Grep", "Glob"},
			},
			"tester": {
				Description: "Creates and runs tests",
				Prompt:      "You are a testing expert. Write comprehensive tests and ensure code quality.",
				Tools:       []string{"Read", "Write", "Bash"},
				Model:       "sonnet",
			},
		},
		SettingSources: []agentsdk.SettingSource{
			agentsdk.SettingSourceUser,
			agentsdk.SettingSourceProject,
		},
	}

	messages, errFn := agentsdk.Query(
		ctx,
		"Use the analyzer agent to find all Go files in the examples/ directory",
		opts,
	)
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
