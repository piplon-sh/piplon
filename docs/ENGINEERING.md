# Engineering

How piplon is built. Short on purpose. If something here and the code disagree, the code is wrong or this file is out of date: open an issue.

## Shape

```
cmd/piplon/            CLI (cobra): run, serve, check, agent, scenario, version
internal/scenario/     versioned request scenarios, YAML to typed struct
internal/probe/        one probe = one scenario against one target; provider adapters
internal/store/        SQLite, raw samples, hourly rollups, incidents, retention
internal/analyze/      baselines, state machine, incident open/close, probable cause
internal/notify/       Slack, Discord, webhook, email, PagerDuty
internal/serve/        local HTTP: dashboard, /metrics, /api/v1, /healthz, /readyz
scenarios/             shipped scenario files; a change in content is a new version
docs/                  this folder; decisions in docs/adr/
```

Data flow: scheduler, probe, sample, store, analyze, incident, notify. `serve` only reads the store. `agent` is the same loop plus an outbound-only push of hourly aggregates.

## Decisions that are settled

Change these with an ADR in `docs/adr/`, not in a PR comment.

- **Go, one static binary, no cgo.** Installable with brew, curl, a container image, or `go install`.
- **SQLite embedded** for a single node. The `store` interface is the only thing the rest of the code sees.
- **The scenario is data.** `scenarios/standard-v1.yaml` defines the conversation, tools and expected shape. Adapters translate it to each provider's wire format. The scenario version is stored with every sample.
- **Keys never leave the host.** Read from env or files, held in memory per request, never logged, never stored, never pushed.
- **Everything that costs is configurable.** Interval is global with a per-target override, scenario is selectable, concurrency and timeouts are explicit.
- **Budget cap on by default.** `budget.daily_usd` defaults to 5.00. Reaching it stops probing and is reported as its own state, not as an outage.
- **Baseline before alerting.** Nothing "slow" fires before 24 h of history for that target. Hard failures (5xx, timeouts, model gone) fire immediately.
- **TTFT is measured from request fully written to first content delta.** Not first byte.
- **No telemetry.** The only outbound data is what `agent --join` sends, and that is opt-in, aggregates only.
- **The dashboard is server-rendered.** HTML plus htmx. No SPA.

## Dependencies

Standard library first. Allowed without discussion: `cobra`, `modernc.org/sqlite`, `prometheus/client_golang`, `go.opentelemetry.io/otel`, `charmbracelet/lipgloss`, `charmbracelet/bubbletea`, `gopkg.in/yaml.v3`. Anything else needs one line of justification in the PR.

## Code

- Go 1.26+. `gofmt`, `go vet`, `golangci-lint` (`.golangci.yml`), `govulncheck`, `go test -race`. `make check` runs all of it.
- Errors are wrapped with context: `fmt.Errorf("probe %s: %w", target.Name, err)`. Sentinel errors live in each package's `errors.go`.
- Logging is `log/slog`. JSON by default, text on a TTY. Never a key, never a full prompt. Always the target name and scenario version.
- Context cancellation everywhere. `piplon run` exits cleanly within 5 s of SIGINT.
- Each `internal` package documents its public surface in `doc.go`.
- Small functions. A file past 400 lines is a sign to split.

## Tests

- No test touches the network. Provider responses are recorded fixtures under `internal/probe/testdata/<provider>/`, scrubbed of keys and account ids.
- Every adapter has: a golden test of the request it builds, a replay test of the stream it parses, a timeout test.
- Table-driven tests for config validation and the analyze state machine.

## Git

- Nothing is committed to `main` directly. Branch, PR, CI green, squash merge.
- Branch names: `feat/<short>`, `fix/<short>`, `docs/<short>`, `chore/<short>`.
- Commits and PR titles follow Conventional Commits: `feat(probe): add bedrock adapter`. Lower case, imperative, 72 chars max, no trailing period. Body says why. Scopes: `probe`, `scenario`, `store`, `analyze`, `notify`, `serve`, `cli`, `config`, `ci`, `docs`, `release`.
- One concern per PR, under 400 lines of diff when possible.
- Commits are signed. Tags are signed. Releases are built from tags by CI only.

## Releases

Semantic versioning. `git tag -s vX.Y.Z` on `main` triggers `.github/workflows/release.yml`: goreleaser builds darwin/linux/windows binaries, signs the checksums with cosign (keyless), attaches an SBOM, pushes the container image to GHCR and updates the Homebrew tap. The GitHub release is created as a draft; the changelog is generated from commit messages, which is why they matter.
