# Roadmap

Weeks are rough. This is a direction, not a promise.

Rule: ship something runnable every two weeks. An item is done when it is merged, tested, documented, and demoable in ten seconds.

## v0.1: the probe (weeks 1-4)

Goal: a stranger installs piplon, points it at two providers and a gateway, and within two minutes sees a table, a Prometheus endpoint, and gets a Slack message when a key is wrong.

### Week 1: foundations
- [ ] Repo hygiene: CI, lint, release pipeline, templates (this kit)
- [ ] `cmd/piplon` with cobra: `version`, `run` (stub), `scenario show`
- [ ] `internal/config`: load `piplon.yaml`, `${VAR}` expansion, validation with precise errors
- [ ] `internal/scenario`: parse `scenarios/standard-v1.yaml`, typed struct, golden test
- [ ] `internal/store`: SQLite open/migrate, `samples` table, insert + query, retention job

### Week 2: the probe
- [ ] `internal/probe`: Probe interface, scheduler with jitter and concurrency limit, budget estimator
- [ ] Adapters: `anthropic` (Messages API, streaming), `openai` (chat completions, streaming; covers OpenAI, OpenRouter, Groq, Together, LiteLLM, vLLM, Ollama-OpenAI)
- [ ] Measurements: TTFT (first content delta), tokens/s, total, status, provider error code, stream cut detection, cache fields on turns 2-3
- [ ] Replay tests from recorded fixtures, no network in tests
- [ ] `piplon run` prints a live table (lipgloss)

### Week 3: cache, analysis, alerts
- [ ] `compare_with`: direct vs gateway diff on cache and TTFT
- [ ] `internal/analyze`: baseline per target × hour × weekday (needs 24 h; hard failures alert immediately), states `ok | slow | errors | down | cache_miss | gone`, incident open/close with hysteresis (3 samples), probable-cause strings
- [ ] `internal/notify`: Slack + generic webhook first, then Discord, email, PagerDuty. Dedupe per incident
- [ ] Adapters: `bedrock` (SigV4, converse-stream), `google` (Gemini generateContent stream)

### Week 4: v0.1 release
- [ ] `piplon serve`: `/metrics`, `/healthz`, `/readyz`, `/api/v1/targets`, `/api/v1/incidents`, minimal HTML dashboard (server-rendered, htmx)
- [ ] `piplon check`: one-shot, thresholds, exit codes, JSON output: the CI gate
- [ ] Docs: README final, `docs/CONFIG.md`, `docs/OUTPUTS.md`, `docs/RUNNING.md` (laptop, systemd, Docker, Kubernetes Deployment + ServiceMonitor)
- [ ] Release: tag `v0.1.0`, goreleaser, Homebrew tap `piplon-sh/homebrew-tap`, cosign, SBOM, container image on GHCR

## v0.2: the map (weeks 5-8)

Goal: piplon.sh shows a world map with 3 official probes and the first community probes.

- [ ] `piplon agent --join`: outbound-only push of hourly aggregates (no keys, no prompts, no raw samples), token enrolment, region label
- [ ] Central: Postgres (Timescale later), ingest API, dedupe, abuse limits
- [ ] piplon.sh: map, provider table with 90-day bars, incidents, weekly reliability ranking, open dataset (CSV + JSON, CC-BY-4.0)
- [ ] 3 official probes: Frankfurt, Ashburn, Singapore (≈5 €/month VPS each)
- [ ] Sponsors button, Open Collective, "sponsor a probe city"

## v0.3: depth (weeks 9-13)

- [ ] Deprecation watch: model list diffing per provider, "sunset in N days" in the table
- [ ] More scenarios: `long-context-v1`, `tools-heavy-v1`, `vision-v1`; scenario versioning rules documented
- [ ] OTLP export, Grafana dashboard JSON in `contrib/`
- [ ] Helm chart in `piplon-sh/charts`
- [ ] Hosted option design doc (team alerts, retention, SSO): the first thing that could be paid; code stays Apache-2.0

## Explicitly not planned

- A gateway, a router, a cache, a proxy of any kind
- Model quality evaluation
- A JavaScript SPA dashboard
- Windows as a first-class target before v0.3 (binaries are built; not tested)
