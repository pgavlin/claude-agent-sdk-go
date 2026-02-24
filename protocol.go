package agentsdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	firstResult     chan struct{}
	firstResultOnce sync.Once
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
		firstResult:   make(chan struct{}),
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

func (q *protocolQuery) WriteUserMessage(ctx context.Context, prompt string, sessionID string) error {
	msg := map[string]any{
		"type":               "user",
		"session_id":         sessionID,
		"message":            map[string]any{"role": "user", "content": prompt},
		"parent_tool_use_id": nil,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal user message: %w", err)
	}
	return q.transport.Write(ctx, string(data))
}

func (q *protocolQuery) EndInput() error {
	return q.transport.EndInput()
}

func (q *protocolQuery) WaitForFirstResult(ctx context.Context) error {
	select {
	case <-q.firstResult:
		return nil
	case <-q.readDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *protocolQuery) Done() <-chan struct{} {
	return q.readDone
}

func (q *protocolQuery) readLoop(ctx context.Context) {
	defer close(q.readDone)
	defer close(q.messages)

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
			go q.handleControlRequest(ctx, data)
		case "control_cancel_request":
			continue
		default:
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
				q.firstResultOnce.Do(func() {
					close(q.firstResult)
				})
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

	q.sendControlResponse(ctx, requestID, response, respErr)
}

func (q *protocolQuery) handleCanUseTool(ctx context.Context, req map[string]any) (map[string]any, error) {
	if q.opts.CanUseTool == nil {
		return map[string]any{"behavior": "allow"}, nil
	}

	toolName, _ := req["tool_name"].(string)
	input, _ := req["input"].(map[string]any)

	permCtx := ToolPermissionContext{}
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

	base := BaseHookFields{}
	base.SessionID, _ = data["session_id"].(string)
	base.TranscriptPath, _ = data["transcript_path"].(string)
	base.CWD, _ = data["cwd"].(string)
	base.PermissionMode, _ = data["permission_mode"].(string)

	eventName, _ := data["hook_event_name"].(string)
	switch HookEvent(eventName) {
	case HookEventPreToolUse:
		input := PreToolUseHookInput{BaseHookFields: base}
		input.ToolName, _ = data["tool_name"].(string)
		input.ToolInput, _ = data["tool_input"].(map[string]any)
		input.ToolUseID, _ = data["tool_use_id"].(string)
		return input, nil
	case HookEventPostToolUse:
		input := PostToolUseHookInput{BaseHookFields: base}
		input.ToolName, _ = data["tool_name"].(string)
		input.ToolInput, _ = data["tool_input"].(map[string]any)
		input.ToolResponse = data["tool_response"]
		input.ToolUseID, _ = data["tool_use_id"].(string)
		return input, nil
	case HookEventPostToolUseFailure:
		input := PostToolUseFailureHookInput{BaseHookFields: base}
		input.ToolName, _ = data["tool_name"].(string)
		input.ToolInput, _ = data["tool_input"].(map[string]any)
		input.ToolUseID, _ = data["tool_use_id"].(string)
		input.Error, _ = data["error"].(string)
		input.IsInterrupt, _ = data["is_interrupt"].(bool)
		return input, nil
	case HookEventUserPromptSubmit:
		input := UserPromptSubmitHookInput{BaseHookFields: base}
		input.Prompt, _ = data["prompt"].(string)
		return input, nil
	case HookEventStop:
		input := StopHookInput{BaseHookFields: base}
		input.StopHookActive, _ = data["stop_hook_active"].(bool)
		return input, nil
	case HookEventSubagentStop:
		input := SubagentStopHookInput{BaseHookFields: base}
		input.StopHookActive, _ = data["stop_hook_active"].(bool)
		input.AgentID, _ = data["agent_id"].(string)
		input.AgentTranscriptPath, _ = data["agent_transcript_path"].(string)
		input.AgentType, _ = data["agent_type"].(string)
		return input, nil
	case HookEventPreCompact:
		input := PreCompactHookInput{BaseHookFields: base}
		input.Trigger, _ = data["trigger"].(string)
		input.CustomInstructions, _ = data["custom_instructions"].(string)
		return input, nil
	case HookEventNotification:
		input := NotificationHookInput{BaseHookFields: base}
		input.Message, _ = data["message"].(string)
		input.Title, _ = data["title"].(string)
		input.NotificationType, _ = data["notification_type"].(string)
		return input, nil
	case HookEventSubagentStart:
		input := SubagentStartHookInput{BaseHookFields: base}
		input.AgentID, _ = data["agent_id"].(string)
		input.AgentType, _ = data["agent_type"].(string)
		return input, nil
	case HookEventPermissionRequest:
		input := PermissionRequestHookInput{BaseHookFields: base}
		input.ToolName, _ = data["tool_name"].(string)
		input.ToolInput, _ = data["tool_input"].(map[string]any)
		if ps, ok := data["permission_suggestions"].([]any); ok {
			input.PermissionSuggestions = ps
		}
		return input, nil
	default:
		return nil, fmt.Errorf("unknown hook event: %s", eventName)
	}
}
