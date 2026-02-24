// Example: system prompt
//
// Demonstrates different SystemPrompt configurations: no system prompt,
// a custom string prompt, a preset, and a preset with appended text.
//
// Usage:
//
//	go run ./examples/systemprompt
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	noSystemPrompt()
	stringSystemPrompt()
	presetSystemPrompt()
	presetWithAppend()
}

func noSystemPrompt() {
	fmt.Println("=== No System Prompt (Vanilla Claude) ===")

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

func stringSystemPrompt() {
	fmt.Println("=== String System Prompt ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		SystemPrompt: "You are a pirate assistant. Respond in pirate speak.",
	}

	messages, errFn := agentsdk.Query(ctx, "What is 2 + 2?", opts)
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

func presetSystemPrompt() {
	fmt.Println("=== Preset System Prompt (Default) ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		SystemPrompt: &agentsdk.SystemPromptPreset{
			Type:   "preset",
			Preset: "claude_code",
		},
	}

	messages, errFn := agentsdk.Query(ctx, "What is 2 + 2?", opts)
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

func presetWithAppend() {
	fmt.Println("=== Preset System Prompt with Append ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		SystemPrompt: &agentsdk.SystemPromptPreset{
			Type:   "preset",
			Preset: "claude_code",
			Append: "Always end your response with a fun fact.",
		},
	}

	messages, errFn := agentsdk.Query(ctx, "What is 2 + 2?", opts)
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
