# Design

Applies to everything a human sees: terminal output, the local dashboard, piplon.sh, README screenshots, social images.

## Principle

**Measured, calm, specific.** piplon is a measuring instrument. It shows numbers, states and causes. It never shouts, never animates for decoration, never uses a glow, a gradient, a neon arc, or a "mission control" aesthetic. If a screen looks like a product landing page, it is wrong.

## Type

- UI text: **IBM Plex Sans** 400 / 500 / 600
- Numbers, code, timestamps, states: **IBM Plex Mono** 400 / 500
- Sizes: 11 (labels, uppercase, 0.04em tracking), 12 (meta), 13 (body), 14 (table), 20-26 (headlines, 600, -0.02em)

## Colour

Light: bg `#FAFAF9`, card `#FFFFFF`, border `#E8E8E5` / `#EFEFEC`, ink `#1A1A1A`, sub `#4A4A46`, muted `#8A8A85`, code bg `#F3F3F0`.
Dark: bg `#0B0B0C`, card `#131315`, border `#232326` / `#1C1C1F`, ink `#F2F2F0`, sub `#B8B8B4`, muted `#7A7A76`, code bg `#1A1A1D`.

States, both themes: ok `#2F9E5B` / `#3DBB73`, slow `#D98B0F` / `#E0A030`, errors/down `#D9480F` / `#F0603A`, accent (links hover, "live") `#E8541E`.

One accent. Colour carries state only; never decoration. Every state has a text label next to its dot (colour-blind safe).

## Layout

- Max content width 1184 px, 48 px side gutters, 20 px gaps, 10 px card radius, 1 px borders, no shadows.
- Pages are **cards in a single column**; two-column only for equal-weight pairs (ranking | join).
- Tables use fixed first columns and a `1fr` middle; 90-day uptime bars are 6 × 22 px with 2 px gaps.
- Tooltips are dark cards (`ink` bg, 8 px radius) with a bold first line and mono detail lines.
- Incidents are a full-width table, never a list of chips.

## Status page structure (piplon.sh and `piplon serve`)

1. Top bar: mark + wordmark, nav (Status · Providers · Cache · Deprecations · Network · Docs), mono clock "14:31 UTC · updated 12 s ago", one CTA "Run a probe".
2. Status banner (only when something is wrong): "Anthropic is degraded in Asia-Pacific" + one-sentence measurement + "since 14:05 UTC". Otherwise: "All providers operating normally" in a quiet card.
3. Map card: dot-matrix world map, probes as dots (official = larger, community = 60 % opacity), state colour, labels with the key number (TTFT or error %). Legend in the card footer.
4. Provider table: provider · status · 90 days · TTFT p50 · errors · cache · next sunset.
5. Incidents (7 d) table: when · provider · region · what we measured · duration · state.
6. Two cards: Reliability ranking (week N, share of successful probes, delta vs last week) | "Put your city on the map" inverted card with the install one-liner and three numbers.

## Terminal

- `lipgloss` only. Same state colours. No spinners that spin forever; progress shows a count.
- The `run` table is the hero. Columns: target · state · TTFT p50 · err 1h · cache · last · note (←). It must be readable in an 80-column terminal by hiding `note` first, then `last`.
- Errors: one line, lower-case, what failed and what to do: `target anthropic-direct: key ANTHROPIC_API_KEY is not set`.

## Logo

`assets/piplon-mark.svg` (currentColor). Minimum size 16 px. Never recolour outside ink/white. Never add a background shape. App icon variants in `assets/`.

## Screenshots and GIFs

- Recorded with VHS (`docs/tapes/*.tape`), Ghostty-like theme (Catppuccin Mocha), JetBrains Mono 14, 100 × 28 cells, 1.6 s initial pause, 2 s final hold.
- No real keys, no client names, no history. Use the `demo/` config with `PIPLON_DEMO=1` which replays fixtures.
