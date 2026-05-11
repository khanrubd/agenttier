# Contributing to AgentTier

Thank you for your interest in contributing to AgentTier! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.22+
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
images/        - Reference Dockerfiles
docs/          - Documentation (MkDocs)
hack/          - Scripts (code generation, load testing)
test/          - Integration, e2e, and property-based tests
terraform/     - Infrastructure as Code modules
```

## Coding Standards

### Go

- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
- Use `gofmt` and `goimports` for formatting
- All exported types and functions must have doc comments
- Error messages should be lowercase and not end with punctuation
- Use structured logging (slog) — no fmt.Printf in production code
- Every source file must include the Apache 2.0 header (see `hack/boilerplate.go.txt`)

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

All PRs must include appropriate tests:

- **Bug fixes**: Include a test that reproduces the bug
- **New features**: Include unit tests and integration tests
- **Controller changes**: Include property-based tests where applicable
- **API changes**: Include OpenAPI spec updates and API integration tests

### Running Tests

```bash
make test              # Unit tests
make test-integration  # Integration tests (requires a Kubernetes cluster)
make test-property     # Property-based tests
make test-e2e          # End-to-end tests (requires Kind cluster)
make test-all          # All tests except e2e
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
