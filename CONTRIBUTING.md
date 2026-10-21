# Contributing to piplon

Thanks for looking. piplon is small and opinionated. The fastest way to get a change in is to keep it small too.

## Before you start

- Read `docs/ENGINEERING.md` once: architecture, allowed dependencies, rules. It is short.
- For anything bigger than a bug fix, open an issue or a Discussion first and describe the problem you are solving, not the code you want to write.

## Setup

```sh
git clone https://github.com/piplon-sh/piplon.git
cd piplon
make check      # build, vet, lint, test -race, govulncheck
```

Go 1.24+. `make tools` installs `golangci-lint` and `govulncheck` if you do not have them.

## Making a change

1. Branch from `main`: `feat/<short>`, `fix/<short>`, `docs/<short>`, `chore/<short>`.
2. Write the test with the code. No test may hit the network; provider responses are fixtures in `internal/probe/testdata/`.
3. Commit with [Conventional Commits](https://www.conventionalcommits.org/): `fix(analyze): close incident after 3 ok samples`. Subject ≤ 72 chars, body says why.
4. `make check` must be green.
5. Open a PR and fill the template. One concern per PR, ideally under 400 lines of diff. Large PRs get asked to split.

PRs are squash-merged; the PR title becomes the commit message, so write it as a Conventional Commit too.

## Adding a provider adapter

Copy `internal/probe/openai/` as a starting point. An adapter needs: request builder (golden test), stream parser (replay test from a recorded fixture), timeout behaviour test, cache-field extraction, and a row in `docs/PROVIDERS.md`. Record fixtures with `piplon scenario record --target <name>` and scrub them: no keys, no account ids.

## What we will say no to

- Anything that puts piplon in the request path (proxy, router, cache).
- New dependencies without a one-line justification.
- Dashboards as single-page apps.
- Features that need telemetry to work.

## Reporting bugs

Use the bug template. Include `piplon version`, your config with keys removed, and the JSON log lines around the problem.

## Security

See `SECURITY.md`. Do not open public issues for vulnerabilities.

## Code of conduct

Be kind, be specific, assume good faith. Maintainers may remove comments or contributors who are not.
