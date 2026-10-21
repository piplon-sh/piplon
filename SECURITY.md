# Security policy

## Supported versions

Only the latest minor release receives security fixes.

## Reporting a vulnerability

Use GitHub's private reporting: **Security → Report a vulnerability** on this repository. Do not open a public issue.

You will get an acknowledgement within 72 hours and a fix or a mitigation plan within 14 days for confirmed issues. Credit is given in the release notes unless you ask otherwise.

## What piplon does with secrets

- API keys are read from environment variables or files named in `piplon.yaml`. They are held in memory for the duration of a request and are never written to the store, the logs, or any network destination other than the provider endpoint they belong to.
- `piplon agent --join` sends hourly aggregates only (counts, percentiles, states). Never keys, prompts, raw samples, or hostnames.
- Releases are built by GitHub Actions from a signed tag, signed with cosign (keyless, Sigstore), and ship an SBOM. Verify with:

```sh
cosign verify-blob --certificate-identity-regexp 'https://github.com/piplon-sh/piplon' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --signature checksums.txt.sig --certificate checksums.txt.pem checksums.txt
```
