// Example: mirroring sessions to an external store
//
// Demonstrates the SessionStore adapter: every transcript line the CLI
// writes locally is also handed to the store, and a resume can materialize
// from the store when the local file is absent. This is how a deployment
// keeps session history in its own storage — S3, Postgres, Redis — rather
// than relying on the local ~/.claude directory.
//
// This example uses the built-in in-memory store, which is for testing and
// development; a production adapter implements the same interface against a
// real backend.
//
// Usage:
//
//	go run ./examples/sessionstore
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	agentsdk "github.com/pgavlin/claude-agent-sdk-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	store := agentsdk.NewInMemorySessionStore()

	// Setting SessionStore makes the CLI emit transcript-mirror frames,
	// which the SDK batches and hands to the store's Append.
	opts := &agentsdk.ClaudeAgentOptions{
		SessionStore:   store,
		PermissionMode: agentsdk.PermissionModePlan,
	}

	fmt.Println("=== Running a query with a session store attached ===")
	messages, errFn := agentsdk.Query(ctx, "Name three planets, briefly.", opts)
	var sessionID string
	for msg := range messages {
		switch m := msg.(type) {
		case *agentsdk.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(agentsdk.TextBlock); ok {
					fmt.Println(tb.Text)
				}
			}
		case *agentsdk.MirrorErrorMessage:
			// Non-fatal: the local transcript is still durable, but the
			// mirrored copy is missing this batch.
			fmt.Printf("mirror error: %s\n", m.Error)
		case *agentsdk.ResultMessage:
			sessionID = m.SessionID
		}
	}
	if err := errFn(); err != nil {
		log.Fatal(err)
	}

	// The store is keyed by a project key derived from the working
	// directory, the same way the CLI names its on-disk project directories.
	fmt.Printf("\n=== Store contents ===\n")
	fmt.Printf("sessions mirrored: %d\n", store.Size())

	sessions, err := agentsdk.ListSessionsFromStore(ctx, store, "", 0, 0)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range sessions {
		fmt.Printf("%s: %s\n", s.SessionID, s.Summary)
	}

	if sessionID == "" {
		return
	}

	// Read the mirrored conversation back out of the store.
	stored, err := agentsdk.GetSessionMessagesFromStore(ctx, store, sessionID, "", 0, 0)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n=== %d messages read back from the store ===\n", len(stored))

	// Resuming with the same store materializes the session into a
	// temporary config directory, so the CLI can resume it even when the
	// local transcript is gone.
	//
	//	resumed := &agentsdk.ClaudeAgentOptions{
	//		SessionStore: store,
	//		Resume:       sessionID,
	//	}
	//
	// An existing local session can also be replayed into a store, which is
	// useful for migrating history or filling a gap after a mirror error:
	//
	//	agentsdk.ImportSessionToStore(ctx, sessionID, store, agentsdk.ImportSessionOptions{})
	fmt.Println("\nResume-from-store and import are shown commented out in this example's source.")
}
