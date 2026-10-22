// Package scenario loads the versioned request scenarios shipped in
// scenarios/*.yaml into a typed Scenario.
//
// Load(name) reads an embedded scenario such as "standard-v1"; Parse reads
// bytes; Names lists what ships. A Scenario is validated on load and its
// padding is generated once, so every probe sends byte-identical content.
//
// Padding is a block of ordinary English words appended to the system prompt
// (after a blank line) so turn 1 reaches padding.target_input_tokens. Its
// algorithm is part of the scenario contract and must never change for an
// existing version:
//
//   - word i is words[b], where b is byte i%32 of SHA-256(seed ‖ uint64BE(i/32))
//   - words are joined by spaces, 12 per line, lines joined by "\n"
//   - word count = target_input_tokens − ceil(bytes(system + tools JSON + turn 1) / 4),
//     counting one token per padding word
//
// The estimate only sizes the padding; real token counts come from each
// provider's response. Render returns the canonical JSON rendering that
// adapters translate to each wire format; a golden test pins it byte for byte.
//
// Errors wrap ErrNotFound or ErrInvalid.
package scenario
