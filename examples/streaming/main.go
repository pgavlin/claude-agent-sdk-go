// Example: streaming mode
//
// Comprehensive examples of using the Client for streaming mode.
// Demonstrates basic streaming, multi-turn conversation, bash commands,
// interrupt handling, custom options, and error handling.
//
// Usage:
//
//	go run ./examples/streaming                          # list examples
//	go run ./examples/streaming all                      # run all
//	go run ./examples/streaming basic_streaming          # run specific
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
	case *agentsdk.UserMessage:
		if text, ok := m.Content.(string); ok {
			fmt.Printf("User: %s\n", text)
		}
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

func exampleBasicStreaming() {
	fmt.Println("=== Basic Streaming Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := agentsdk.NewClient(nil)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("User: What is 2+2?")
	if err := client.SendQuery(ctx, "What is 2+2?", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()
}

func exampleMultiTurnConversation() {
	fmt.Println("=== Multi-Turn Conversation Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := agentsdk.NewClient(nil)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	// First turn
	fmt.Println("User: What's the capital of France?")
	if err := client.SendQuery(ctx, "What's the capital of France?", "s1"); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}

	// Second turn - follow-up
	fmt.Println()
	fmt.Println("User: What's the population of that city?")
	if err := client.SendQuery(ctx, "What's the population of that city?", "s1"); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()
}

func exampleWithInterrupt() {
	fmt.Println("=== Interrupt Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := agentsdk.NewClient(nil)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	// Start a long-running task
	fmt.Println("User: Count from 1 to 100 slowly")
	if err := client.SendQuery(ctx, "Count from 1 to 100 slowly, with a brief pause between each number", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	// Consume messages in a goroutine while we wait to interrupt
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range client.ReceiveResponse() {
			displayMessage(msg)
		}
	}()

	// Wait briefly then send interrupt
	time.Sleep(2 * time.Second)
	fmt.Println()
	fmt.Println("[After 2 seconds, sending interrupt...]")
	if err := client.Interrupt(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "interrupt failed: %v\n", err)
	}

	<-done

	// Send new instruction after interrupt
	fmt.Println()
	fmt.Println("User: Never mind, just tell me a quick joke")
	if err := client.SendQuery(ctx, "Never mind, just tell me a quick joke", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		displayMessage(msg)
	}
	fmt.Println()
}

func exampleWithOptions() {
	fmt.Println("=== Custom Options Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		AllowedTools: []string{"Read", "Write"},
		SystemPrompt: "You are a helpful coding assistant.",
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("User: Create a simple hello.txt file with a greeting message")
	if err := client.SendQuery(ctx, "Create a simple hello.txt file with a greeting message", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	var toolUses []string
	for msg := range client.ReceiveResponse() {
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				switch b := block.(type) {
				case agentsdk.TextBlock:
					fmt.Printf("Claude: %s\n", b.Text)
				case agentsdk.ToolUseBlock:
					toolUses = append(toolUses, b.Name)
				}
			}
		case *agentsdk.ResultMessage:
			displayMessage(msg)
		}
	}

	if len(toolUses) > 0 {
		fmt.Printf("Tools used: %s\n", strings.Join(toolUses, ", "))
	}
	fmt.Println()
}

func exampleBashCommand() {
	fmt.Println("=== Bash Command Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := agentsdk.NewClient(nil)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("User: Run a bash echo command")
	if err := client.SendQuery(ctx, "Run a bash echo command that says 'Hello from bash!'", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	messageTypes := map[string]bool{}
	for msg := range client.ReceiveResponse() {
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			messageTypes["AssistantMessage"] = true
			for _, block := range m.Content {
				switch b := block.(type) {
				case agentsdk.TextBlock:
					fmt.Printf("Claude: %s\n", b.Text)
				case agentsdk.ToolUseBlock:
					fmt.Printf("Tool Use: %s (id: %s)\n", b.Name, b.ID)
					if b.Name == "Bash" {
						if cmd, ok := b.Input["command"].(string); ok {
							fmt.Printf("  Command: %s\n", cmd)
						}
					}
				}
			}
		case *agentsdk.UserMessage:
			messageTypes["UserMessage"] = true
		case *agentsdk.ResultMessage:
			messageTypes["ResultMessage"] = true
			fmt.Println("Result ended")
			if m.TotalCostUSD != nil {
				fmt.Printf("Cost: $%.4f\n", *m.TotalCostUSD)
			}
		}
	}

	types := make([]string, 0, len(messageTypes))
	for t := range messageTypes {
		types = append(types, t)
	}
	fmt.Printf("\nMessage types received: %s\n", strings.Join(types, ", "))
	fmt.Println()
}

func exampleControlProtocol() {
	fmt.Println("=== Control Protocol Example ===")
	fmt.Println("Shows server info retrieval and interrupt capability")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := agentsdk.NewClient(nil)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	// 1. Get server initialization info
	fmt.Println("1. Getting server info...")
	serverInfo := client.ServerInfo()
	if serverInfo != nil {
		fmt.Println("Server info retrieved successfully!")
		if commands, ok := serverInfo["commands"].([]any); ok {
			fmt.Printf("  - Available commands: %d\n", len(commands))
		}
		if style, ok := serverInfo["output_style"].(string); ok {
			fmt.Printf("  - Output style: %s\n", style)
		}
	} else {
		fmt.Println("No server info available")
	}

	fmt.Println()
	fmt.Println("2. Testing interrupt capability...")

	fmt.Println("User: Count from 1 to 20 slowly")
	if err := client.SendQuery(ctx, "Count from 1 to 20 slowly, pausing between each number", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	// Consume messages in background
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range client.ReceiveResponse() {
			if m, ok := msg.(*agentsdk.AssistantMessage); ok {
				for _, block := range m.Content {
					if tb, ok := block.(agentsdk.TextBlock); ok {
						text := tb.Text
						if len(text) > 50 {
							text = text[:50] + "..."
						}
						fmt.Printf("Claude: %s\n", text)
						break
					}
				}
			}
		}
	}()

	time.Sleep(2 * time.Second)
	fmt.Println()
	fmt.Println("[Sending interrupt after 2 seconds...]")
	if err := client.Interrupt(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "interrupt failed: %v\n", err)
	} else {
		fmt.Println("Interrupt sent successfully")
	}

	<-done

	// Send new query after interrupt
	fmt.Println()
	fmt.Println("User: Just say 'Hello!'")
	if err := client.SendQuery(ctx, "Just say 'Hello!'", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}
	for msg := range client.ReceiveResponse() {
		if m, ok := msg.(*agentsdk.AssistantMessage); ok {
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		}
	}
	fmt.Println()
}

func exampleErrorHandling() {
	fmt.Println("=== Error Handling Example ===")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := agentsdk.NewClient(nil)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("User: Run a bash sleep command for 60 seconds not in the background")
	if err := client.SendQuery(ctx, "Run a bash sleep command for 60 seconds not in the background", ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		return
	}

	msgCount := 0
	for msg := range client.ReceiveResponse() {
		msgCount++
		if m, ok := msg.(*agentsdk.AssistantMessage); ok {
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					text := tb.Text
					if len(text) > 50 {
						text = text[:50] + "..."
					}
					fmt.Printf("Claude: %s\n", text)
				}
			}
		} else {
			displayMessage(msg)
		}

		// Check if context was cancelled (timeout)
		if ctx.Err() != nil {
			fmt.Printf("\nResponse timeout - demonstrating graceful handling\n")
			fmt.Printf("Received %d messages before timeout\n", msgCount)
			break
		}
	}
	fmt.Println()
}

func main() {
	examples := map[string]func(){
		"basic_streaming":         exampleBasicStreaming,
		"multi_turn_conversation": exampleMultiTurnConversation,
		"with_interrupt":          exampleWithInterrupt,
		"with_options":            exampleWithOptions,
		"bash_command":            exampleBashCommand,
		"control_protocol":        exampleControlProtocol,
		"error_handling":          exampleErrorHandling,
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run ./examples/streaming <example_name>")
		fmt.Println()
		fmt.Println("Available examples:")
		fmt.Println("  all                     - Run all examples")
		fmt.Println("  basic_streaming         - Basic streaming with Client")
		fmt.Println("  multi_turn_conversation - Multi-turn conversation")
		fmt.Println("  with_interrupt          - Interrupt and resume")
		fmt.Println("  with_options            - Custom options")
		fmt.Println("  bash_command            - Bash tool use blocks")
		fmt.Println("  control_protocol        - Server info and interrupt")
		fmt.Println("  error_handling          - Timeout and error handling")
		os.Exit(0)
	}

	name := os.Args[1]
	if name == "all" {
		for _, fn := range []func(){
			exampleBasicStreaming,
			exampleMultiTurnConversation,
			exampleWithInterrupt,
			exampleWithOptions,
			exampleBashCommand,
			exampleControlProtocol,
			exampleErrorHandling,
		} {
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
