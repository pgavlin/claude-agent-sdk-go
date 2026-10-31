package agentsdk

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestRunEndCeilingMS(t *testing.T) {
	const unset = "\x00"
	tests := []struct {
		name    string
		option  string
		ambient string
		want    int64
	}{
		{"default", unset, unset, defaultRunEndCeilingMS},
		{"zero", "0", unset, 0},
		{"whitespace", " 250 ", unset, 250},
		{"ambient", unset, "1234", 1234},
		{"options over ambient", "250", "1234", 250},
		{"not a number", "soon", unset, defaultRunEndCeilingMS},
		{"negative", "-1", unset, defaultRunEndCeilingMS},
		{"empty option wins", "", "1234", defaultRunEndCeilingMS},
		{"huge", "99999999999999999999", unset, maxRunEndCeilingMS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.ambient == unset {
				t.Setenv(runEndCeilingEnv, "")
				// t.Setenv restores the variable afterwards; unset it for
				// the test itself.
				unsetEnv(t, runEndCeilingEnv)
			} else {
				t.Setenv(runEndCeilingEnv, tt.ambient)
			}
			env := map[string]string{}
			if tt.option != unset {
				env[runEndCeilingEnv] = tt.option
			}
			if got := runEndCeilingMS(env); got != tt.want {
				t.Errorf("runEndCeilingMS = %d, want %d", got, tt.want)
			}
		})
	}
}

// runEndHarness drives a protocolQuery over a channel transport with a hook
// configured, so the CLI may send control requests and stdin must outlast
// each turn.
type runEndHarness struct {
	t      *testing.T
	ct     *chanTransport
	q      *protocolQuery
	closed chan struct{}
	marker int
}

func newRunEndHarness(t *testing.T, ceilingMS int64, bidirectional bool) *runEndHarness {
	t.Helper()
	opts := &ClaudeAgentOptions{
		Env: map[string]string{runEndCeilingEnv: strconv.FormatInt(ceilingMS, 10)},
	}
	if bidirectional {
		noop := func(context.Context, HookInput, string) (*HookOutput, error) { return &HookOutput{}, nil }
		opts.Hooks = map[HookEvent][]HookMatcher{HookEventPreToolUse: {{Hooks: []HookCallback{noop}}}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := &runEndHarness{t: t, ct: newChanTransport(), closed: make(chan struct{})}
	h.q = newProtocolQuery(h.ct, opts)
	h.q.Start(ctx)
	go func() {
		_ = h.q.WaitForRunEndAndEndInput(ctx)
		close(h.closed)
	}()
	return h
}

// send delivers frames and waits until the read loop has handled them.
func (h *runEndHarness) send(frames ...map[string]any) {
	h.t.Helper()
	for _, f := range frames {
		h.ct.send(f)
	}
	h.marker++
	id := "marker-" + strconv.Itoa(h.marker)
	h.ct.send(map[string]any{"type": "system", "subtype": "test_marker", "id": id})
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg := <-h.q.Messages():
			if sm, ok := msg.(*SystemMessage); ok && sm.Subtype == "test_marker" && sm.Data["id"] == id {
				return
			}
		case <-timeout:
			h.t.Fatal("timed out waiting for the read loop")
		}
	}
}

func (h *runEndHarness) expectOpen() {
	h.t.Helper()
	select {
	case <-h.closed:
		h.t.Fatal("stdin was closed, expected it to stay open")
	case <-time.After(20 * time.Millisecond):
	}
}

func (h *runEndHarness) expectClosedWithin(d time.Duration) {
	h.t.Helper()
	select {
	case <-h.closed:
	case <-time.After(d):
		h.t.Fatal("stdin is still open, expected it to be closed")
	}
}

func sessionState(state string, hostOnly bool) map[string]any {
	frame := map[string]any{"type": "system", "subtype": "session_state_changed", "state": state, "session_id": "test"}
	if hostOnly {
		frame["sdk_host_only"] = true
	}
	return frame
}

func runEndResult() map[string]any {
	return map[string]any{
		"type": "result", "subtype": "success", "duration_ms": 1, "duration_api_ms": 1,
		"is_error": false, "num_turns": 1, "session_id": "test",
	}
}

func runEndAssistant(parentToolUseID any) map[string]any {
	return map[string]any{
		"type":               "assistant",
		"parent_tool_use_id": parentToolUseID,
		"message": map[string]any{
			"role": "assistant", "model": "m",
			"content": []any{map[string]any{"type": "text", "text": "hi"}},
		},
	}
}

func taskStarted(id string) map[string]any {
	return map[string]any{"type": "system", "subtype": "task_started", "task_id": id, "task_type": "local_agent", "description": "d"}
}

func taskNotification(id string) map[string]any {
	return map[string]any{"type": "system", "subtype": "task_notification", "task_id": id, "status": "completed"}
}

func TestRunEndNoStateFramesClosesAtFirstResult(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, true)
	h.send(runEndAssistant(nil))
	h.expectOpen()
	h.send(runEndResult())
	h.expectClosedWithin(time.Second)
}

func TestRunEndTaskSettledBeforeResultWaitsForIdle(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, true)
	h.send(sessionState("running", true), runEndAssistant(nil), taskStarted("t1"), taskNotification("t1"), runEndResult())
	// The finished task still wakes the session for another turn.
	h.expectOpen()
	h.send(runEndAssistant(nil), runEndResult())
	h.expectOpen()
	h.send(sessionState("idle", true))
	h.expectClosedWithin(time.Second)
}

func TestRunEndOnlyMarkedStateFramesAreDropped(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, true)
	h.ct.send(sessionState("running", true))
	h.ct.send(sessionState("requires_action", false))
	h.ct.send(map[string]any{"type": "system", "subtype": "test_marker", "id": "end"})

	var states []string
	for msg := range h.q.Messages() {
		sm, ok := msg.(*SystemMessage)
		if !ok {
			continue
		}
		if sm.Subtype == "test_marker" {
			break
		}
		if sm.Subtype == "session_state_changed" {
			states = append(states, sm.Data["state"].(string))
		}
	}
	if len(states) != 1 || states[0] != "requires_action" {
		t.Errorf("states yielded = %v, want [requires_action]", states)
	}
}

func TestRunEndIdleJustBeforeResultEndsAtResult(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, true)
	h.send(sessionState("running", true), runEndAssistant(nil), sessionState("idle", true))
	h.expectOpen()
	h.send(runEndResult())
	h.expectClosedWithin(time.Second)
}

func TestRunEndIdleBeforeAnyResultDoesNotEnd(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, true)
	h.send(sessionState("idle", true))
	h.expectOpen()
	h.send(sessionState("running", true), runEndAssistant(nil), runEndResult())
	h.expectOpen()
	h.send(sessionState("idle", true))
	h.expectClosedWithin(time.Second)
}

func TestRunEndIdleWithTrackedTaskInFlightStaysOpen(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, true)
	h.send(taskStarted("t1"), runEndResult(), sessionState("idle", true))
	h.expectOpen()
	h.send(taskNotification("t1"), sessionState("running", true), runEndAssistant(nil), runEndResult(), sessionState("idle", true))
	h.expectClosedWithin(time.Second)
}

func TestRunEndWithoutControlRequestsEndsAtResult(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, false)
	h.send(sessionState("running", true), runEndResult())
	h.expectClosedWithin(time.Second)
}

func TestRunEndReadLoopExitEndsRun(t *testing.T) {
	h := newRunEndHarness(t, defaultRunEndCeilingMS, true)
	h.send(sessionState("running", true))
	h.ct.close()
	h.expectClosedWithin(time.Second)
}

func TestRunEndCeilingEndsRunWithNoIdle(t *testing.T) {
	h := newRunEndHarness(t, 50, true)
	h.send(sessionState("running", true), runEndAssistant(nil), runEndResult())
	h.expectClosedWithin(2 * time.Second)
}

func TestRunEndMainThreadTurnStopsCeiling(t *testing.T) {
	h := newRunEndHarness(t, 100, true)
	h.send(sessionState("running", true), runEndResult(), runEndAssistant(nil))
	time.Sleep(250 * time.Millisecond)
	h.expectOpen()
	h.send(runEndResult())
	h.expectClosedWithin(2 * time.Second)
}

func TestRunEndSubagentMessagesDoNotStopCeiling(t *testing.T) {
	h := newRunEndHarness(t, 100, true)
	h.send(sessionState("running", true), runEndResult(), runEndAssistant("toolu_agent"))
	h.expectClosedWithin(2 * time.Second)
}

func TestRunEndRequiresActionStopsCeiling(t *testing.T) {
	h := newRunEndHarness(t, 100, true)
	h.send(sessionState("running", true), runEndResult(), sessionState("requires_action", true))
	time.Sleep(250 * time.Millisecond)
	h.expectOpen()
	// The answered request puts the CLI back to "running", which restarts
	// the ceiling between turns.
	h.send(sessionState("running", true))
	h.expectClosedWithin(2 * time.Second)
}

func TestRunEndResultWhileAnsweringRequestDoesNotArm(t *testing.T) {
	h := newRunEndHarness(t, 100, true)
	h.send(sessionState("requires_action", true), runEndResult())
	time.Sleep(250 * time.Millisecond)
	h.expectOpen()
}

func TestRunEndCeilingLeavesTrackedAgentAlone(t *testing.T) {
	h := newRunEndHarness(t, 100, true)
	h.send(sessionState("running", true), taskStarted("t1"), runEndResult())
	time.Sleep(250 * time.Millisecond)
	h.expectOpen()
	// Once the agent settles, the wait between turns starts over.
	h.send(taskNotification("t1"))
	h.expectClosedWithin(2 * time.Second)
}

func TestRunEndZeroCeilingWaitsForIdle(t *testing.T) {
	h := newRunEndHarness(t, 0, true)
	h.send(sessionState("running", true), runEndResult())
	time.Sleep(100 * time.Millisecond)
	h.expectOpen()
	h.send(sessionState("idle", true))
	h.expectClosedWithin(time.Second)
}

func TestWriteUserMessageVerbatimPrompts(t *testing.T) {
	for _, verbatim := range []bool{false, true} {
		ct := newChanTransport()
		q := newProtocolQuery(ct, &ClaudeAgentOptions{VerbatimPrompts: verbatim})
		if err := q.WriteUserMessage(context.Background(), "@/etc/passwd", ""); err != nil {
			t.Fatal(err)
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(<-ct.writeCh), &msg); err != nil {
			t.Fatal(err)
		}
		if _, ok := msg["client_composed"]; ok != verbatim {
			t.Errorf("verbatim=%v: client_composed present=%v", verbatim, ok)
		}
		if verbatim && msg["client_composed"] != true {
			t.Errorf("client_composed = %v, want true", msg["client_composed"])
		}
	}
}

// unsetEnv unsets key for the rest of the test. Call t.Setenv first so the
// original value is restored afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}
