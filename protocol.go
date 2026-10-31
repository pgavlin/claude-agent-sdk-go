package agentsdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultInitTimeout = 60 * time.Second
	messageChannelCap  = 100
)

// protocolQuery manages the control protocol for a single CLI session.
type protocolQuery struct {
	transport Transport
	opts      *ClaudeAgentOptions

	messages   chan Message
	readDone   chan struct{}
	initResult map[string]any

	pendingMu sync.Mutex
	pending   map[string]chan controlResult

	reqCounter atomic.Int64

	hookCallbacks map[string]HookCallback

	// inflightMu guards inflight, which maps a control request ID to the
	// cancel function of its handler goroutine so a control_cancel_request
	// can abandon it.
	inflightMu sync.Mutex
	inflight   map[string]context.CancelFunc

	// mirror receives transcript_mirror frames when a SessionStore is
	// configured. It is nil otherwise.
	mirror *transcriptMirrorBatcher

	// run tracks whether the run is over, so stdin can stay open while the
	// CLI may still send control requests.
	run *runState
}

type controlResult struct {
	response map[string]any
	err      error
}

func newProtocolQuery(transport Transport, opts *ClaudeAgentOptions) *protocolQuery {
	if opts == nil {
		opts = &ClaudeAgentOptions{}
	}
	return &protocolQuery{
		transport:     transport,
		opts:          opts,
		messages:      make(chan Message, messageChannelCap),
		readDone:      make(chan struct{}),
		pending:       make(map[string]chan controlResult),
		hookCallbacks: make(map[string]HookCallback),
		inflight:      make(map[string]context.CancelFunc),
		run:           newRunState(runEndCeilingMS(opts.Env), hasBidirectionalNeeds(opts)),
	}
}

func (q *protocolQuery) Messages() <-chan Message {
	return q.messages
}

func (q *protocolQuery) Start(ctx context.Context) {
	go q.readLoop(ctx)
}

func (q *protocolQuery) Initialize(ctx context.Context) (map[string]any, error) {
	request := map[string]any{
		"subtype": "initialize",
	}

	if q.opts.Hooks != nil {
		hooksConfig := q.buildHooksConfig()
		if len(hooksConfig) > 0 {
			request["hooks"] = hooksConfig
		}
	}

	if q.opts.Agents != nil {
		request["agents"] = q.opts.Agents
	}

	switch sp := q.opts.SystemPrompt.(type) {
	case *SystemPromptPreset:
		if sp.ExcludeDynamicSections != nil {
			request["excludeDynamicSections"] = *sp.ExcludeDynamicSections
		}
		if sp.Snapshot != nil {
			request["systemPromptSnapshot"] = *sp.Snapshot
		}
	case *SystemPromptCustom:
		if sp.Snapshot != nil {
			request["systemPromptSnapshot"] = *sp.Snapshot
		}
	}

	// SkillsAll and an omitted value are equivalent on the wire, since
	// neither installs a filter, so only an explicit list is sent.
	if skills, ok := q.opts.Skills.([]string); ok {
		request["skills"] = skills
	}

	if q.opts.ForwardSubagentText {
		request["forwardSubagentText"] = true
	}

	timeout := q.initTimeout()
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := q.sendControlRequest(timeoutCtx, request)
	if err != nil {
		return nil, fmt.Errorf("initialize failed: %w", err)
	}

	q.initResult = resp
	return resp, nil
}

func (q *protocolQuery) InitResult() map[string]any {
	return q.initResult
}

func (q *protocolQuery) Interrupt(ctx context.Context) error {
	_, err := q.sendControlRequest(ctx, map[string]any{"subtype": "interrupt"})
	return err
}

func (q *protocolQuery) SetPermissionMode(ctx context.Context, mode PermissionMode) error {
	_, err := q.sendControlRequest(ctx, map[string]any{
		"subtype": "set_permission_mode",
		"mode":    string(mode),
	})
	return err
}

func (q *protocolQuery) SetModel(ctx context.Context, model string) error {
	_, err := q.sendControlRequest(ctx, map[string]any{
		"subtype": "set_model",
		"model":   model,
	})
	return err
}

func (q *protocolQuery) RewindFiles(ctx context.Context, userMessageID string) error {
	_, err := q.sendControlRequest(ctx, map[string]any{
		"subtype":         "rewind_files",
		"user_message_id": userMessageID,
	})
	return err
}

func (q *protocolQuery) McpStatus(ctx context.Context) (map[string]any, error) {
	return q.sendControlRequest(ctx, map[string]any{"subtype": "mcp_status"})
}

func (q *protocolQuery) GetContextUsage(ctx context.Context) (map[string]any, error) {
	return q.sendControlRequest(ctx, map[string]any{"subtype": "get_context_usage"})
}

func (q *protocolQuery) ReconnectMcpServer(ctx context.Context, serverName string) error {
	_, err := q.sendControlRequest(ctx, map[string]any{
		"subtype": "mcp_reconnect",
		// The wire protocol uses camelCase for this field.
		"serverName": serverName,
	})
	return err
}

func (q *protocolQuery) ToggleMcpServer(ctx context.Context, serverName string, enabled bool) error {
	_, err := q.sendControlRequest(ctx, map[string]any{
		"subtype": "mcp_toggle",
		// The wire protocol uses camelCase for this field.
		"serverName": serverName,
		"enabled":    enabled,
	})
	return err
}

func (q *protocolQuery) StopTask(ctx context.Context, taskID string) error {
	_, err := q.sendControlRequest(ctx, map[string]any{
		"subtype": "stop_task",
		"task_id": taskID,
	})
	return err
}

// setMirrorBatcher attaches a batcher that receives transcript_mirror
// frames. When set, the read loop peels those frames off stdout rather than
// yielding them to consumers, enqueues them on the batcher, and flushes
// before yielding each result message.
func (q *protocolQuery) setMirrorBatcher(b *transcriptMirrorBatcher) {
	q.mirror = b
}

// reportMirrorError surfaces a SessionStore.Append failure as a system
// message. It is called from the batcher's error callback; the dropped batch
// is not retried further, so this is the consumer's only signal. It never
// blocks: when the message buffer is full the error is logged and dropped
// rather than back-pressuring the read loop.
func (q *protocolQuery) reportMirrorError(key *SessionKey, errText string) {
	data := map[string]any{
		"type":       "system",
		"subtype":    "mirror_error",
		"error":      errText,
		"uuid":       newRequestID(),
		"session_id": "",
	}
	if key != nil {
		data["key"] = map[string]any{
			"project_key": key.ProjectKey,
			"session_id":  key.SessionID,
			"subpath":     key.Subpath,
		}
		data["session_id"] = key.SessionID
	}

	msg := &MirrorErrorMessage{
		SystemMessage: SystemMessage{Subtype: "mirror_error", Data: data},
		Key:           key,
		Error:         errText,
	}

	select {
	case q.messages <- msg:
	default:
		log.Printf("agentsdk: dropping mirror_error message (buffer full): %s", errText)
	}
}

func (q *protocolQuery) WriteUserMessage(ctx context.Context, prompt string, sessionID string) error {
	msg := map[string]any{
		"type":               "user",
		"session_id":         sessionID,
		"message":            map[string]any{"role": "user", "content": prompt},
		"parent_tool_use_id": nil,
	}
	if q.opts.VerbatimPrompts {
		// Deliver the text as written: no @path expansion and no
		// slash-command dispatch.
		msg["client_composed"] = true
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal user message: %w", err)
	}
	return q.transport.Write(ctx, string(data))
}

// hasBidirectionalNeeds reports whether the CLI may send control requests
// that need a reply over stdin.
func hasBidirectionalNeeds(opts *ClaudeAgentOptions) bool {
	return len(opts.Hooks) != 0 || opts.CanUseTool != nil
}

// WaitForRunEndAndEndInput closes stdin once the run is over.
//
// When hooks or a CanUseTool callback need replies over stdin, it first waits
// for the end of the run: the CLI's "idle" session state after a result, or,
// from a CLI that reports no session state, the first result with no tracked
// background agents in flight. A result ends a turn, not necessarily the run:
// a background agent keeps running past it, or has just finished and still
// wakes the session for a follow-up turn whose control requests need stdin.
//
// The wait between turns is bounded: if the CLI still reports "running"
// CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS (10 minutes by default, 0 for no
// limit) after a result with no new turn, the run ends anyway. A turn under
// way, a request still being answered, and a tracked background agent still
// in flight stop that clock. The wait also ends if the read loop exits.
func (q *protocolQuery) WaitForRunEndAndEndInput(ctx context.Context) error {
	if hasBidirectionalNeeds(q.opts) {
		if err := q.run.wait(ctx); err != nil {
			return err
		}
	}
	q.run.finish()
	return q.transport.EndInput()
}

func (q *protocolQuery) Done() <-chan struct{} {
	return q.readDone
}

func (q *protocolQuery) readLoop(ctx context.Context) {
	defer close(q.readDone)
	defer close(q.messages)
	// Flush any remaining mirror entries before closing, so an early stdout
	// EOF or transport error does not drop entries batched this turn. Also
	// end the run so a waiter does not stall on an early exit.
	defer func() {
		if q.mirror != nil {
			q.mirror.flush(context.WithoutCancel(ctx))
		}
		q.run.finish()
	}()

	for data, err := range q.transport.ReadMessages(ctx) {
		if err != nil {
			q.messages <- &SystemMessage{
				Subtype: "error",
				Data:    map[string]any{"error": err.Error()},
			}
			continue
		}

		if data == nil {
			continue
		}

		msgType, _ := data["type"].(string)
		switch msgType {
		case "control_response":
			q.handleControlResponse(data)

		case "control_request":
			q.spawnControlRequestHandler(ctx, data)

		case "control_cancel_request":
			// The CLI has abandoned the request, so cancel the handler and
			// write no response.
			if cancelID, _ := data["request_id"].(string); cancelID != "" {
				q.inflightMu.Lock()
				cancel, ok := q.inflight[cancelID]
				delete(q.inflight, cancelID)
				q.inflightMu.Unlock()
				if ok {
					cancel()
				}
			}

		case "transcript_mirror":
			// SessionStore write path: peel mirror frames off stdout and
			// hand them to the batcher rather than yielding to consumers.
			if q.mirror != nil {
				filePath, _ := data["filePath"].(string)
				q.mirror.enqueue(filePath, data["entries"])
			}

		default:
			switch msgType {
			case "system":
				if q.run.observeSystem(data) {
					continue
				}
			case "assistant", "stream_event":
				if data["parent_tool_use_id"] == nil {
					q.run.observeTurnActivity()
				}
			}

			msg, parseErr := parseMessage(data)
			if parseErr != nil {
				q.messages <- &SystemMessage{
					Subtype: "error",
					Data:    map[string]any{"error": parseErr.Error()},
				}
				continue
			}
			if msg == nil {
				continue
			}

			if _, ok := msg.(*ResultMessage); ok {
				// Flush pending mirror entries before yielding the result,
				// so consumers observing it can rely on the SessionStore
				// being up to date for this turn.
				if q.mirror != nil {
					q.mirror.flush(ctx)
				}
				q.run.observeResult()
			}

			q.messages <- msg
		}
	}
}

func (q *protocolQuery) handleControlResponse(data map[string]any) {
	respObj, ok := data["response"].(map[string]any)
	if !ok {
		return
	}
	requestID, _ := respObj["request_id"].(string)
	if requestID == "" {
		return
	}

	q.pendingMu.Lock()
	ch, exists := q.pending[requestID]
	if exists {
		delete(q.pending, requestID)
	}
	q.pendingMu.Unlock()

	if !exists {
		return
	}

	subtype, _ := respObj["subtype"].(string)
	if subtype == "error" {
		errMsg, _ := respObj["error"].(string)
		ch <- controlResult{err: fmt.Errorf("control error: %s", errMsg)}
	} else {
		resp, _ := respObj["response"].(map[string]any)
		ch <- controlResult{response: resp}
	}
}

// spawnControlRequestHandler runs a control request handler in its own
// goroutine and tracks it so a control_cancel_request can abandon it.
func (q *protocolQuery) spawnControlRequestHandler(ctx context.Context, data map[string]any) {
	requestID, _ := data["request_id"].(string)

	handlerCtx, cancel := context.WithCancel(ctx)
	if requestID != "" {
		q.inflightMu.Lock()
		q.inflight[requestID] = cancel
		q.inflightMu.Unlock()
	}

	go func() {
		defer cancel()
		defer func() {
			if requestID != "" {
				q.inflightMu.Lock()
				delete(q.inflight, requestID)
				q.inflightMu.Unlock()
			}
		}()
		q.handleControlRequest(handlerCtx, data)
	}()
}

func (q *protocolQuery) handleControlRequest(ctx context.Context, data map[string]any) {
	requestID, _ := data["request_id"].(string)
	reqBody, _ := data["request"].(map[string]any)
	if reqBody == nil {
		return
	}

	subtype, _ := reqBody["subtype"].(string)
	var response map[string]any
	var respErr error

	switch subtype {
	case "can_use_tool":
		response, respErr = q.handleCanUseTool(ctx, reqBody)
	case "hook_callback":
		response, respErr = q.handleHookCallback(ctx, reqBody)
	default:
		respErr = fmt.Errorf("unknown control request subtype: %s", subtype)
	}

	// A cancelled request has already been abandoned by the CLI, so writing
	// a response would be answering a request nobody is waiting on.
	if ctx.Err() != nil {
		return
	}

	q.sendControlResponse(context.WithoutCancel(ctx), requestID, response, respErr)
}

func (q *protocolQuery) handleCanUseTool(ctx context.Context, req map[string]any) (map[string]any, error) {
	if q.opts.CanUseTool == nil {
		return map[string]any{"behavior": "allow"}, nil
	}

	toolName, _ := req["tool_name"].(string)
	input, _ := req["input"].(map[string]any)

	permCtx := ToolPermissionContext{
		ToolUseID:      stringFromAny(req["tool_use_id"]),
		AgentID:        stringFromAny(req["agent_id"]),
		BlockedPath:    stringFromAny(req["blocked_path"]),
		DecisionReason: stringFromAny(req["decision_reason"]),
		Title:          stringFromAny(req["title"]),
		DisplayName:    stringFromAny(req["display_name"]),
		Description:    stringFromAny(req["description"]),
	}
	if suggestions, ok := req["permission_suggestions"].([]any); ok {
		for _, s := range suggestions {
			if sMap, ok := s.(map[string]any); ok {
				update := parsePermissionUpdateFromMap(sMap)
				permCtx.Suggestions = append(permCtx.Suggestions, update)
			}
		}
	}

	result, err := q.opts.CanUseTool(ctx, toolName, input, permCtx)
	if err != nil {
		return nil, err
	}

	switch r := result.(type) {
	case PermissionAllow:
		resp := map[string]any{"behavior": "allow"}
		if r.UpdatedInput != nil {
			resp["updatedInput"] = r.UpdatedInput
		} else {
			resp["updatedInput"] = input
		}
		if r.UpdatedPermissions != nil {
			resp["updatedPermissions"] = r.UpdatedPermissions
		}
		return resp, nil
	case PermissionDeny:
		return map[string]any{
			"behavior":  "deny",
			"message":   r.Message,
			"interrupt": r.Interrupt,
		}, nil
	default:
		return map[string]any{"behavior": "allow"}, nil
	}
}

func (q *protocolQuery) handleHookCallback(ctx context.Context, req map[string]any) (map[string]any, error) {
	callbackID, _ := req["callback_id"].(string)
	toolUseID, _ := req["tool_use_id"].(string)

	callback, ok := q.hookCallbacks[callbackID]
	if !ok {
		return nil, fmt.Errorf("unknown hook callback: %s", callbackID)
	}

	hookInput, err := parseHookInput(req["input"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse hook input: %w", err)
	}

	output, err := callback(ctx, hookInput, toolUseID)
	if err != nil {
		return nil, err
	}
	if output == nil {
		return map[string]any{}, nil
	}

	data, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal hook output: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal hook output: %w", err)
	}

	return result, nil
}

func (q *protocolQuery) sendControlRequest(ctx context.Context, request map[string]any) (map[string]any, error) {
	requestID := q.nextRequestID()

	ch := make(chan controlResult, 1)
	q.pendingMu.Lock()
	q.pending[requestID] = ch
	q.pendingMu.Unlock()

	envelope := map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request":    request,
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		q.pendingMu.Lock()
		delete(q.pending, requestID)
		q.pendingMu.Unlock()
		return nil, fmt.Errorf("failed to marshal control request: %w", err)
	}

	if err := q.transport.Write(ctx, string(data)); err != nil {
		q.pendingMu.Lock()
		delete(q.pending, requestID)
		q.pendingMu.Unlock()
		return nil, fmt.Errorf("failed to write control request: %w", err)
	}

	select {
	case result := <-ch:
		return result.response, result.err
	case <-ctx.Done():
		q.pendingMu.Lock()
		delete(q.pending, requestID)
		q.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-q.readDone:
		q.pendingMu.Lock()
		delete(q.pending, requestID)
		q.pendingMu.Unlock()
		return nil, fmt.Errorf("transport closed before response received")
	}
}

func (q *protocolQuery) sendControlResponse(ctx context.Context, requestID string, response map[string]any, respErr error) {
	var payload map[string]any
	if respErr != nil {
		payload = map[string]any{
			"subtype":    "error",
			"request_id": requestID,
			"error":      respErr.Error(),
		}
	} else {
		payload = map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   response,
		}
	}

	envelope := map[string]any{
		"type":     "control_response",
		"response": payload,
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return
	}
	q.transport.Write(ctx, string(data))
}

func (q *protocolQuery) nextRequestID() string {
	counter := q.reqCounter.Add(1)
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("req_%d_%s", counter, hex.EncodeToString(b))
}

// newRequestID returns a random identifier for SDK-synthesized messages.
func newRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (q *protocolQuery) buildHooksConfig() map[string]any {
	config := make(map[string]any)

	for event, matchers := range q.opts.Hooks {
		var matcherConfigs []map[string]any
		for _, m := range matchers {
			mc := map[string]any{}
			if m.Matcher != "" {
				mc["matcher"] = m.Matcher
			}
			if m.Timeout != nil {
				mc["timeout"] = *m.Timeout
			}

			var callbackIDs []string
			for _, hook := range m.Hooks {
				id := fmt.Sprintf("hook_%d", len(q.hookCallbacks))
				q.hookCallbacks[id] = hook
				callbackIDs = append(callbackIDs, id)
			}
			mc["hookCallbackIds"] = callbackIDs

			matcherConfigs = append(matcherConfigs, mc)
		}
		config[string(event)] = matcherConfigs
	}

	return config
}

func (q *protocolQuery) initTimeout() time.Duration {
	if envTimeout := os.Getenv("CLAUDE_CODE_STREAM_CLOSE_TIMEOUT"); envTimeout != "" {
		if secs, err := strconv.Atoi(envTimeout); err == nil && secs > 0 {
			d := time.Duration(secs) * time.Second
			if d < defaultInitTimeout {
				return defaultInitTimeout
			}
			return d
		}
	}
	return defaultInitTimeout
}

func parsePermissionUpdateFromMap(data map[string]any) PermissionUpdate {
	update := PermissionUpdate{}
	if t, ok := data["type"].(string); ok {
		update.Type = PermissionUpdateType(t)
	}
	if b, ok := data["behavior"].(string); ok {
		update.Behavior = PermissionBehavior(b)
	}
	if m, ok := data["mode"].(string); ok {
		update.Mode = PermissionMode(m)
	}
	if d, ok := data["destination"].(string); ok {
		update.Destination = PermissionUpdateDestination(d)
	}
	if rules, ok := data["rules"].([]any); ok {
		for _, r := range rules {
			if rMap, ok := r.(map[string]any); ok {
				rv := PermissionRuleValue{}
				rv.ToolName, _ = rMap["toolName"].(string)
				rv.RuleContent, _ = rMap["ruleContent"].(string)
				update.Rules = append(update.Rules, rv)
			}
		}
	}
	if dirs, ok := data["directories"].([]any); ok {
		for _, d := range dirs {
			if s, ok := d.(string); ok {
				update.Directories = append(update.Directories, s)
			}
		}
	}
	return update
}

func parseHookInput(v any) (HookInput, error) {
	data, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hook input is not a map")
	}

	eventName, _ := data["hook_event_name"].(string)
	switch HookEvent(eventName) {
	case HookEventPreToolUse:
		return decodeHookInput[PreToolUseHookInput](data)
	case HookEventPostToolUse:
		return decodeHookInput[PostToolUseHookInput](data)
	case HookEventPostToolUseFailure:
		return decodeHookInput[PostToolUseFailureHookInput](data)
	case HookEventUserPromptSubmit:
		return decodeHookInput[UserPromptSubmitHookInput](data)
	case HookEventStop:
		return decodeHookInput[StopHookInput](data)
	case HookEventSubagentStop:
		return decodeHookInput[SubagentStopHookInput](data)
	case HookEventPreCompact:
		return decodeHookInput[PreCompactHookInput](data)
	case HookEventNotification:
		return decodeHookInput[NotificationHookInput](data)
	case HookEventSubagentStart:
		return decodeHookInput[SubagentStartHookInput](data)
	case HookEventPermissionRequest:
		return decodeHookInput[PermissionRequestHookInput](data)
	default:
		return nil, fmt.Errorf("unknown hook event: %s", eventName)
	}
}

// decodeHookInput decodes a hook input map into T via its JSON tags. A field
// of an unexpected type is left at its zero value rather than failing the
// hook, as encoding/json keeps decoding the remaining fields.
func decodeHookInput[T HookInput](data map[string]any) (HookInput, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var input T
	if err := json.Unmarshal(raw, &input); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return nil, fmt.Errorf("decoding %s hook input: %w", data["hook_event_name"], err)
		}
	}
	return input, nil
}
