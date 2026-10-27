// Package scenario loads the versioned request scenarios shipped in
// scenarios/*.yaml into a typed Scenario.
//
// Load(name) reads an embedded scenario such as "standard-v1"; Parse reads
// bytes; Names lists what ships. A Scenario is validated on load and its
// padding is generated once, so every probe sends byte-identical content.
//
// Padding is an inventory of fictional services appended to the system prompt
// (after a blank line) so turn 1 reaches padding.target_input_tokens. Its
// algorithm is part of the scenario contract and must never change for an
// existing version:
//
//   - a fixed header line, then one row per line:
//     "<service>-<NNN> | <region> | <status> | p50 <n> ms | p99 <n> ms | <n> replicas"
//   - row fields come from a byte stream: SHA-256(seed ‖ uint64BE(0)), then
//     SHA-256(seed ‖ uint64BE(1)), and so on; NNN is the row number
//   - rows are added while the padding stays within its budget of
//     target_input_tokens − ceil(bytes(system + tools JSON + turn 1) / 4),
//     the padding itself counted at 4 bytes per token
//
// The estimate only sizes the padding; real token counts come from each
// provider's response. Render returns the canonical JSON rendering that
// adapters translate to each wire format; a golden test pins it byte for byte.
//
// Errors wrap ErrNotFound or ErrInvalid.
package scenario
