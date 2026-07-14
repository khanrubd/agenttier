# Contributing to AgentTier

Thank you for your interest in contributing to AgentTier! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.25+ (matches `go.mod`; the toolchain pin is 1.25.x)
- Docker (for building container images)
- Kind (for local Kubernetes testing)
- kubectl
- Helm 3.x
- Node.js 20+ (for web-ui development)
- golangci-lint
- controller-gen (install via `make install-tools`)

### Getting Started

```bash
# Clone the repository
git clone https://github.com/agenttier/agenttier.git
cd agenttier

# Install development tools
make install-tools

# Build all binaries
make build

# Run tests
make test

# Run linter
make lint
```

### Project Structure

```
cmd/           - Application entrypoints (controller, router, cli)
pkg/           - Shared Go packages
api/v1alpha1/  - CRD type definitions
config/        - Generated manifests (CRDs, RBAC, samples)
web-ui/        - React frontend
helm/          - Helm chart
docker/        - Dockerfiles for the controller + router images
images/        - Reference Dockerfiles for sandbox images
ci/            - CodeBuild buildspecs (build / deploy / teardown)
docs/          - Documentation (MkDocs)
scripts/       - Scripts (code generation, load testing)
test/          - Integration, e2e, and property-based tests (planned; not present yet)
terraform/     - Infrastructure as Code modules
```

## Coding Standards

### Go

- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
- Use `gofmt` and `goimports` for formatting
- All exported types and functions must have doc comments
- Error messages should be lowercase and not end with punctuation
- Use structured logging (slog) — no fmt.Printf in production code
- Every source file must include the Apache 2.0 header (see `scripts/boilerplate.go.txt`)

### TypeScript (Web UI)

- Use TypeScript strict mode
- Use functional components with hooks
- Follow the existing plain CSS approach (no component libraries)
- Use Prettier for formatting

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(controller): add idle timeout enforcement
fix(router): handle WebSocket reconnection race condition
docs: update quickstart guide for EKS
test(e2e): add sandbox cloning test
chore: update Go dependencies
```

## Testing Requirements

All PRs must include appropriate tests. Today that means **unit tests**, written
as `_test.go` files colocated with the code under `pkg/` and `api/` (run by
`make test`):

- **Bug fixes**: Include a test that reproduces the bug
- **New features**: Include unit tests covering the new behavior and its edge cases
- **Controller changes**: Table-driven reconcile tests against a fake client (see the existing `pkg/controller/*_test.go`)
- **API changes**: Run `make verify-codegen` and commit the regenerated `api/`, `config/`, and `pkg/crds/` files

The integration, e2e, and property tiers described under "Running Tests" are
planned; once the `test/` tree lands, feature and controller PRs will be
expected to exercise them too.

### Running Tests

```bash
make test              # Unit tests (pkg/ + api/) — this is what CI runs
```

The integration, e2e, and property tiers are planned but not implemented yet —
the `test/` tree doesn't exist, so these targets no-op with a notice rather than
running anything:

```bash
make test-integration  # planned — skips until test/integration/ exists
make test-property     # planned — skips until test/property/ exists
make test-e2e          # planned — skips until test/e2e/ exists (requires Kind)
make test-all          # unit tests plus any tiers that are present
```

### Test Coverage

We aim for >80% coverage on core packages (`pkg/controller/`, `pkg/router/`).

## Pull Request Process

1. Fork the repository and create a feature branch from `main`
2. Make your changes with appropriate tests
3. Run `make lint test` and ensure everything passes
4. Run `make verify-codegen` if you changed API types
5. Update documentation if your change affects user-facing behavior
6. Submit a PR with a clear description of the change

### PR Description Template

- **What**: Brief description of the change
- **Why**: Motivation and context
- **How**: Implementation approach
- **Testing**: What was tested and how
- **Breaking Changes**: Any breaking changes (if applicable)

## Code Review

- All PRs require at least one approval from a maintainer
- CI must pass (lint, test, build)
- Reviewers should focus on: correctness, security, performance, maintainability

## Release Process

Releases follow semantic versioning (vMAJOR.MINOR.PATCH):

- **Patch**: Bug fixes, no API changes
- **Minor**: New features, backward-compatible API additions
- **Major**: Breaking changes (CRD schema changes that require migration)

Releases are triggered by pushing a git tag: `git tag v0.1.0 && git push --tags`

## Getting Help

- Open a [GitHub Issue](https://github.com/agenttier/agenttier/issues) for bugs or feature requests
- Join discussions in GitHub Discussions
- Tag `@agenttier/maintainers` for urgent issues
