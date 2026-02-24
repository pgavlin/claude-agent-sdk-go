// Example: max budget
//
// Demonstrates the MaxBudgetUSD option for controlling API costs.
// Shows three scenarios: no budget, a reasonable budget, and a very
// tight budget that will be exceeded.
//
// Usage:
//
//	go run ./examples/maxbudget
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	fmt.Println("This example demonstrates using MaxBudgetUSD to control API costs.")
	fmt.Println()

	withoutBudget()
	withReasonableBudget()
	withTightBudget()

	fmt.Println()
	fmt.Println("Note: Budget checking happens after each API call completes,")
	fmt.Println("so the final cost may slightly exceed the specified budget.")
}

func withoutBudget() {
	fmt.Println("=== Without Budget Limit ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages, errFn := agentsdk.Query(ctx, "What is 2 + 2?", nil)
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			if m.TotalCostUSD != nil {
				fmt.Printf("Total cost: $%.4f\n", *m.TotalCostUSD)
			}
			fmt.Printf("Status: %s\n", m.Subtype)
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}

func withReasonableBudget() {
	fmt.Println("=== With Reasonable Budget ($0.10) ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	budget := 0.10
	opts := &agentsdk.ClaudeAgentOptions{
		MaxBudgetUSD: &budget,
	}

	messages, errFn := agentsdk.Query(ctx, "What is 2 + 2?", opts)
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			if m.TotalCostUSD != nil {
				fmt.Printf("Total cost: $%.4f\n", *m.TotalCostUSD)
			}
			fmt.Printf("Status: %s\n", m.Subtype)
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}

func withTightBudget() {
	fmt.Println("=== With Tight Budget ($0.0001) ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	budget := 0.0001
	opts := &agentsdk.ClaudeAgentOptions{
		MaxBudgetUSD: &budget,
	}

	messages, errFn := agentsdk.Query(ctx, "Read the README.md file and summarize it", opts)
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Printf("Claude: %s\n", tb.Text)
				}
			}
		case *agentsdk.ResultMessage:
			if m.TotalCostUSD != nil {
				fmt.Printf("Total cost: $%.4f\n", *m.TotalCostUSD)
			}
			fmt.Printf("Status: %s\n", m.Subtype)

			if m.Subtype == "error_max_budget_usd" {
				fmt.Println("Budget limit exceeded!")
				fmt.Println("Note: The cost may exceed the budget by up to one API call's worth")
			}
		}
	}
	if err := errFn(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	fmt.Println()
}
