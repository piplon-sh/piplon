# Groq fixtures

Real answers from Groq's OpenAI-compatible API, replayed through the openai
adapter. They are **recorded**, not synthetic, by a development tool that is
not part of the piplon binary: response headers are never saved, provider ids
(`chatcmpl-`, `fp_`, `req_`) are replaced with `…scrubbed`, and the tool writes
nothing if a body contains the key.

| File | What it stands for |
|---|---|
| `recorded/turnN.sse` | the three turns of `standard-v1`, streamed by `openai/gpt-oss-20b`, a reasoning model, with `reasoning_effort: low` and `max_output_tokens: 200`: reasoning deltas come before the answer |
| `model_not_found_404.json` | the error body for a model that does not exist: piplon judges it `gone` |
| `invalid_key_401.json` | the error body for a wrong key |

Groq's free plan is enough. With `GROQ_API_KEY` set:

```sh
go run ./tools/record --format groq --model openai/gpt-oss-20b --reasoning-effort low --max-output-tokens 200
go run ./tools/record --format groq --model piplon-no-such-model --save-error model_not_found_404.json
env PIPLON_FAKE_GROQ_KEY=gsk_piplonFakeKeyForRecording0000000000 \
  go run ./tools/record --format groq --model openai/gpt-oss-20b --key-env PIPLON_FAKE_GROQ_KEY --save-error invalid_key_401.json
```

The replay tests (`go test ./internal/probe -run Groq`) are skipped until the
files exist. They check what any real answer must show and log the numbers
rather than pinning them.

What Groq does not show, and where it is covered:

- **Prompt cache.** For `openai/gpt-oss-20b` Groq sends no
  `prompt_tokens_details` at all, so the recorded turns read no cache. The
  cache path is covered by the synthetic `../openai/turn2.sse` and
  `turn3.sse`.
- **Timing.** Only bodies are recorded, so a replay measures the local test
  server, not Groq. What the recording does pin is the order: four reasoning
  deltas, then the answer, which is where TTFT stops.
- **`content_filter`, rate limits, a cut stream, an error inside a stream, a
  tool call streamed in pieces.** Not provoked on purpose: covered by the
  synthetic fixtures in `../openai/`.
