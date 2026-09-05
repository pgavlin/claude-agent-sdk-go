package agentsdk

import "fmt"

// parseMessage converts a raw JSON map into a typed Message.
// Returns (nil, nil) for unknown message types (forward-compatible).
func parseMessage(data map[string]any) (Message, error) {
	typ, _ := data["type"].(string)
	switch typ {
	case "user":
		return parseUserMessage(data)
	case "assistant":
		return parseAssistantMessage(data)
	case "system":
		return parseSystemMessage(data)
	case "result":
		return parseResultMessage(data)
	case "stream_event":
		return parseStreamEvent(data)
	case "rate_limit_event":
		return parseRateLimitEvent(data)
	case "conversation_reset":
		return parseConversationResetMessage(data)
	default:
		return nil, nil
	}
}

func parseUserMessage(data map[string]any) (*UserMessage, error) {
	msg := &UserMessage{}

	if msgObj, ok := data["message"].(map[string]any); ok {
		msg.Content = parseContentField(msgObj["content"])
	} else {
		msg.Content = parseContentField(data["content"])
	}

	msg.UUID, _ = data["uuid"].(string)
	msg.ParentToolUseID, _ = data["parent_tool_use_id"].(string)
	msg.SessionID, _ = data["session_id"].(string)

	if tur, ok := data["tool_use_result"].(map[string]any); ok {
		msg.ToolUseResult = tur
	}

	msg.Origin = parseMessageOrigin(data["origin"])

	return msg, nil
}

func parseAssistantMessage(data map[string]any) (*AssistantMessage, error) {
	msg := &AssistantMessage{}

	var msgObj map[string]any
	if m, ok := data["message"].(map[string]any); ok {
		msgObj = m
	} else {
		return nil, NewMessageParseError("assistant message missing 'message' field", data)
	}

	msg.Model, _ = msgObj["model"].(string)

	if contentList, ok := msgObj["content"].([]any); ok {
		for _, item := range contentList {
			blockMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			block, err := parseContentBlock(blockMap)
			if err != nil {
				return nil, err
			}
			if block != nil {
				msg.Content = append(msg.Content, block)
			}
		}
	}

	if errStr, ok := data["error"].(string); ok {
		msg.Error = AssistantMessageError(errStr)
	}

	msg.ParentToolUseID, _ = data["parent_tool_use_id"].(string)

	msg.Usage, _ = msgObj["usage"].(map[string]any)
	msg.MessageID, _ = msgObj["id"].(string)
	msg.StopReason, _ = msgObj["stop_reason"].(string)
	msg.SessionID, _ = data["session_id"].(string)
	msg.UUID, _ = data["uuid"].(string)

	return msg, nil
}

func parseSystemMessage(data map[string]any) (Message, error) {
	subtype, _ := data["subtype"].(string)
	base := SystemMessage{Subtype: subtype, Data: data}

	switch subtype {
	case "task_started":
		return &TaskStartedMessage{
			SystemMessage: base,
			TaskID:        stringFromAny(data["task_id"]),
			Description:   stringFromAny(data["description"]),
			UUID:          stringFromAny(data["uuid"]),
			SessionID:     stringFromAny(data["session_id"]),
			ToolUseID:     stringFromAny(data["tool_use_id"]),
			TaskType:      stringFromAny(data["task_type"]),
		}, nil

	case "task_progress":
		return &TaskProgressMessage{
			SystemMessage: base,
			TaskID:        stringFromAny(data["task_id"]),
			Description:   stringFromAny(data["description"]),
			Usage:         parseTaskUsageValue(data["usage"]),
			UUID:          stringFromAny(data["uuid"]),
			SessionID:     stringFromAny(data["session_id"]),
			ToolUseID:     stringFromAny(data["tool_use_id"]),
			LastToolName:  stringFromAny(data["last_tool_name"]),
		}, nil

	case "task_notification":
		msg := &TaskNotificationMessage{
			SystemMessage: base,
			TaskID:        stringFromAny(data["task_id"]),
			Status:        TaskNotificationStatus(stringFromAny(data["status"])),
			OutputFile:    stringFromAny(data["output_file"]),
			Summary:       stringFromAny(data["summary"]),
			UUID:          stringFromAny(data["uuid"]),
			SessionID:     stringFromAny(data["session_id"]),
			ToolUseID:     stringFromAny(data["tool_use_id"]),
		}
		if usage := parseTaskUsage(data["usage"]); usage != nil {
			msg.Usage = usage
		}
		return msg, nil

	case "task_updated":
		patch, _ := data["patch"].(map[string]any)
		msg := &TaskUpdatedMessage{
			SystemMessage: base,
			TaskID:        stringFromAny(data["task_id"]),
			Patch:         patch,
			SessionID:     stringFromAny(data["session_id"]),
			UUID:          stringFromAny(data["uuid"]),
		}
		// The status lives inside the patch, and is absent when the patch
		// carries other fields.
		if status := stringFromAny(patch["status"]); status != "" {
			msg.Status = TaskUpdatedStatus(status)
		}
		return msg, nil

	case "hook_started", "hook_response":
		return &HookEventMessage{
			SystemMessage: base,
			HookEventName: stringFromAny(data["hook_event"]),
			SessionID:     stringFromAny(data["session_id"]),
			UUID:          stringFromAny(data["uuid"]),
		}, nil

	case "mirror_error":
		// Synthesized by the SDK when a SessionStore append fails; the CLI
		// subprocess never emits it.
		msg := &MirrorErrorMessage{
			SystemMessage: base,
			Error:         stringFromAny(data["error"]),
		}
		if key := parseSessionKey(data["key"]); key != nil {
			msg.Key = key
		}
		return msg, nil

	default:
		return &base, nil
	}
}

// parseSessionKey converts a raw origin map into a SessionKey.
func parseSessionKey(v any) *SessionKey {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return &SessionKey{
		ProjectKey: stringFromAny(raw["project_key"]),
		SessionID:  stringFromAny(raw["session_id"]),
		Subpath:    stringFromAny(raw["subpath"]),
	}
}

// parseTaskUsage converts a raw usage map into a TaskUsage, returning nil
// when the value is absent or not an object.
func parseTaskUsage(v any) *TaskUsage {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	usage := taskUsageFromMap(raw)
	return &usage
}

// parseTaskUsageValue is parseTaskUsage for a required field, yielding a
// zero TaskUsage when the value is absent.
func parseTaskUsageValue(v any) TaskUsage {
	raw, _ := v.(map[string]any)
	return taskUsageFromMap(raw)
}

func taskUsageFromMap(raw map[string]any) TaskUsage {
	return TaskUsage{
		TotalTokens: intFromAny(raw["total_tokens"]),
		ToolUses:    intFromAny(raw["tool_uses"]),
		DurationMS:  intFromAny(raw["duration_ms"]),
	}
}

// parseMessageOrigin converts a raw origin map into a MessageOrigin,
// returning nil when the CLI did not attribute the message.
func parseMessageOrigin(v any) *MessageOrigin {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return &MessageOrigin{
		Kind:            MessageOriginKind(stringFromAny(raw["kind"])),
		Server:          stringFromAny(raw["server"]),
		From:            stringFromAny(raw["from"]),
		Name:            stringFromAny(raw["name"]),
		FromSession:     stringFromAny(raw["fromSession"]),
		SenderTaskID:    stringFromAny(raw["senderTaskId"]),
		Body:            stringFromAny(raw["body"]),
		VerifiedPeerPid: intFromAny(raw["verifiedPeerPid"]),
		Subkind:         TaskNotificationOriginSubkind(stringFromAny(raw["subkind"])),
		Raw:             raw,
	}
}

func parseResultMessage(data map[string]any) (*ResultMessage, error) {
	msg := &ResultMessage{}
	msg.Subtype, _ = data["subtype"].(string)
	msg.DurationMS = intFromAny(data["duration_ms"])
	msg.DurationAPIMS = intFromAny(data["duration_api_ms"])
	msg.IsError, _ = data["is_error"].(bool)
	msg.NumTurns = intFromAny(data["num_turns"])
	msg.SessionID, _ = data["session_id"].(string)

	if cost, ok := data["total_cost_usd"].(float64); ok {
		msg.TotalCostUSD = &cost
	}
	if usage, ok := data["usage"].(map[string]any); ok {
		msg.Usage = usage
	}
	msg.Result, _ = data["result"].(string)
	msg.StructuredOutput = data["structured_output"]

	msg.StopReason, _ = data["stop_reason"].(string)
	msg.ModelUsage = parseModelUsage(data["modelUsage"])
	msg.PermissionDenials, _ = data["permission_denials"].([]any)
	msg.DeferredToolUse = parseDeferredToolUse(data["deferred_tool_use"])
	msg.Errors = normalizeResultErrors(data["errors"])
	msg.APIErrorStatus = intFromAny(data["api_error_status"])
	msg.UUID, _ = data["uuid"].(string)
	msg.TerminalReason, _ = data["terminal_reason"].(string)
	msg.Origin = parseMessageOrigin(data["origin"])

	return msg, nil
}

// parseModelUsage converts the CLI's modelUsage map into typed per-model
// usage, keyed by model string.
func parseModelUsage(v any) map[string]ModelUsage {
	raw, ok := v.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]ModelUsage, len(raw))
	for model, entry := range raw {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		out[model] = ModelUsage{
			InputTokens:              intFromAny(fields["inputTokens"]),
			OutputTokens:             intFromAny(fields["outputTokens"]),
			CacheReadInputTokens:     intFromAny(fields["cacheReadInputTokens"]),
			CacheCreationInputTokens: intFromAny(fields["cacheCreationInputTokens"]),
			WebSearchRequests:        intFromAny(fields["webSearchRequests"]),
			CostUSD:                  floatFromAny(fields["costUSD"]),
			ContextWindow:            intFromAny(fields["contextWindow"]),
			MaxOutputTokens:          intFromAny(fields["maxOutputTokens"]),
			CanonicalModel:           stringFromAny(fields["canonicalModel"]),
			Provider:                 stringFromAny(fields["provider"]),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseDeferredToolUse converts a deferred_tool_use payload into its typed
// form, returning nil when absent.
func parseDeferredToolUse(v any) *DeferredToolUse {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	input, _ := raw["input"].(map[string]any)
	return &DeferredToolUse{
		ID:    stringFromAny(raw["id"]),
		Name:  stringFromAny(raw["name"]),
		Input: input,
	}
}

// parseRateLimitEvent parses a rate_limit_event message.
func parseRateLimitEvent(data map[string]any) (*RateLimitEvent, error) {
	info, ok := data["rate_limit_info"].(map[string]any)
	if !ok {
		return nil, NewMessageParseError(
			"missing required field in rate_limit_event message: rate_limit_info", data)
	}
	status, ok := info["status"].(string)
	if !ok {
		return nil, NewMessageParseError(
			"missing required field in rate_limit_event message: rate_limit_info.status", data)
	}
	uuid, ok := data["uuid"].(string)
	if !ok {
		return nil, NewMessageParseError(
			"missing required field in rate_limit_event message: uuid", data)
	}
	sessionID, ok := data["session_id"].(string)
	if !ok {
		return nil, NewMessageParseError(
			"missing required field in rate_limit_event message: session_id", data)
	}

	return &RateLimitEvent{
		RateLimitInfo: RateLimitInfo{
			Status:                RateLimitStatus(status),
			ResetsAt:              int64PtrFromAny(info["resetsAt"]),
			RateLimitType:         RateLimitType(stringFromAny(info["rateLimitType"])),
			Utilization:           floatPtrFromAny(info["utilization"]),
			OverageStatus:         RateLimitStatus(stringFromAny(info["overageStatus"])),
			OverageResetsAt:       int64PtrFromAny(info["overageResetsAt"]),
			OverageDisabledReason: stringFromAny(info["overageDisabledReason"]),
			Raw:                   info,
		},
		UUID:      uuid,
		SessionID: sessionID,
	}, nil
}

// parseConversationResetMessage parses a conversation_reset message.
func parseConversationResetMessage(data map[string]any) (*ConversationResetMessage, error) {
	return &ConversationResetMessage{
		NewConversationID: stringFromAny(data["new_conversation_id"]),
		UUID:              stringFromAny(data["uuid"]),
		SessionID:         stringFromAny(data["session_id"]),
	}, nil
}

func parseStreamEvent(data map[string]any) (*StreamEvent, error) {
	msg := &StreamEvent{}
	msg.UUID, _ = data["uuid"].(string)
	msg.SessionID, _ = data["session_id"].(string)
	msg.ParentToolUseID, _ = data["parent_tool_use_id"].(string)

	if event, ok := data["event"].(map[string]any); ok {
		msg.Event = event
	}

	return msg, nil
}

// parseContentBlock converts a raw JSON map into a typed ContentBlock.
func parseContentBlock(data map[string]any) (ContentBlock, error) {
	typ, _ := data["type"].(string)
	switch typ {
	case "text":
		text, _ := data["text"].(string)
		return TextBlock{Type: "text", Text: text}, nil
	case "thinking":
		thinking, _ := data["thinking"].(string)
		signature, _ := data["signature"].(string)
		return ThinkingBlock{Type: "thinking", Thinking: thinking, Signature: signature}, nil
	case "tool_use":
		id, _ := data["id"].(string)
		name, _ := data["name"].(string)
		input, _ := data["input"].(map[string]any)
		return ToolUseBlock{Type: "tool_use", ID: id, Name: name, Input: input}, nil
	case "tool_result":
		toolUseID, _ := data["tool_use_id"].(string)
		block := ToolResultBlock{Type: "tool_result", ToolUseID: toolUseID, Content: data["content"]}
		if isErr, ok := data["is_error"].(bool); ok {
			block.IsError = &isErr
		}
		return block, nil
	case "server_tool_use":
		id, _ := data["id"].(string)
		name, _ := data["name"].(string)
		input, _ := data["input"].(map[string]any)
		return ServerToolUseBlock{
			Type:  "server_tool_use",
			ID:    id,
			Name:  ServerToolName(name),
			Input: input,
		}, nil
	case "advisor_tool_result":
		toolUseID, _ := data["tool_use_id"].(string)
		content, _ := data["content"].(map[string]any)
		return ServerToolResultBlock{
			Type:      "advisor_tool_result",
			ToolUseID: toolUseID,
			Content:   content,
		}, nil
	default:
		return nil, nil
	}
}

func parseContentField(v any) any {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var blocks []ContentBlock
		for _, item := range c {
			if blockMap, ok := item.(map[string]any); ok {
				block, _ := parseContentBlock(blockMap)
				if block != nil {
					blocks = append(blocks, block)
				}
			}
		}
		return blocks
	default:
		return fmt.Sprintf("%v", v)
	}
}

// stringFromAny reads a string value, yielding "" for absent or
// non-string values.
func stringFromAny(v any) string {
	s, _ := v.(string)
	return s
}

// floatFromAny reads a numeric value as a float64, yielding 0 when absent.
func floatFromAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

// floatPtrFromAny reads an optional numeric value, yielding nil when absent.
func floatPtrFromAny(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case int:
		f := float64(n)
		return &f
	default:
		return nil
	}
}

// int64PtrFromAny reads an optional integer value, yielding nil when absent.
func int64PtrFromAny(v any) *int64 {
	switch n := v.(type) {
	case float64:
		i := int64(n)
		return &i
	case int:
		i := int64(n)
		return &i
	case int64:
		return &n
	default:
		return nil
	}
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
