# Anthropic fixtures

These files are **synthetic**: written by hand from the documented Messages API
streaming format, not recorded from a live API. They contain no keys, account
ids or real message ids. Replacing them with scrubbed recordings is tracked in
the roadmap (replay tests from recorded fixtures).

| File | What it stands for |
|---|---|
| `turn1.sse` | turn 1 answers READY in two deltas, writes the prompt cache |
| `turn2.sse` | turn 2 answers STILL, reads the prompt cache |
| `turn3.sse` | turn 3 answers DONE, reads the prompt cache |
| `wrong_answer.sse` | a complete stream whose text fails the turn's expectation |
| `stream_error.sse` | an `error` event (overloaded) in the middle of a stream |
| `stream_cut.sse` | a stream that ends without `message_stop` |
| `overloaded_529.json` | the error body returned with HTTP 529 |
| `request_turn1.golden.json` | the exact request body of turn 1 |
| `request_turn3.golden.json` | the exact request body of turn 3, with history |

Regenerate the two golden files with `go test ./internal/probe -run Golden -update`
only when the request format is meant to change.
