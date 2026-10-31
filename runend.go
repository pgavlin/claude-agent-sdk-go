package agentsdk

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// runEndCeilingEnv is the CLI's own bound on its wait for background work
	// once stdin is closed; the SDK bounds its wait for the CLI's "idle" by
	// the same value.
	runEndCeilingEnv = "CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS"

	defaultRunEndCeilingMS = 600_000

	// maxRunEndCeilingMS is the longest ceiling honored (~24.8 days), as in
	// the TypeScript SDK, whose timers cannot run longer.
	maxRunEndCeilingMS = 1<<31 - 1
)

// deferringTaskTypes are the background task types that hold stdin open past
// a result. This mirrors the set the CLI itself holds a result back for.
// Background shells, monitors, teammates and remote agents are left out on
// purpose: they can run indefinitely, so waiting on one would withhold the
// close forever. Anything added here must reliably reach a terminal status.
var deferringTaskTypes = map[string]struct{}{
	"local_agent":    {},
	"local_workflow": {},
}

// runEndCeilingMS reads CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS as the CLI will
// see it: env (ClaudeAgentOptions.Env) overrides the inherited environment.
// 0 means no limit; anything that is not a non-negative integer falls back to
// the CLI's default of 10 minutes.
func runEndCeilingMS(env map[string]string) int64 {
	raw, ok := env[runEndCeilingEnv]
	if !ok {
		raw, ok = os.LookupEnv(runEndCeilingEnv)
	}
	if !ok {
		return defaultRunEndCeilingMS
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(strings.TrimSpace(raw), "-") {
			return maxRunEndCeilingMS
		}
		return defaultRunEndCeilingMS
	}
	if value < 0 {
		return defaultRunEndCeilingMS
	}
	return value
}

// runState tracks whether a run is over, so the stdin-closing waiter knows
// when it may close stdin.
//
// A result frame ends a turn, not necessarily the run: background tasks keep
// running past it, or have just finished and still wake the session for a
// follow-up turn, and those turns need stdin for hook and permission
// requests. A CLI that reports session state stays "running" while such a
// turn is owed and reports "idle" once none is; from a CLI that reports no
// state, the first result with no tracked tasks in flight ends the run.
type runState struct {
	mu sync.Mutex

	// ended is closed when the run is over. Work the CLI takes up after the
	// run ended swaps in a fresh channel (reopenLocked).
	ended chan struct{}

	resultReceived bool

	// sessionState is the CLI's latest session_state_changed state, or empty
	// while it sends none.
	sessionState string

	// turnInProgress is set while a main-thread turn is under way: the
	// ceiling counts only the wait between turns.
	turnInProgress bool

	// final is set once stdin is closed or the reader is gone: the run then
	// stays ended, since nothing can wait on a reopened one.
	final bool

	// inflightTasks holds the IDs of started-but-not-finished deferring
	// tasks.
	inflightTasks map[string]struct{}

	// ceilingMS bounds the wait between turns; 0 means no limit.
	ceilingMS int64
	// ceiling ends the run if no new turn starts within ceilingMS after a
	// result. ceilingGen tells a timer that fired after it was cleared or
	// re-armed to stand down.
	ceiling    *time.Timer
	ceilingGen uint64

	// bidirectional reports whether the CLI may still send control requests
	// that need a reply.
	bidirectional bool
}

func newRunState(ceilingMS int64, bidirectional bool) *runState {
	return &runState{
		ended:         make(chan struct{}),
		inflightTasks: make(map[string]struct{}),
		ceilingMS:     ceilingMS,
		bidirectional: bidirectional,
	}
}

// observeSystem tracks a system frame. It reports whether the frame should be
// withheld from the caller's stream.
func (r *runState) observeSystem(data map[string]any) (drop bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	hadTasks := len(r.inflightTasks) != 0
	r.trackTaskLifecycleLocked(data)
	if hadTasks && len(r.inflightTasks) == 0 {
		// The ceiling left the last tracked agent alone; the wait between
		// turns starts over now that it settled.
		r.rearmBetweenTurnsLocked()
	}

	if subtype, _ := data["subtype"].(string); subtype == "session_state_changed" {
		state, _ := data["state"].(string)
		r.onSessionStateLocked(state)
		// Frames the CLI sent only because the transport asked for them
		// (CLAUDE_CODE_SDK_READS_SESSION_STATE); the caller did not opt in.
		if hostOnly, _ := data["sdk_host_only"].(bool); hostOnly {
			return true
		}
	}
	return false
}

// observeResult tracks a result frame.
func (r *runState) observeResult() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resultReceived = true
	r.turnInProgress = false
	switch {
	case r.sessionState == "" || r.sessionState == "idle" || !r.bidirectional:
		r.maybeEndLocked()
	case r.sessionState != "requires_action":
		// While the SDK is still answering a request, the ceiling waits for
		// the "running" that follows.
		r.armCeilingLocked()
	}
}

// observeTurnActivity tracks a main-thread assistant or stream_event frame:
// a turn is under way, so the ceiling stops and the run reopens even if the
// ceiling ended it while no state changed.
func (r *runState) observeTurnActivity() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.turnInProgress = true
	r.reopenLocked()
	r.clearCeilingLocked()
}

// finish marks the run ended for good. It is called once stdin is closed or
// the reader is gone.
func (r *runState) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.final = true
	r.endLocked()
}

// wait blocks until the run ends or ctx is done.
func (r *runState) wait(ctx context.Context) error {
	r.mu.Lock()
	ended := r.ended
	r.mu.Unlock()

	select {
	case <-ended:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *runState) trackTaskLifecycleLocked(data map[string]any) {
	taskID, _ := data["task_id"].(string)
	if taskID == "" {
		return
	}
	switch subtype, _ := data["subtype"].(string); subtype {
	case "task_started":
		taskType, _ := data["task_type"].(string)
		if _, ok := deferringTaskTypes[taskType]; ok {
			r.inflightTasks[taskID] = struct{}{}
		}
	case "task_notification":
		delete(r.inflightTasks, taskID)
	case "task_updated":
		patch, _ := data["patch"].(map[string]any)
		status, _ := patch["status"].(string)
		if IsTerminalTaskStatus(status) {
			delete(r.inflightTasks, taskID)
		}
	}
}

func (r *runState) onSessionStateLocked(state string) {
	r.sessionState = state
	if state == "idle" {
		if r.resultReceived {
			r.maybeEndLocked()
		}
		return
	}
	// Work the CLI took up after the run ended (a finished background task
	// woke it) reopens the run until the next "idle".
	r.reopenLocked()
	if state == "requires_action" {
		// The host is answering a request; stdin must outlast it.
		r.clearCeilingLocked()
	} else {
		r.rearmBetweenTurnsLocked()
	}
}

// maybeEndLocked ends the run unless a tracked background task is still in
// flight. Such a task may still need hook and permission responses over
// stdin, and its completion wakes the session for a follow-up turn whose
// result (or "idle") ends the run then.
func (r *runState) maybeEndLocked() {
	if len(r.inflightTasks) != 0 {
		return
	}
	r.endLocked()
}

func (r *runState) endLocked() {
	r.clearCeilingLocked()
	select {
	case <-r.ended:
	default:
		close(r.ended)
	}
}

// reopenLocked reopens an ended run for work that started after it ended. A
// waiter the ended run already woke still closes stdin; this makes a later
// wait cover the new work too.
func (r *runState) reopenLocked() {
	if r.final {
		return
	}
	select {
	case <-r.ended:
		r.ended = make(chan struct{})
	default:
	}
}

func (r *runState) isEndedLocked() bool {
	select {
	case <-r.ended:
		return true
	default:
		return false
	}
}

// armCeilingLocked ends the run anyway once the ceiling passes with no new
// turn. The CLI's own background-wait ceiling only counts once stdin is
// closed, so without this, work that never finishes would hold "running",
// and stdin, open forever.
func (r *runState) armCeilingLocked() {
	r.clearCeilingLocked()
	if r.ceilingMS <= 0 || r.isEndedLocked() || r.final || r.turnInProgress || !r.bidirectional {
		return
	}
	gen := r.ceilingGen
	d := time.Duration(min(r.ceilingMS, maxRunEndCeilingMS)) * time.Millisecond
	r.ceiling = time.AfterFunc(d, func() { r.endAtCeiling(gen) })
}

// rearmBetweenTurnsLocked restarts the ceiling if the run is between turns,
// past a result, with the CLI still reporting work.
func (r *runState) rearmBetweenTurnsLocked() {
	if r.resultReceived && r.sessionState != "" && r.sessionState != "idle" && r.sessionState != "requires_action" {
		r.armCeilingLocked()
	}
}

func (r *runState) endAtCeiling(gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if gen != r.ceilingGen {
		return
	}
	r.ceiling = nil
	if len(r.inflightTasks) != 0 {
		// A tracked background agent may still need stdin. The ceiling
		// starts over once it settles.
		return
	}
	r.endLocked()
}

func (r *runState) clearCeilingLocked() {
	r.ceilingGen++
	if r.ceiling != nil {
		r.ceiling.Stop()
		r.ceiling = nil
	}
}
