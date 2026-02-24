// Example: tool permission callbacks
//
// Demonstrates how to use CanUseTool callbacks to programmatically control
// which tools Claude can use and modify their inputs. Shows allow/deny
// decisions, input modification for safety, and logging.
//
// Usage:
//
//	go run ./examples/toolpermission
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

// toolUsageLog tracks tool usage for demonstration.
var toolUsageLog []map[string]any

func myPermissionCallback(
	_ context.Context,
	toolName string,
	input map[string]any,
	permCtx agentsdk.ToolPermissionContext,
) (agentsdk.PermissionResult, error) {
	// Log the tool request
	toolUsageLog = append(toolUsageLog, map[string]any{
		"tool":        toolName,
		"input":       input,
		"suggestions": permCtx.Suggestions,
	})

	inputJSON, _ := json.MarshalIndent(input, "   ", "  ")
	fmt.Printf("\n  Tool Permission Request: %s\n", toolName)
	fmt.Printf("   Input: %s\n", inputJSON)

	// Always allow read operations
	if toolName == "Read" || toolName == "Glob" || toolName == "Grep" {
		fmt.Printf("   Automatically allowing %s (read-only operation)\n", toolName)
		return agentsdk.PermissionAllow{}, nil
	}

	// Deny write operations to system directories
	if toolName == "Write" || toolName == "Edit" || toolName == "MultiEdit" {
		filePath, _ := input["file_path"].(string)
		if strings.HasPrefix(filePath, "/etc/") || strings.HasPrefix(filePath, "/usr/") {
			fmt.Printf("   Denying write to system directory: %s\n", filePath)
			return agentsdk.PermissionDeny{
				Message: fmt.Sprintf("Cannot write to system directory: %s", filePath),
			}, nil
		}

		// Redirect writes to a safe directory
		if !strings.HasPrefix(filePath, "/tmp/") && !strings.HasPrefix(filePath, "./") {
			parts := strings.Split(filePath, "/")
			safePath := fmt.Sprintf("./safe_output/%s", parts[len(parts)-1])
			fmt.Printf("   Redirecting write from %s to %s\n", filePath, safePath)
			modifiedInput := make(map[string]any)
			for k, v := range input {
				modifiedInput[k] = v
			}
			modifiedInput["file_path"] = safePath
			return agentsdk.PermissionAllow{
				UpdatedInput: modifiedInput,
			}, nil
		}
	}

	// Check dangerous bash commands
	if toolName == "Bash" {
		command, _ := input["command"].(string)
		dangerous := []string{"rm -rf", "sudo", "chmod 777", "dd if=", "mkfs"}

		for _, d := range dangerous {
			if strings.Contains(command, d) {
				fmt.Printf("   Denying dangerous command: %s\n", command)
				return agentsdk.PermissionDeny{
					Message: fmt.Sprintf("Dangerous command pattern detected: %s", d),
				}, nil
			}
		}

		fmt.Printf("   Allowing bash command: %s\n", command)
		return agentsdk.PermissionAllow{}, nil
	}

	// Allow all other tools
	fmt.Printf("   Allowing tool: %s\n", toolName)
	return agentsdk.PermissionAllow{}, nil
}

func main() {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Tool Permission Callback Example")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()
	fmt.Println("This example demonstrates how to:")
	fmt.Println("1. Allow/deny tools based on type")
	fmt.Println("2. Modify tool inputs for safety")
	fmt.Println("3. Log tool usage")
	fmt.Println(strings.Repeat("=", 60))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	opts := &agentsdk.ClaudeAgentOptions{
		CanUseTool:     myPermissionCallback,
		PermissionMode: agentsdk.PermissionModeDefault,
	}

	client := agentsdk.NewClient(opts)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	fmt.Println()
	fmt.Println("Sending query to Claude...")
	prompt := "Please do the following:\n" +
		"1. List the files in the current directory\n" +
		"2. Create a simple Go hello world script at hello.go\n" +
		"3. Run the script to test it"

	if err := client.SendQuery(ctx, prompt, ""); err != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Receiving response...")
	msgCount := 0

	for msg := range client.ReceiveResponse() {
		msgCount++
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("\nClaude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			fmt.Println()
			fmt.Println("Task completed!")
			fmt.Printf("   Duration: %dms\n", m.DurationMS)
			if m.TotalCostUSD != nil {
				fmt.Printf("   Cost: $%.4f\n", *m.TotalCostUSD)
			}
			fmt.Printf("   Messages processed: %d\n", msgCount)
		}
	}

	// Print tool usage summary
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Tool Usage Summary")
	fmt.Println(strings.Repeat("=", 60))
	for i, usage := range toolUsageLog {
		inputJSON, _ := json.MarshalIndent(usage["input"], "   ", "  ")
		fmt.Printf("\n%d. Tool: %s\n", i+1, usage["tool"])
		fmt.Printf("   Input: %s\n", inputJSON)
	}
}
