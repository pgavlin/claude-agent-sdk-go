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

	return msg, nil
}

func parseSystemMessage(data map[string]any) (*SystemMessage, error) {
	msg := &SystemMessage{}
	msg.Subtype, _ = data["subtype"].(string)
	msg.Data = data
	return msg, nil
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

	return msg, nil
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
