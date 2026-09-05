// Example: session history
//
// Demonstrates reading past sessions from the local Claude Code transcript
// store under ~/.claude/projects/, and mutating them: listing sessions,
// reading their messages, inspecting subagent transcripts, and renaming,
// tagging, or forking a session.
//
// These functions read the CLI's on-disk transcripts directly, so no CLI
// subprocess is spawned and no API calls are made.
//
// Usage:
//
//	go run ./examples/sessions
//	go run ./examples/sessions /path/to/project
package main

import (
	"fmt"
	"os"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	// An empty directory searches every project; passing one scopes the
	// listing to that project and its git worktrees.
	directory := ""
	if len(os.Args) > 1 {
		directory = os.Args[1]
	}

	sessions := agentsdk.ListSessions(agentsdk.ListSessionsOptions{
		Directory: directory,
		Limit:     10,
	})
	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		fmt.Println("Run Claude Code at least once to create a session transcript.")
		return
	}

	fmt.Printf("=== %d most recent sessions ===\n\n", len(sessions))
	for _, s := range sessions {
		fmt.Printf("%s\n  %s\n", s.SessionID, s.Summary)
		if s.GitBranch != "" {
			fmt.Printf("  branch: %s\n", s.GitBranch)
		}
		if s.Tag != "" {
			fmt.Printf("  tag: %s\n", s.Tag)
		}
		fmt.Println()
	}

	// Read the conversation from the most recent session.
	newest := sessions[0]
	fmt.Printf("=== First messages of %s ===\n\n", newest.SessionID)

	messages := agentsdk.GetSessionMessages(newest.SessionID, directory, 4, 0)
	for _, msg := range messages {
		fmt.Printf("[%s] %s\n", msg.Type, truncate(fmt.Sprint(msg.Message), 100))
	}
	fmt.Println()

	// Subagent transcripts live beside the main session file.
	if agentIDs := agentsdk.ListSubagents(newest.SessionID, directory); len(agentIDs) > 0 {
		fmt.Printf("=== Subagents of %s ===\n\n", newest.SessionID)
		for _, agentID := range agentIDs {
			subMessages := agentsdk.GetSubagentMessages(newest.SessionID, agentID, directory, 0, 0)
			fmt.Printf("%s: %d messages", agentID, len(subMessages))
			if len(subMessages) > 0 && subMessages[0].ParentToolUseID != "" {
				fmt.Printf(" (spawned by tool use %s)", subMessages[0].ParentToolUseID)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	// Mutations are commented out so the example is read-only by default.
	//
	// Renaming and tagging append entries; the most recent one wins, so
	// repeated calls are safe.
	//
	//	agentsdk.RenameSession(newest.SessionID, "Renamed by the SDK", directory)
	//	agentsdk.TagSession(newest.SessionID, "reviewed", directory)
	//	agentsdk.TagSession(newest.SessionID, "", directory) // clears the tag
	//
	// Forking copies the transcript into a new session with fresh UUIDs.
	// Pass UpToMessageID to branch from an earlier point.
	//
	//	fork, err := agentsdk.ForkSession(newest.SessionID, agentsdk.ForkSessionOptions{
	//		Directory: directory,
	//		Title:     "Experiment",
	//	})
	//
	// Deleting is a hard delete of the transcript and its subagent
	// transcripts.
	//
	//	agentsdk.DeleteSession(newest.SessionID, directory)
	fmt.Println("Rename, tag, fork, and delete are shown commented out in this example's source.")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
