package translate

import (
	"encoding/json"
	"fmt"
	"strings"
)

type responsesOpenAIStreamState struct {
	Started       bool
	Stopped       bool
	ID            string
	Model         string
	Created       any
	NextToolIndex int
	HasToolCalls  bool
	Items         map[string]*responsesOpenAIItem
}

type responsesOpenAIItem struct {
	ToolIndex int
	Announced bool
	SawDelta  bool
	CallID    string
	Name      string
}

func responsesStreamToOpenAI(model string, frame []byte, state *any) ([][]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("Responses-to-OpenAI stream translation requires state")
	}
	streamState, ok := (*state).(*responsesOpenAIStreamState)
	if !ok {
		streamState = &responsesOpenAIStreamState{
			ID:      "chatcmpl-copilot",
			Model:   model,
			Created: 0,
			Items:   make(map[string]*responsesOpenAIItem),
		}
		*state = streamState
	}
	event, data, done, err := parseSSEFrame(frame)
	if err != nil {
		return nil, err
	}
	if done {
		if streamState.Stopped {
			return nil, nil
		}
		return nil, fmt.Errorf("Responses stream ended before a terminal response event")
	}
	if len(data) == 0 {
		return nil, nil
	}
	payload, err := decodeObject(data)
	if err != nil {
		return nil, fmt.Errorf("decode Responses SSE event %q: %w", event, err)
	}
	if event == "" {
		event = stringValue(payload["type"])
	}

	switch event {
	case "error", "response.failed":
		errorObject := objectValue(payload["error"])
		if response, okResponse := payload["response"].(map[string]any); okResponse {
			if nested := objectValue(response["error"]); len(nested) > 0 {
				errorObject = nested
			}
		}
		message := firstNonEmptyString(stringValue(errorObject["message"]), stringValue(payload["message"]), "unknown upstream stream error")
		return nil, fmt.Errorf("Copilot Responses stream failed: %s", message)
	}

	var out [][]byte
	responseObject := objectValue(payload["response"])
	if !streamState.Started && strings.HasPrefix(event, "response.") {
		out = append(out, streamState.start(responseObject, payload)...)
	}

	switch event {
	case "response.output_item.added":
		item := objectValue(payload["item"])
		switch stringValue(item["type"]) {
		case "function_call", "custom_tool_call":
			toolItem := streamState.item(itemKey(payload))
			toolItem.CallID = firstNonEmptyString(firstString(item, "call_id", "id"), toolItem.CallID)
			toolItem.Name = firstNonEmptyString(stringValue(item["name"]), toolItem.Name)
			if toolItem.Name != "" {
				out = append(out, streamState.announceTool(toolItem)...)
			}
		}
	case "response.content_part.added":
		part := objectValue(payload["part"])
		switch stringValue(part["type"]) {
		case "output_text", "text":
			if text := rawStringValue(part["text"]); text != "" {
				out = append(out, streamState.chunk(map[string]any{"content": text}, nil, nil))
			}
		case "refusal":
			if text := rawStringValue(part["refusal"]); text != "" {
				out = append(out, streamState.chunk(map[string]any{"refusal": text}, nil, nil))
			}
		}
	case "response.output_text.delta":
		if delta := rawStringValue(payload["delta"]); delta != "" {
			out = append(out, streamState.chunk(map[string]any{"content": delta}, nil, nil))
		}
	case "response.refusal.delta":
		if delta := rawStringValue(payload["delta"]); delta != "" {
			out = append(out, streamState.chunk(map[string]any{"refusal": delta}, nil, nil))
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if delta := rawStringValue(payload["delta"]); delta != "" {
			out = append(out, streamState.chunk(map[string]any{"reasoning_content": delta}, nil, nil))
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		toolItem := streamState.item(itemKey(payload))
		toolItem.CallID = firstNonEmptyString(toolItem.CallID, stringValue(payload["call_id"]), stringValue(payload["item_id"]))
		toolItem.Name = firstNonEmptyString(toolItem.Name, stringValue(payload["name"]))
		out = append(out, streamState.announceTool(toolItem)...)
		if delta := rawStringValue(payload["delta"]); delta != "" {
			toolItem.SawDelta = true
			out = append(out, streamState.chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index":    toolItem.ToolIndex,
				"function": map[string]any{"arguments": delta},
			}}}, nil, nil))
		}
	case "response.output_item.done":
		item := objectValue(payload["item"])
		switch stringValue(item["type"]) {
		case "reasoning":
			key := itemKey(payload)
			if existing := streamState.Items[key]; existing == nil || !existing.SawDelta {
				if text := responsesReasoningText(item); text != "" {
					out = append(out, streamState.chunk(map[string]any{"reasoning_content": text}, nil, nil))
				}
			}
		case "function_call", "custom_tool_call":
			toolItem := streamState.item(itemKey(payload))
			toolItem.CallID = firstNonEmptyString(toolItem.CallID, firstString(item, "call_id", "id"))
			toolItem.Name = firstNonEmptyString(toolItem.Name, stringValue(item["name"]))
			out = append(out, streamState.announceTool(toolItem)...)
			if arguments := firstRawString(item, "arguments", "input"); arguments != "" && !toolItem.SawDelta {
				toolItem.SawDelta = true
				out = append(out, streamState.chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index":    toolItem.ToolIndex,
					"function": map[string]any{"arguments": arguments},
				}}}, nil, nil))
			}
		}
	case "response.completed", "response.incomplete":
		if event == "response.completed" {
			if errFailure := responsesFailure(responseObject); errFailure != nil {
				return nil, errFailure
			}
		}
		out = append(out, streamState.finish(responseObject)...)
	}
	return out, nil
}

func (s *responsesOpenAIStreamState) start(response, payload map[string]any) [][]byte {
	if s.Started {
		return nil
	}
	s.Started = true
	s.ID = firstNonEmptyString(stringValue(response["id"]), stringValue(payload["response_id"]), "chatcmpl-copilot")
	s.Model = firstNonEmptyString(stringValue(response["model"]), s.Model)
	s.Created = numberValue(response["created_at"])
	return [][]byte{s.chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil)}
}

func (s *responsesOpenAIStreamState) item(key string) *responsesOpenAIItem {
	if key == "" {
		key = fmt.Sprintf("tool:%d", len(s.Items))
	}
	if existing := s.Items[key]; existing != nil {
		return existing
	}
	created := &responsesOpenAIItem{ToolIndex: s.NextToolIndex}
	s.NextToolIndex++
	s.Items[key] = created
	return created
}

func (s *responsesOpenAIStreamState) announceTool(item *responsesOpenAIItem) [][]byte {
	if item.Announced {
		return nil
	}
	item.Announced = true
	s.HasToolCalls = true
	return [][]byte{s.chunk(map[string]any{"tool_calls": []any{map[string]any{
		"index": item.ToolIndex,
		"id":    item.CallID,
		"type":  "function",
		"function": map[string]any{
			"name":      item.Name,
			"arguments": "",
		},
	}}}, nil, nil)}
}

func (s *responsesOpenAIStreamState) finish(response map[string]any) [][]byte {
	if s.Stopped {
		return nil
	}
	s.Stopped = true
	usage := responsesUsageToOpenAI(objectValue(response["usage"]))
	return [][]byte{s.chunk(map[string]any{}, responsesFinishReason(response, s.HasToolCalls), usage)}
}

func (s *responsesOpenAIStreamState) chunk(delta map[string]any, finishReason any, usage map[string]any) []byte {
	payload := map[string]any{
		"id":      s.ID,
		"object":  "chat.completion.chunk",
		"created": s.Created,
		"model":   s.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finishReason,
		}},
	}
	if usage != nil {
		payload["usage"] = usage
	}
	data, _ := json.Marshal(payload)
	return data
}
