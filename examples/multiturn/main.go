// Example: multi-turn conversation
//
// Demonstrates multi-turn conversation where Claude preserves context
// between turns. Each follow-up question builds on previous answers.
//
// Usage:
//
//	go run ./examples/multiturn
package main

import (
	"context"
	"fmt"
	"os"
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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := agentsdk.NewClient(&agentsdk.ClaudeAgentOptions{
		Model: "claude-sonnet-4-5",
	})

	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Println("=== Multi-turn Conversation ===")
	fmt.Println()

	// First turn: Simple math question
	fmt.Println("User: What's 15 + 27?")
	if err := client.SendQuery(ctx, "What's 15 + 27?", "math"); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		os.Exit(1)
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()

	// Second turn: Follow-up calculation
	fmt.Println("User: Now multiply that result by 2")
	if err := client.SendQuery(ctx, "Now multiply that result by 2", "math"); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		os.Exit(1)
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()

	// Third turn: One more operation
	fmt.Println("User: Divide that by 7 and round to 2 decimal places")
	if err := client.SendQuery(ctx, "Divide that by 7 and round to 2 decimal places", "math"); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		os.Exit(1)
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}

	fmt.Println()
	fmt.Println("Conversation complete!")
}
