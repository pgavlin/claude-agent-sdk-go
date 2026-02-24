// Example: stderr callback
//
// Demonstrates the Stderr callback for capturing CLI debug output.
// Each line of stderr from the CLI process is passed to the callback,
// which can be used for logging, monitoring, or debugging.
//
// Usage:
//
//	go run ./examples/stderr
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	fmt.Println("Running query with stderr capture...")

	var mu sync.Mutex
	var stderrMessages []string

	opts := &agentsdk.ClaudeAgentOptions{
		Stderr: func(message string) {
			mu.Lock()
			stderrMessages = append(stderrMessages, message)
			mu.Unlock()

			// Optionally print specific messages
			if strings.Contains(message, "[ERROR]") {
				fmt.Printf("Error detected: %s\n", message)
			}
		},
		ExtraArgs: map[string]string{
			"debug-to-stderr": "",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages, errFn := agentsdk.Query(ctx, "What is 2+2?", opts)
	for msg := range messages {
		if m, ok := msg.(*agentsdk.AssistantMessage); ok {
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Response: %s\n", tb.Text)
				}
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}

	// Show what we captured
	mu.Lock()
	count := len(stderrMessages)
	first := ""
	if count > 0 {
		first = stderrMessages[0]
		if len(first) > 100 {
			first = first[:100]
		}
	}
	mu.Unlock()

	fmt.Printf("\nCaptured %d stderr lines\n", count)
	if first != "" {
		fmt.Printf("First stderr line: %s\n", first)
	}
}
