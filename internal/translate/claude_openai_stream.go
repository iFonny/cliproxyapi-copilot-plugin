package translate

import (
	"fmt"
)

type claudeOpenAIStreamState struct {
	Started       bool
	Stopped       bool
	ID            string
	Model         string
	InputTokens   any
	OutputTokens  any
	NextToolIndex int
	HasToolCalls  bool
	Blocks        map[string]*claudeOpenAIBlock
}

type claudeOpenAIBlock struct {
	Kind      string
	ToolIndex int
}

// claudeStreamToOpenAI replaces the official Claude to Chat Completions stream
// transformer, which drops any frame that does not begin with "data:" and so
// discards every event of a real Claude SSE stream.
func claudeStreamToOpenAI(model string, frame []byte, state *any) ([][]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("Claude-to-OpenAI stream translation requires state")
	}
	streamState, ok := (*state).(*claudeOpenAIStreamState)
	if !ok {
		streamState = &claudeOpenAIStreamState{
			ID:     "chatcmpl-copilot",
			Model:  model,
			Blocks: make(map[string]*claudeOpenAIBlock),
		}
		*state = streamState
	}
	event, data, done, err := parseSSEFrame(frame)
	if err != nil {
		return nil, err
	}
	if done || len(data) == 0 {
		return nil, nil
	}
	payload, err := decodeObject(data)
	if err != nil {
		return nil, fmt.Errorf("decode Claude SSE event %q: %w", event, err)
	}
	if event == "" {
		event = stringValue(payload["type"])
	}

	switch event {
	case "error":
		errorObject := objectValue(payload["error"])
		message := firstNonEmptyString(stringValue(errorObject["message"]), stringValue(payload["message"]), "unknown upstream stream error")
		return nil, fmt.Errorf("Copilot Claude stream failed: %s", message)
	case "ping":
		return nil, nil
	}

	var out [][]byte
	switch event {
	case "message_start":
		message := objectValue(payload["message"])
		streamState.ID = firstNonEmptyString(stringValue(message["id"]), streamState.ID)
		streamState.Model = firstNonEmptyString(stringValue(message["model"]), streamState.Model)
		streamState.InputTokens = objectValue(message["usage"])["input_tokens"]
		out = append(out, streamState.start()...)
	case "content_block_start":
		out = append(out, streamState.start()...)
		contentBlock := objectValue(payload["content_block"])
		key := numberKey(payload["index"])
		switch stringValue(contentBlock["type"]) {
		case "text":
			streamState.block(key, "text")
			if text := rawStringValue(contentBlock["text"]); text != "" {
				out = append(out, streamState.chunk(map[string]any{"content": text}, nil, nil))
			}
		case "thinking", "redacted_thinking":
			streamState.block(key, "thinking")
			if text := rawStringValue(contentBlock["thinking"]); text != "" {
				out = append(out, streamState.chunk(map[string]any{"reasoning_content": text}, nil, nil))
			}
		case "tool_use", "server_tool_use":
			block := streamState.block(key, "tool_use")
			streamState.HasToolCalls = true
			out = append(out, streamState.chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": block.ToolIndex,
				"id":    firstString(contentBlock, "id", "tool_use_id"),
				"type":  "function",
				"function": map[string]any{
					"name":      stringValue(contentBlock["name"]),
					"arguments": "",
				},
			}}}, nil, nil))
		}
	case "content_block_delta":
		out = append(out, streamState.start()...)
		delta := objectValue(payload["delta"])
		key := numberKey(payload["index"])
		switch stringValue(delta["type"]) {
		case "text_delta":
			if text := rawStringValue(delta["text"]); text != "" {
				out = append(out, streamState.chunk(map[string]any{"content": text}, nil, nil))
			}
		case "thinking_delta":
			if text := rawStringValue(delta["thinking"]); text != "" {
				out = append(out, streamState.chunk(map[string]any{"reasoning_content": text}, nil, nil))
			}
		case "input_json_delta":
			partial := rawStringValue(delta["partial_json"])
			if partial == "" {
				break
			}
			block := streamState.block(key, "tool_use")
			streamState.HasToolCalls = true
			out = append(out, streamState.chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index":    block.ToolIndex,
				"function": map[string]any{"arguments": partial},
			}}}, nil, nil))
		}
	case "message_delta":
		out = append(out, streamState.start()...)
		if outputTokens, okUsage := objectValue(payload["usage"])["output_tokens"]; okUsage {
			streamState.OutputTokens = outputTokens
		}
		out = append(out, streamState.finish(stringValue(objectValue(payload["delta"])["stop_reason"]))...)
	case "message_stop":
		out = append(out, streamState.start()...)
		out = append(out, streamState.finish("")...)
	}
	return out, nil
}

func (s *claudeOpenAIStreamState) start() [][]byte {
	if s.Started {
		return nil
	}
	s.Started = true
	return [][]byte{s.chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil)}
}

func (s *claudeOpenAIStreamState) block(key, kind string) *claudeOpenAIBlock {
	if key == "" {
		key = fmt.Sprintf("%s:%d", kind, len(s.Blocks))
	}
	if existing := s.Blocks[key]; existing != nil {
		return existing
	}
	created := &claudeOpenAIBlock{Kind: kind}
	if kind == "tool_use" {
		created.ToolIndex = s.NextToolIndex
		s.NextToolIndex++
	}
	s.Blocks[key] = created
	return created
}

func (s *claudeOpenAIStreamState) finish(stopReason string) [][]byte {
	if s.Stopped {
		return nil
	}
	s.Stopped = true
	usage := map[string]any{
		"prompt_tokens":     numberValue(s.InputTokens),
		"completion_tokens": numberValue(s.OutputTokens),
		"total_tokens":      int64Value(s.InputTokens) + int64Value(s.OutputTokens),
	}
	return [][]byte{s.chunk(map[string]any{}, claudeFinishReason(stopReason, s.HasToolCalls), usage)}
}

func (s *claudeOpenAIStreamState) chunk(delta map[string]any, finishReason any, usage map[string]any) []byte {
	return openAIChunk(s.ID, s.Model, 0, delta, finishReason, usage)
}
