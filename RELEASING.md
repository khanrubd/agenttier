# Releasing AgentTier

The canonical release workflow. Follow this every time you cut a new version.

## Before you tag

Ask the user which bump to apply: **patch** / **minor** / **major**.
Never decide unilaterally.

| Bump | Example | When |
| --- | --- | --- |
| patch | v0.1.0 → v0.1.1 | Bug fixes, doc-only, internal refactors, dep bumps |
| minor | v0.1.1 → v0.2.0 | New features, new CRD fields, new endpoints, non-breaking behavior changes |
| major | v0.x.y → v1.0.0 | Breaking API / CRD / endpoint changes |

Release-candidate tags are `vX.Y.Z-rcN`. Final GA tags have no suffix.

## Pre-release checklist

Work through this top-to-bottom on a clean `main`. Everything must pass
before you push the tag.

### Code and tests

- [ ] `main` is green on CI (lint, test 1.29 / 1.30 / 1.31, build, python-sdk, docs, security)
- [ ] `make verify-codegen` — generated deepcopy + CRD YAML is current
- [ ] `make test` — unit tests pass locally (skip on macOS if the LC_UUID issue bites; CI covers it)
- [ ] `make lint` — golangci-lint clean
- [ ] `./hack/check-license-headers.sh` — every Go file has the Apache header
- [ ] `cd web-ui && npm ci && npm run lint && npm run build` — web UI builds and types check
- [ ] `cd python-sdk && pip install -e ".[dev]" && pytest && mypy src/agenttier/` — SDK smoke tests and type check pass

### Helm chart

- [ ] `helm lint helm/agenttier/`
- [ ] `helm template agenttier helm/agenttier/` renders without error

### Docs

- [ ] `cd docs && mkdocs build --strict` — no broken nav or missing pages
- [ ] New or changed features from this release have docs pages or README blurbs

### Versioning (all three must match)

- [ ] `helm/agenttier/Chart.yaml` — `version` and `appVersion` updated
- [ ] `python-sdk/pyproject.toml` — `version` updated
- [ ] (Go: version is set via ldflags in `make build`; no source change)

### Changelog and README

- [ ] `CHANGELOG.md` — `[Unreleased]` rolled into a new `[vX.Y.Z] - YYYY-MM-DD` section; empty `[Unreleased]` stays on top
- [ ] `README.md` — feature descriptions reflect what actually ships; install commands still valid
- [ ] `todo.md` — relevant tasks marked `[x]` or `[~]` with a note on what shipped

### Secrets and repo settings

- [ ] `PYPI_TOKEN` is set in the org-scoped Actions secrets (required for SDK publish; without it the step skips gracefully)
- [ ] Org Packages setting allows Public container visibility (one-time; done)
- [ ] GitHub Pages is enabled and pointed at `gh-pages` (one-time; done)

## Releasing

Tag:

```bash
git tag vX.Y.Z
git push --tags
```

Watch the `Release` workflow run. It produces:

- Multi-arch images at `ghcr.io/agenttier/{controller,router,web-ui,sandbox-general,sandbox-claude-code,sandbox-minimal}:vX.Y.Z` (and `:latest` on GA only)
- Cosign signatures + SPDX & CycloneDX SBOMs attached to every image
- Helm chart tarball + updated `index.yaml` at `https://agenttier.github.io/agenttier/charts/`
- MkDocs site at `https://agenttier.github.io/agenttier/`
- CLI binaries for linux/darwin/windows × amd64/arm64 attached to the GitHub Release
- Python wheel + sdist published to PyPI (if `PYPI_TOKEN` is set) or attached as workflow artifacts
- GitHub Release with a templated body (install snippets + auto-generated notes)

## Post-release verification

Anonymous checks a user would run:

```bash
docker pull ghcr.io/agenttier/controller:vX.Y.Z
helm repo update && helm search repo agenttier --versions
curl -sL https://agenttier.github.io/agenttier/ | grep -q AgentTier
pip install agenttier==X.Y.Z   # if PyPI publish is on
```

If any of these 404 or fail, investigate the workflow run before announcing.

## First-release-only chores

These only apply when the release produces artifacts for the first time:

- **New ghcr.io packages default to Private.** Flip each via
  `https://github.com/orgs/agenttier/packages/container/package/<name>/settings`
  → Change visibility → Public.
- **PyPI project claim.** The very first PyPI upload claims the name. Subsequent
  uploads use the scoped token you configure in the repo secrets.

## Rollback

If a release is unusable:

1. Don't delete the tag (it stays pointing at a real commit).
2. Tag a new patch release that reverts the offending commits.
3. Mark the bad release as a pre-release in the GitHub UI so it stops being the "latest."
4. If images are actively harmful, flip their visibility back to Private until a fix ships.
