# Validate the client formats

These checks confirm that all three client protocols return a body, in both
streaming and non-streaming mode, against both Copilot model classes. An empty
`200 OK` is the failure this matrix is designed to catch.

Set the endpoint and an existing CLIProxyAPI client key first:

```bash
BASE=http://127.0.0.1:8317
API_KEY=...
```

Pick one model of each class. The classes behave differently upstream, which is
why both are needed:

```bash
CHAT_MODEL=gpt-4.1          # exposes Copilot's /chat/completions endpoint
RESPONSES_MODEL=gpt-5.6-sol # always routed to Copilot's /responses endpoint
```

Confirm both are discoverable:

```bash
curl -fsS -H "Authorization: Bearer $API_KEY" "$BASE/v1/models" \
  | grep -Eo "\"id\":\"(${CHAT_MODEL}|${RESPONSES_MODEL})\""
```

## OpenAI Chat Completions

This is the protocol GitKraken, GitLens and most OpenAI-compatible clients
speak, and the one that previously returned nothing.

Non-streaming, for each model:

```bash
for MODEL in "$CHAT_MODEL" "$RESPONSES_MODEL"; do
  echo "== $MODEL"
  curl -fsS "$BASE/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with ok.\"}]}"
  echo
done
```

`choices[0].message.content` must be non-empty. A response whose `content` is
`""` and whose `id`, `model` and `created` are empty or zero is the empty
skeleton produced by a failed format negotiation.

Streaming, for each model:

```bash
for MODEL in "$CHAT_MODEL" "$RESPONSES_MODEL"; do
  echo "== $MODEL"
  curl -fsS -N "$BASE/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"Count to three.\"}]}"
  echo
done
```

Every line must be a single `data: {...}` frame carrying
`choices[0].delta.content`, the last frame before the sentinel must carry a
`finish_reason`, and the stream must end with `data: [DONE]`. A frame reading
`data: data: {...}` means the SSE envelope was forwarded twice.

Tool calling exercises the code path most likely to differ between the two
model classes:

```bash
curl -fsS "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$RESPONSES_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"What is the weather in Paris? Use the tool.\"}],\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"description\":\"Current weather for a city\",\"parameters\":{\"type\":\"object\",\"properties\":{\"city\":{\"type\":\"string\"}},\"required\":[\"city\"]}}}]}"
```

`choices[0].message.tool_calls[0].function.arguments` must be parseable JSON and
`finish_reason` must be `tool_calls`.

## OpenAI Responses

```bash
for MODEL in "$CHAT_MODEL" "$RESPONSES_MODEL"; do
  echo "== $MODEL"
  curl -fsS "$BASE/v1/responses" \
    -H "Authorization: Bearer $API_KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"input\":\"Reply with ok.\"}"
  echo
done
```

Repeat with `"stream":true` and confirm the `response.created`,
`response.output_text.delta` and `response.completed` events all arrive.

## Claude Messages

```bash
for MODEL in "$CHAT_MODEL" "$RESPONSES_MODEL"; do
  echo "== $MODEL"
  curl -fsS "$BASE/v1/messages" \
    -H "Authorization: Bearer $API_KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"max_tokens\":32,\"messages\":[{\"role\":\"user\",\"content\":\"Reply with ok.\"}]}"
  echo
done
```

`content[0].text` must be non-empty. Repeat with `"stream":true` and confirm the
`message_start`, `content_block_delta` and `message_stop` events arrive.

## GitKraken

GitKraken and GitLens only offer OpenAI-shaped custom endpoints, so point them
at the Chat Completions route:

- provider: OpenAI Compatible
- URL: `https://your-host/v1/chat/completions`
- key: a CLIProxyAPI client key
- model: any Copilot model listed by `/v1/models`

Then run any AI action, such as generating a commit message from a staged
change. The result must contain generated text rather than an empty suggestion.

## If a response is still empty

Enable `debug: true` in `config.yaml` temporarily and reproduce the request.
The log records the negotiated client format and the Copilot endpoint chosen for
the model, which identifies whether the failure is in format negotiation or
upstream.

Debug logs contain full prompts and completions. Turn `debug` back off and
delete the collected logs when finished.
