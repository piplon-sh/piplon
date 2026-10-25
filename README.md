<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/piplon-mark-light-256.png">
    <img src="assets/piplon-mark-dark-256.png" width="72" alt="">
  </picture>
</p>

<h1 align="center">piplon</h1>

<p align="center">
  Independent blackbox probe for LLM provider APIs.<br>
  <em>It measures what your users actually get, not what the status page says.</em>
</p>

<p align="center">
  <a href="https://github.com/piplon-sh/piplon/actions/workflows/ci.yml"><img src="https://github.com/piplon-sh/piplon/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/piplon-sh/piplon/releases"><img src="https://img.shields.io/github/v/release/piplon-sh/piplon?include_prereleases" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License"></a>
</p>

---

> **Status: pre-alpha.** Nothing is released yet. Watch the repo or follow [@piplon_sh](https://x.com/piplon_sh) for the first release.

## The problem

You run production on LLM APIs, often through a gateway. When "the AI is slow", you find out from users. The provider's status page is green. You cannot tell whether it is the provider, the region, the gateway, or your prompt cache silently missing and tripling your bill.

## What piplon does

Every minute, from wherever it runs, piplon sends the **same real request** (a 3-turn conversation with tools, streamed) to every provider, model, region and path you configure, and records what came back:

- **time to first token**, throughput, total latency
- **errors**: 429, 529, throttling, 5xx, timeouts
- **cut streams** and truncated answers
- **prompt-cache hits vs misses** on turns 2 and 3
- **model deprecations**: a model that answered yesterday and 404s today
- **direct vs gateway**: the same request down both paths, side by side

It learns a baseline per hour and weekday, opens and closes incidents, and tells you on Slack, Discord, PagerDuty or a webhook, with the probable cause.

```
$ piplon run

piplon 0.1.0 · standard-v1 · 3 targets · every 5 m · budget 5.00 USD/day

TARGET                 STATE       TTFT p50   ERR 1h   CACHE   LAST
anthropic-direct       ok          412 ms     0.0 %    hit     3 s ago
anthropic-via-gateway  cache_miss  455 ms     0.0 %    miss    3 s ago
bedrock-eu-west-1      errors      -          14 %     -       3 s ago

anthropic-via-gateway  gateway drops cache_control on turn 2, misses since 11:40
bedrock-eu-west-1      ThrottlingException on 14 % of probes since 14:05
```

## Install

```sh
brew install piplon-sh/tap/piplon        # macOS / Linux
# or
curl -fsSL https://piplon.sh/install | sh
# or
go install github.com/piplon-sh/piplon/cmd/piplon@latest
```

Container images and a Helm chart follow the first release.

## Quick start

```yaml
# piplon.yaml
scenario: standard-v1
interval: 5m           # default for every target; 10s to 1h, your call
budget:
  daily_usd: 5.00      # circuit breaker: probing stops when reached
targets:
  - name: anthropic-direct
    format: anthropic
    url: https://api.anthropic.com
    model: claude-haiku-4-5
    key: ${ANTHROPIC_API_KEY}
    price: {input: 1.00, output: 5.00, cache_read: 0.10, cache_write: 1.25}  # USD per Mtok
  - name: anthropic-via-gateway
    format: openai
    url: https://litellm.internal/v1
    model: claude-haiku-4-5
    key: ${LITELLM_KEY}
    compare_with: anthropic-direct
    price: {input: 1.00, output: 5.00, cache_read: 0.10, cache_write: 1.25}
  - name: bedrock-eu-west-1
    format: bedrock
    region: eu-west-1
    model: anthropic.claude-haiku-4-5
    interval: 15m        # per-target override: probe this one less often
    price: {input: 1.00, output: 5.00, cache_read: 0.10, cache_write: 1.25}
notify:
  slack: ${SLACK_WEBHOOK}
```

```sh
piplon run                 # probe forever, print a live table, store in ./piplon.db
piplon serve               # local dashboard + /metrics for Prometheus
piplon check --max-ttft 2s # one shot, exit 1 on failure. For CI and pre-deploy gates
piplon agent --join        # put your city on the public map (opt-in, aggregates only)
```

## What it costs

Probes are real requests, so they cost tokens. With `standard-v1` (about 2 000 input tokens per turn, 3 turns), one target costs roughly **0.30 to 26 USD per day**, depending on the model and the interval:

| Model | every 15 min | every 5 min (default) | every 60 s |
|---|---|---|---|
| Haiku 4.5 | 0.30 – 0.60 | 0.85 – 1.75 | 1.20 – 8.60 |
| Sonnet 4.5 | 0.85 – 1.75 | 2.60 – 5.20 | 3.50 – 26 |

USD per target per day, at list prices. The low end is with the prompt cache hit on turns 2 and 3 (and on turn 1 at 60 s, while the cache is still warm); the high end is without caching, for example through a gateway that drops cache markers.

To watch availability and latency, probe a Haiku-class model: it measures the same path for a fraction of the cost. Everything that drives cost is yours to set: the interval globally and per target, the scenario, the number of targets, and the budget cap.

`budget.daily_usd` is a circuit breaker, not a forecast. When the estimated daily spend reaches it, piplon stops probing and reports that as its own state rather than overspending. The default of 5.00 USD per day covers about three to five Haiku-class targets at the default interval; raise it to match your targets. Set `price` on each target, in USD per million tokens (for example `price: {input: 1.00, output: 5.00, cache_read: 0.10, cache_write: 1.25}`), so the estimate matches what you pay: without it, piplon assumes Sonnet-class prices.

## Outputs

piplon does not come with its own dashboard product. It feeds the tools you already run.

- **Prometheus**: `/metrics` endpoint, scrape it with Prometheus, VictoriaMetrics, Grafana Agent, Datadog, New Relic, anything that speaks the format
- **OpenTelemetry**: OTLP traces and metrics to any collector
- **JSON logs**: one line per probe, pipe them into Loki, Elastic, Splunk, CloudWatch, or `jq`
- **Webhook**: every state change as a JSON POST, so a custom system or a workflow engine can react
- **SQLite**: every sample is a row in a file you own; query it with `sqlite3`, DuckDB, a notebook, or your own script
- **Notifications**: Slack, Discord, email, PagerDuty, generic webhook
- **Terminal and local dashboard**: `run` and `serve`, when you want to look without wiring anything

If your stack is not in that list and it can read Prometheus, OTLP, JSON or a webhook, it is covered.

## Roadmap

See [docs/ROADMAP.md](docs/ROADMAP.md) and [docs/ENGINEERING.md](docs/ENGINEERING.md). Short version: v0.1 is the probe + store + alerts + CI check. v0.2 is the public map and community probes.

## Contributing

Issues and PRs welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first; it is short. Questions go in [Discussions](https://github.com/piplon-sh/piplon/discussions).

## License

Apache-2.0. See [LICENSE](LICENSE).
