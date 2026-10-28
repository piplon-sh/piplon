# OpenAI-compatible fixtures

These files are **synthetic**: written by hand from the documented Chat
Completions streaming format, not recorded from a live API. They contain no
keys, organisation ids or real completion ids. Real answers live
next to them in `recorded/`, see below.

| File | What it stands for |
|---|---|
| `turn1.sse` | turn 1 answers READY in two chunks, no cached tokens |
| `turn2.sse` | turn 2 answers STILL, 1 920 cached prompt tokens |
| `turn3.sse` | turn 3 answers DONE, 1 920 cached prompt tokens |
| `wrong_answer.sse` | a complete stream whose text fails the turn's expectation |
| `stream_error.sse` | an `error` object sent as a stream chunk |
| `tool_call.sse` | the model calls a tool instead of answering in text |
| `content_filter.sse` | the answer stops with `finish_reason: content_filter`, no category |
| `content_filter_categories.sse` | the same after partial text, with Azure-style `content_filter_results` |
| `stream_cut.sse` | a stream that ends without `data: [DONE]` |
| `rate_limited_429.json` | the error body returned with HTTP 429 |
| `request_turn1.golden.json` | the exact request body of turn 1 |
| `request_turn3.golden.json` | the exact request body of turn 3, with history |

Regenerate the two golden files with `go test ./internal/probe -run Golden -update`
only when the request format is meant to change.

## Recorded answers

`recorded/turnN.sse` are real streamed answers to `standard-v1`, saved by a
development tool that is not part of the piplon binary. Response headers are
never saved, provider ids are replaced with `…scrubbed`, and the tool writes
nothing if a turn fails or a body contains the key. One recording costs one
real probe, a few cents:

```sh
OPENAI_API_KEY=... go run ./tools/record --format openai --model gpt-4o-mini
```

The replay test (`go test ./internal/probe -run Recorded`) is skipped until
recordings exist. It checks what any real success must show and logs the
numbers rather than pinning them.
