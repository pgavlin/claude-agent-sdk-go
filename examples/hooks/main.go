// Example: hooks
//
// Demonstrates various hook patterns using the Hooks parameter in
// ClaudeAgentOptions. Hooks allow intercepting tool use, prompt submission,
// and other events to add custom behavior.
//
// Usage:
//
//	go run ./examples/hooks            # list examples
//	go run ./examples/hooks all        # run all
//	go run ./examples/hooks PreToolUse # run specific example
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func displayMessage(msg agentsdk.Message) {
	switch m := msg.(type) {
	case *agentsdk.AssistantMessage:
		for _, block := range m.Content {
			if tb, ok := block.(agentsdk.TextBlock); ok {
				fmt.Printf("Claude: %s\n", tb.Text)
			}
		}
	case *agentsdk.ResultMessage:
		fmt.Println("Result ended")
	}
}

// checkBashCommand prevents certain bash commands from being executed.
func checkBashCommand(_ context.Context, input agentsdk.HookInput, _ string) (*agentsdk.HookOutput, error) {
	pre, ok := input.(agentsdk.PreToolUseHookInput)
	if !ok || pre.ToolName != "Bash" {
		return &agentsdk.HookOutput{}, nil
	}

	command, _ := pre.ToolInput["command"].(string)
	blockPatterns := []string{"foo.sh"}

	for _, pattern := range blockPatterns {
		if strings.Contains(command, pattern) {
			fmt.Printf("  Blocked command: %s\n", command)
			return &agentsdk.HookOutput{
				HookSpecificOutput: map[string]any{
					"hookEventName":            "PreToolUse",
					"permissionDecision":       "deny",
					"permissionDecisionReason": fmt.Sprintf("Command contains invalid pattern: %s", pattern),
				},
			}, nil
		}
	}

	return &agentsdk.HookOutput{}, nil
}

// addCustomInstructions adds custom instructions when a session starts.
func addCustomInstructions(_ context.Context, _ agentsdk.HookInput, _ string) (*agentsdk.HookOutput, error) {
	return &agentsdk.HookOutput{
		HookSpecificOutput: map[string]any{
			"hookEventName":     "SessionStart",
			"additionalContext": "My favorite color is hot pink",
		},
	}, nil
}

// reviewToolOutput reviews tool output and provides additional context.
func reviewToolOutput(_ context.Context, input agentsdk.HookInput, _ string) (*agentsdk.HookOutput, error) {
	post, ok := input.(agentsdk.PostToolUseHookInput)
	if !ok {
		return &agentsdk.HookOutput{}, nil
	}

	toolResponse := fmt.Sprintf("%v", post.ToolResponse)
	if strings.Contains(strings.ToLower(toolResponse), "error") {
		return &agentsdk.HookOutput{
			SystemMessage: "The command produced an error",
			Reason:        "Tool execution failed - consider checking the command syntax",
			HookSpecificOutput: map[string]any{
				"hookEventName":     "PostToolUse",
				"additionalContext": "The command encountered an error. You may want to try a different approach.",
			},
		}, nil
	}

	return &agentsdk.HookOutput{}, nil
}

func examplePreToolUse() {
	fmt.Println("=== PreToolUse Example ===")
	fmt.Println("This example demonstrates how PreToolUse can block some bash commands but not others.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		AllowedTools: []string{"Bash"},
		Hooks: map[agentsdk.HookEvent][]agentsdk.HookMatcher{
			agentsdk.HookEventPreToolUse: {
				{
					Matcher: "Bash",
					Hooks:   []agentsdk.HookCallback{checkBashCommand},
				},
			},
		},
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	// Test 1: Command with forbidden pattern (will be blocked)
	fmt.Println("Test 1: Trying a command that our PreToolUse hook should block...")
	fmt.Println("User: Run the bash command: ./foo.sh --help")
	if err := client.SendQuery(ctx, "Run the bash command: ./foo.sh --help", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println()

	// Test 2: Safe command that should work
	fmt.Println("Test 2: Trying a command that our PreToolUse hook should allow...")
	fmt.Println("User: Run the bash command: echo 'Hello from hooks example!'")
	if err := client.SendQuery(ctx, "Run the bash command: echo 'Hello from hooks example!'", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()
}

func exampleUserPromptSubmit() {
	fmt.Println("=== UserPromptSubmit Example ===")
	fmt.Println("This example shows how a UserPromptSubmit hook can add context.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		Hooks: map[agentsdk.HookEvent][]agentsdk.HookMatcher{
			agentsdk.HookEventUserPromptSubmit: {
				{
					Hooks: []agentsdk.HookCallback{addCustomInstructions},
				},
			},
		},
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("User: What's my favorite color?")
	if err := client.SendQuery(ctx, "What's my favorite color?", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()
}

func examplePostToolUse() {
	fmt.Println("=== PostToolUse Example ===")
	fmt.Println("This example shows how PostToolUse can provide feedback with reason and systemMessage.")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		AllowedTools: []string{"Bash"},
		Hooks: map[agentsdk.HookEvent][]agentsdk.HookMatcher{
			agentsdk.HookEventPostToolUse: {
				{
					Matcher: "Bash",
					Hooks:   []agentsdk.HookCallback{reviewToolOutput},
				},
			},
		},
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("User: Run a command that will produce an error: ls /nonexistent_directory")
	if err := client.SendQuery(ctx, "Run this command: ls /nonexistent_directory", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()
}

func main() {
	examples := map[string]func(){
		"PreToolUse":       examplePreToolUse,
		"UserPromptSubmit": exampleUserPromptSubmit,
		"PostToolUse":      examplePostToolUse,
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run ./examples/hooks <example_name>")
		fmt.Println()
		fmt.Println("Available examples:")
		fmt.Println("  all              - Run all examples")
		fmt.Println("  PreToolUse       - Block commands using PreToolUse hook")
		fmt.Println("  UserPromptSubmit - Add context at prompt submission")
		fmt.Println("  PostToolUse      - Review tool output with reason and systemMessage")
		os.Exit(0)
	}

	name := os.Args[1]
	if name == "all" {
		for _, fn := range []func(){examplePreToolUse, exampleUserPromptSubmit, examplePostToolUse} {
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
