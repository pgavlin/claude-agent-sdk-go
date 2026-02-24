// Example: partial message streaming
//
// Demonstrates the IncludePartialMessages option to receive StreamEvent
// messages containing incremental updates as Claude generates responses.
// This is useful for building real-time UIs or monitoring tool use progress.
//
// Usage:
//
//	go run ./examples/partialmessages
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	fmt.Println("Partial Message Streaming Example")
	fmt.Println("==================================================")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	maxTurns := 2
	opts := &agentsdk.ClaudeAgentOptions{
		IncludePartialMessages: true,
		Model:                  "claude-sonnet-4-5",
		MaxTurns:               &maxTurns,
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	prompt := "Think of three jokes, then tell one"
	fmt.Printf("Prompt: %s\n\n", prompt)
	fmt.Println("==================================================")

	if err := client.SendQuery(ctx, prompt, ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		os.Exit(1)
	}

	for msg := range client.ReceiveResponse() {
		switch m := msg.(type) {
		case *agentsdk.StreamEvent:
			eventType, _ := m.Event["type"].(string)
			fmt.Printf("[stream event] type=%s\n", eventType)
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
}
