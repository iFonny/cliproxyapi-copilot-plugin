package translate

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestClaudeRequestToResponsesPreservesCoreContent(t *testing.T) {
	t.Parallel()

	claudeRequest := []byte(`{
		"model":"ignored",
		"max_tokens":321,
		"system":"Be concise.",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"describe this"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YWJj"}}
			]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"inspect first","signature":"sig"},
				{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"x"}}
			]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"found"}]}
		],
		"tools":[{"name":"lookup","description":"Look up data","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]
	}`)

	out, err := RequestForEndpointFrom("claude", "gpt-5.6-sol", claudeRequest, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate Claude request: %v", err)
	}
	data := gjson.ParseBytes(out)
	if got := data.Get("model").String(); got != "gpt-5.6-sol" {
		t.Fatalf("model = %q", got)
	}
	if got := data.Get("max_output_tokens").Int(); got != 321 {
		t.Fatalf("max_output_tokens = %d", got)
	}
	text := string(out)
	for _, needle := range []string{
		"Be concise.",
		"describe this",
		"data:image/png;base64,YWJj",
		"inspect first",
		`"type":"function_call"`,
		`"call_id":"call_1"`,
		`"type":"function_call_output"`,
		`"name":"lookup"`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("translated request omits %q: %s", needle, out)
		}
	}
}

func TestClaudeStructuredOutputAddsResponsesSchemaName(t *testing.T) {
	t.Parallel()

	original := []byte(`{
		"model":"gpt-5.6-terra",
		"messages":[{"role":"user","content":"Create a title."}],
		"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}
	}`)
	out, err := RequestForEndpointFrom("claude", "gpt-5.6-terra", original, true, EndpointResponses)
	if err != nil {
		t.Fatalf("translate request: %v", err)
	}
	if got := gjson.GetBytes(out, "text.format.name").String(); got != "claude_structured_output" {
		t.Fatalf("text.format.name = %q; request=%s", got, out)
	}
}

func TestChatNonStreamResponseToResponses(t *testing.T) {
	t.Parallel()

	original := []byte(`{"model":"gpt-test","input":[{"role":"user","content":[{"type":"input_text","text":"ping"}]}]}`)
	translated, err := RequestForEndpoint("gpt-test", original, false, EndpointChatCompletions)
	if err != nil {
		t.Fatalf("translate request: %v", err)
	}
	upstream := []byte(`{
		"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"gpt-test",
		"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
	}`)
	out, err := ResponseToResponses(context.Background(), EndpointChatCompletions, "gpt-test", original, translated, upstream)
	if err != nil {
		t.Fatalf("translate response: %v", err)
	}
	data := gjson.ParseBytes(out)
	if got := data.Get("status").String(); got != "completed" {
		t.Fatalf("status = %q; response=%s", got, out)
	}
	if got := data.Get("output.0.content.0.text").String(); got != "pong" {
		t.Fatalf("output text = %q; response=%s", got, out)
	}
	if got := data.Get("usage.total_tokens").Int(); got != 3 {
		t.Fatalf("total tokens = %d; response=%s", got, out)
	}
}

func TestResponsesNonStreamResponseToClaude(t *testing.T) {
	t.Parallel()

	original := []byte(`{"model":"gpt-5.6-sol","max_tokens":20,"messages":[{"role":"user","content":"ping"}]}`)
	translated, err := RequestForEndpointFrom("claude", "gpt-5.6-sol", original, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate request: %v", err)
	}
	upstream := []byte(`{
		"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"gpt-5.6-sol",
		"output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"pong","annotations":[]}]}],
		"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
	}`)
	out, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "gpt-5.6-sol", original, translated, upstream)
	if err != nil {
		t.Fatalf("translate response: %v", err)
	}
	data := gjson.ParseBytes(out)
	if got := data.Get("type").String(); got != "message" {
		t.Fatalf("Claude response type = %q; response=%s", got, out)
	}
	if got := data.Get("content.0.text").String(); got != "pong" {
		t.Fatalf("Claude response text = %q; response=%s", got, out)
	}
	if got := data.Get("usage.input_tokens").Int(); got != 2 {
		t.Fatalf("Claude input tokens = %d; response=%s", got, out)
	}
}

func TestChatSSEToResponses(t *testing.T) {
	t.Parallel()

	original := []byte(`{"model":"gpt-test","input":[{"role":"user","content":[{"type":"input_text","text":"ping"}]}]}`)
	translated, err := RequestForEndpoint("gpt-test", original, true, EndpointChatCompletions)
	if err != nil {
		t.Fatalf("translate request: %v", err)
	}
	chunks := [][]byte{
		[]byte(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"po"},"finish_reason":null}]}`),
		[]byte(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"ng"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`),
		[]byte(`data: [DONE]`),
	}
	var state any
	var output strings.Builder
	for _, chunk := range chunks {
		frames, errTranslate := StreamToResponses(context.Background(), EndpointChatCompletions, "gpt-test", original, translated, chunk, &state)
		if errTranslate != nil {
			t.Fatalf("translate SSE: %v", errTranslate)
		}
		for _, frame := range frames {
			output.Write(frame)
		}
	}
	text := output.String()
	for _, needle := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		`"delta":"po"`,
		`"delta":"ng"`,
		"event: response.completed",
		`"total_tokens":3`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("translated SSE omits %q:\n%s", needle, text)
		}
	}
}

func TestResponsesSSEToClaude(t *testing.T) {
	t.Parallel()

	original := []byte(`{"model":"gpt-5.6-sol","max_tokens":20,"messages":[{"role":"user","content":"ping"}]}`)
	translated, err := RequestForEndpointFrom("claude", "gpt-5.6-sol", original, true, EndpointResponses)
	if err != nil {
		t.Fatalf("translate request: %v", err)
	}
	chunks := [][]byte{
		[]byte(`event: response.created
data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":2,"output_tokens":0}}}

`),
		[]byte(`event: response.content_part.added
data: {"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}

`),
		[]byte(`event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"one"}

`),
		[]byte(`event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":" two"}

`),
		[]byte(`event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":" three"}

`),
		[]byte(`event: response.content_part.done
data: {"type":"response.content_part.done","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"one two three"}}

`),
		[]byte(`event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"gpt-5.6-sol","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"one two three"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}

`),
	}
	var state any
	var output strings.Builder
	for _, chunk := range chunks {
		frames, errTranslate := StreamFromEndpoint(context.Background(), EndpointResponses, "claude", "gpt-5.6-sol", original, translated, chunk, &state)
		if errTranslate != nil {
			t.Fatalf("translate SSE: %v", errTranslate)
		}
		for _, frame := range frames {
			output.Write(frame)
		}
	}
	text := output.String()
	for _, needle := range []string{
		"event: message_start",
		"event: content_block_start",
		`"type":"text"`,
		`"text":"one"`,
		`"text":" two"`,
		`"text":" three"`,
		`"stop_reason":"end_turn"`,
		`"output_tokens":3`,
		"event: message_stop",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("Claude SSE omits %q:\n%s", needle, text)
		}
	}
}

func TestOpenAIRequestToResponsesPreservesCoreContent(t *testing.T) {
	t.Parallel()

	chatRequest := []byte(`{
		"model":"ignored",
		"max_tokens":321,
		"messages":[
			{"role":"system","content":"Be concise."},
			{"role":"user","content":"describe this"},
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"found"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","description":"Look up data","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}]
	}`)

	out, err := RequestForEndpointFrom("openai", "gpt-5.6-sol", chatRequest, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate Chat Completions request: %v", err)
	}
	data := gjson.ParseBytes(out)
	if got := data.Get("model").String(); got != "gpt-5.6-sol" {
		t.Fatalf("model = %q", got)
	}
	if got := data.Get("max_output_tokens").Int(); got != 321 {
		t.Fatalf("max_output_tokens = %d; request=%s", got, out)
	}
	if got := data.Get("instructions").String(); got != "Be concise." {
		t.Fatalf("instructions = %q; request=%s", got, out)
	}
	text := string(out)
	for _, needle := range []string{
		"describe this",
		`"type":"function_call"`,
		`"call_id":"call_1"`,
		`"type":"function_call_output"`,
		`"name":"lookup"`,
		"found",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("translated request omits %q: %s", needle, out)
		}
	}
	// The Claude hop synthesizes an Anthropic user fingerprint that Copilot has
	// no use for.
	if data.Get("metadata").Exists() {
		t.Fatalf("translated request leaks intermediate metadata: %s", out)
	}
}

func TestOpenAIRequestToResponsesOnlyLimitsTokensWhenAsked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request string
		want    int64
	}{
		{name: "no ceiling", request: `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, want: 0},
		{name: "max_tokens", request: `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`, want: 100},
		{name: "max_completion_tokens", request: `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":200}`, want: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out, err := RequestForEndpointFrom("openai", "m", []byte(test.request), false, EndpointResponses)
			if err != nil {
				t.Fatalf("translate request: %v", err)
			}
			ceiling := gjson.GetBytes(out, "max_output_tokens")
			if test.want == 0 {
				if ceiling.Exists() {
					t.Fatalf("unrequested max_output_tokens = %s; request=%s", ceiling.Raw, out)
				}
				return
			}
			if got := ceiling.Int(); got != test.want {
				t.Fatalf("max_output_tokens = %d, want %d; request=%s", got, test.want, out)
			}
		})
	}
}

func TestResponsesNonStreamResponseToOpenAI(t *testing.T) {
	t.Parallel()

	original := []byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"ping"}]}`)
	translated, err := RequestForEndpointFrom("openai", "gpt-5.6-sol", original, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate request: %v", err)
	}
	upstream := []byte(`{
		"id":"resp_1","object":"response","created_at":17,"status":"completed","model":"gpt-5.6-sol",
		"output":[
			{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"weighing"}]},
			{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"pong"}]},
			{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"}
		],
		"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3,"input_tokens_details":{"cached_tokens":1}}
	}`)
	out, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "openai", "gpt-5.6-sol", original, translated, upstream)
	if err != nil {
		t.Fatalf("translate response: %v", err)
	}
	data := gjson.ParseBytes(out)
	if got := data.Get("object").String(); got != "chat.completion" {
		t.Fatalf("object = %q; response=%s", got, out)
	}
	if got := data.Get("created").Int(); got != 17 {
		t.Fatalf("created = %d; response=%s", got, out)
	}
	if got := data.Get("choices.0.message.content").String(); got != "pong" {
		t.Fatalf("content = %q; response=%s", got, out)
	}
	if got := data.Get("choices.0.message.reasoning_content").String(); got != "weighing" {
		t.Fatalf("reasoning_content = %q; response=%s", got, out)
	}
	if got := data.Get("choices.0.message.tool_calls.0.function.arguments").String(); got != `{"q":"x"}` {
		t.Fatalf("tool arguments = %q; response=%s", got, out)
	}
	if got := data.Get("choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q; response=%s", got, out)
	}
	if got := data.Get("usage.prompt_tokens").Int(); got != 2 {
		t.Fatalf("prompt_tokens = %d; response=%s", got, out)
	}
	if got := data.Get("usage.prompt_tokens_details.cached_tokens").Int(); got != 1 {
		t.Fatalf("cached_tokens = %d; response=%s", got, out)
	}
}

func TestResponsesTruncationBecomesLengthFinishReason(t *testing.T) {
	t.Parallel()

	upstream := []byte(`{
		"id":"resp_1","status":"incomplete","model":"m","incomplete_details":{"reason":"max_output_tokens"},
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)
	out, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "openai", "m", nil, nil, upstream)
	if err != nil {
		t.Fatalf("translate response: %v", err)
	}
	data := gjson.ParseBytes(out)
	if got := data.Get("choices.0.finish_reason").String(); got != "length" {
		t.Fatalf("finish_reason = %q; response=%s", got, out)
	}
	if got := data.Get("usage.total_tokens").Int(); got != 2 {
		t.Fatalf("total_tokens = %d; response=%s", got, out)
	}
}

func TestClaudeNonStreamResponseToOpenAI(t *testing.T) {
	t.Parallel()

	// The official Claude to Chat Completions transformer only reads SSE "data:"
	// lines, so it returns an empty skeleton for a Claude message body.
	upstream := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude-test",
		"content":[
			{"type":"thinking","thinking":"weighing"},
			{"type":"text","text":"pong"},
			{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"x"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":2,"output_tokens":1}
	}`)
	out, err := ResponseFromEndpoint(context.Background(), EndpointMessages, "openai", "claude-test", nil, nil, upstream)
	if err != nil {
		t.Fatalf("translate response: %v", err)
	}
	data := gjson.ParseBytes(out)
	if got := data.Get("choices.0.message.content").String(); got != "pong" {
		t.Fatalf("content = %q; response=%s", got, out)
	}
	if got := data.Get("choices.0.message.reasoning_content").String(); got != "weighing" {
		t.Fatalf("reasoning_content = %q; response=%s", got, out)
	}
	if got := data.Get("choices.0.message.tool_calls.0.function.arguments").String(); got != `{"q":"x"}` {
		t.Fatalf("tool arguments = %q; response=%s", got, out)
	}
	if got := data.Get("choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q; response=%s", got, out)
	}
	if got := data.Get("usage.total_tokens").Int(); got != 3 {
		t.Fatalf("total_tokens = %d; response=%s", got, out)
	}
}

func TestResponsesSSEToOpenAI(t *testing.T) {
	t.Parallel()

	original := []byte(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"ping"}]}`)
	translated, err := RequestForEndpointFrom("openai", "gpt-5.6-sol", original, true, EndpointResponses)
	if err != nil {
		t.Fatalf("translate request: %v", err)
	}
	chunks := [][]byte{
		[]byte(`event: response.created
data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"gpt-5.6-sol","created_at":17}}

`),
		[]byte(`event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"weighing"}

`),
		[]byte(`event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"one"}

`),
		[]byte(`event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":" two"}

`),
		[]byte(`event: response.output_item.added
data: {"type":"response.output_item.added","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup"}}

`),
		[]byte(`event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"{\"q\":\"x\"}"}

`),
		[]byte(`event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"gpt-5.6-sol","created_at":17,"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}

`),
	}
	var state any
	var frames [][]byte
	for _, chunk := range chunks {
		out, errTranslate := StreamFromEndpoint(context.Background(), EndpointResponses, "openai", "gpt-5.6-sol", original, translated, chunk, &state)
		if errTranslate != nil {
			t.Fatalf("translate SSE: %v", errTranslate)
		}
		frames = append(frames, out...)
	}
	if len(frames) == 0 {
		t.Fatal("no frames emitted")
	}
	// The host wraps every chunk in its own "data:" envelope and appends the
	// [DONE] sentinel itself, so each frame must be bare Chat Completions JSON.
	for _, frame := range frames {
		if !gjson.ValidBytes(frame) {
			t.Fatalf("frame is not bare JSON: %s", frame)
		}
		if got := gjson.GetBytes(frame, "object").String(); got != "chat.completion.chunk" {
			t.Fatalf("frame object = %q: %s", got, frame)
		}
	}
	if got := gjson.GetBytes(frames[0], "choices.0.delta.role").String(); got != "assistant" {
		t.Fatalf("first frame does not open the assistant message: %s", frames[0])
	}
	text := string(bytes.Join(frames, []byte("\n")))
	for _, needle := range []string{
		`"reasoning_content":"weighing"`,
		`"content":"one"`,
		`"content":" two"`,
		`"id":"call_1"`,
		`"name":"lookup"`,
		`"arguments":"{\"q\":\"x\"}"`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("translated SSE omits %q:\n%s", needle, text)
		}
	}
	last := frames[len(frames)-1]
	if got := gjson.GetBytes(last, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("final finish_reason = %q: %s", got, last)
	}
	if got := gjson.GetBytes(last, "usage.total_tokens").Int(); got != 5 {
		t.Fatalf("final total_tokens = %d: %s", got, last)
	}
}

func TestChatCompletionsPassthroughUnwrapsSSEEnvelope(t *testing.T) {
	t.Parallel()

	var state any
	chunk := []byte("data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
	frames, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, "openai", "m", nil, nil, chunk, &state)
	if err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if got := gjson.GetBytes(frames[0], "choices.0.delta.content").String(); got != "hi" {
		t.Fatalf("content = %q: %s", got, frames[0])
	}
	if strings.HasPrefix(string(frames[0]), "data:") {
		t.Fatalf("frame keeps the upstream SSE envelope: %s", frames[0])
	}

	done, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, "openai", "m", nil, nil, []byte("data: [DONE]\n\n"), &state)
	if err != nil {
		t.Fatalf("passthrough sentinel: %v", err)
	}
	if len(done) != 0 {
		t.Fatalf("sentinel forwarded %d frames; the host appends its own", len(done))
	}
}
