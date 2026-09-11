package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// openAIRequestToResponses reaches the Responses payload in two hops because the
// official registry exposes no direct Chat Completions to Responses route.
func openAIRequestToResponses(model string, body []byte, stream bool) ([]byte, error) {
	intermediate := registry.TranslateRequest(sdktranslator.FormatOpenAI, sdktranslator.FormatClaude, model, body, stream)
	if len(intermediate) == 0 || !json.Valid(intermediate) {
		return nil, fmt.Errorf("official request translation from %s to %s failed", sdktranslator.FormatOpenAI, sdktranslator.FormatClaude)
	}
	root, err := decodeObject(intermediate)
	if err != nil {
		return nil, fmt.Errorf("decode intermediate Claude request: %w", err)
	}
	// The Chat Completions hop synthesizes an Anthropic user fingerprint and a
	// default token ceiling to satisfy the Claude schema. Copilot needs neither,
	// and the ceiling can exceed what a model accepts.
	delete(root, "metadata")
	if ceiling, ok := openAIOutputTokenCeiling(body); ok {
		root["max_tokens"] = ceiling
	} else {
		delete(root, "max_tokens")
	}
	cleaned, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode intermediate Claude request: %w", err)
	}
	return claudeRequestToResponses(model, cleaned, stream)
}

// openAIOutputTokenCeiling reports the ceiling the client actually asked for.
// The official Chat Completions hop only understands max_tokens, so a client
// sending the newer max_completion_tokens would otherwise get the hop default.
func openAIOutputTokenCeiling(body []byte) (any, bool) {
	root, err := decodeObject(body)
	if err != nil {
		return nil, false
	}
	for _, key := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		if value, ok := root[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func responsesResponseToOpenAI(model string, body []byte) ([]byte, error) {
	root, err := decodeObject(body)
	if err != nil {
		return nil, fmt.Errorf("decode Responses response: %w", err)
	}
	if errResponse := responsesFailure(root); errResponse != nil {
		return nil, errResponse
	}
	var text, reasoning, refusal strings.Builder
	toolCalls := make([]any, 0)
	for _, rawItem := range arrayValue(root["output"]) {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		switch stringValue(item["type"]) {
		case "message":
			for _, rawPart := range arrayValue(item["content"]) {
				part, okPart := rawPart.(map[string]any)
				if !okPart {
					continue
				}
				switch stringValue(part["type"]) {
				case "output_text", "text":
					text.WriteString(rawStringValue(part["text"]))
				case "refusal":
					refusal.WriteString(rawStringValue(part["refusal"]))
				}
			}
		case "reasoning":
			reasoning.WriteString(responsesReasoningText(item))
		case "function_call", "custom_tool_call":
			toolCalls = append(toolCalls, map[string]any{
				"id":   firstString(item, "call_id", "id"),
				"type": "function",
				"function": map[string]any{
					"name":      stringValue(item["name"]),
					"arguments": firstRawString(item, "arguments", "input"),
				},
			})
		}
	}

	message := map[string]any{"role": "assistant"}
	if text.Len() == 0 && len(toolCalls) > 0 {
		message["content"] = nil
	} else {
		message["content"] = text.String()
	}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if refusal.Len() > 0 {
		message["refusal"] = refusal.String()
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	out := map[string]any{
		"id":      firstNonEmptyString(stringValue(root["id"]), "chatcmpl-copilot"),
		"object":  "chat.completion",
		"created": numberValue(root["created_at"]),
		"model":   firstNonEmptyString(stringValue(root["model"]), model),
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": responsesFinishReason(root, len(toolCalls) > 0),
		}},
		"usage": responsesUsageToOpenAI(objectValue(root["usage"])),
	}
	return json.Marshal(out)
}

func claudeResponseToOpenAI(model string, body []byte) ([]byte, error) {
	root, err := decodeObject(body)
	if err != nil {
		return nil, fmt.Errorf("decode Claude response: %w", err)
	}
	var text, reasoning strings.Builder
	toolCalls := make([]any, 0)
	for _, rawBlock := range arrayValue(root["content"]) {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		switch stringValue(block["type"]) {
		case "text":
			text.WriteString(rawStringValue(block["text"]))
		case "thinking":
			reasoning.WriteString(rawStringValue(block["thinking"]))
		case "tool_use", "server_tool_use":
			arguments, errArguments := json.Marshal(objectValue(block["input"]))
			if errArguments != nil {
				return nil, fmt.Errorf("encode Claude tool input: %w", errArguments)
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   firstString(block, "id", "tool_use_id"),
				"type": "function",
				"function": map[string]any{
					"name":      stringValue(block["name"]),
					"arguments": string(arguments),
				},
			})
		}
	}

	message := map[string]any{"role": "assistant"}
	if text.Len() == 0 && len(toolCalls) > 0 {
		message["content"] = nil
	} else {
		message["content"] = text.String()
	}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	usage := objectValue(root["usage"])
	out := map[string]any{
		"id":      firstNonEmptyString(stringValue(root["id"]), "chatcmpl-copilot"),
		"object":  "chat.completion",
		"created": 0,
		"model":   firstNonEmptyString(stringValue(root["model"]), model),
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": claudeFinishReason(stringValue(root["stop_reason"]), len(toolCalls) > 0),
		}},
		"usage": map[string]any{
			"prompt_tokens":     numberValue(usage["input_tokens"]),
			"completion_tokens": numberValue(usage["output_tokens"]),
			"total_tokens":      int64Value(usage["input_tokens"]) + int64Value(usage["output_tokens"]),
		},
	}
	return json.Marshal(out)
}

// openAIChunk builds a bare Chat Completions stream chunk. The host wraps every
// chunk in its own "data:" envelope and appends the [DONE] sentinel itself, so
// frames must carry no SSE framing of their own.
func openAIChunk(id, model string, created any, delta map[string]any, finishReason any, usage map[string]any) []byte {
	payload := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
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

// openAIPassthroughFrame unwraps an upstream Chat Completions SSE frame because
// the host re-wraps every chunk in its own "data:" envelope.
func openAIPassthroughFrame(frame []byte) ([][]byte, error) {
	_, data, done, err := parseSSEFrame(frame)
	if err != nil {
		return nil, err
	}
	if done || len(data) == 0 {
		return nil, nil
	}
	return [][]byte{data}, nil
}

func responsesFinishReason(root map[string]any, hasToolCalls bool) string {
	switch strings.ToLower(stringValue(objectValue(root["incomplete_details"])["reason"])) {
	case "max_output_tokens", "max_tokens":
		return "length"
	case "content_filter":
		return "content_filter"
	}
	if hasToolCalls {
		return "tool_calls"
	}
	return "stop"
}

func claudeFinishReason(stopReason string, hasToolCalls bool) string {
	switch strings.ToLower(stopReason) {
	case "max_tokens":
		return "length"
	case "refusal":
		return "content_filter"
	case "tool_use":
		return "tool_calls"
	}
	if hasToolCalls {
		return "tool_calls"
	}
	return "stop"
}

func responsesUsageToOpenAI(usage map[string]any) map[string]any {
	out := map[string]any{
		"prompt_tokens":     numberValue(usage["input_tokens"]),
		"completion_tokens": numberValue(usage["output_tokens"]),
	}
	if total, ok := usage["total_tokens"]; ok {
		out["total_tokens"] = numberValue(total)
	} else {
		out["total_tokens"] = int64Value(usage["input_tokens"]) + int64Value(usage["output_tokens"])
	}
	if cached, ok := objectValue(usage["input_tokens_details"])["cached_tokens"]; ok {
		out["prompt_tokens_details"] = map[string]any{"cached_tokens": numberValue(cached)}
	}
	if reasoningTokens, ok := objectValue(usage["output_tokens_details"])["reasoning_tokens"]; ok {
		out["completion_tokens_details"] = map[string]any{"reasoning_tokens": numberValue(reasoningTokens)}
	}
	return out
}
