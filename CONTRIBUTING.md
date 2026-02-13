# Contributing to InSo Sequencer

Thanks for your interest in contributing to the InSoBlok sequencer!

## Development Setup

1. Install Go 1.22+
2. Clone the repo: `git clone https://github.com/insoblok/inso-sequencer.git`
3. Install dependencies: `go mod download`
4. Run tests: `make test`
5. Build: `make build`

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Run `golangci-lint` before submitting PRs
- Write tests for new functionality
- Keep functions small and focused

## Pull Request Process

1. Fork the repo and create a feature branch from `main`
2. Write/update tests for your changes
3. Ensure `make test` and `make lint` pass
4. Write a clear PR description explaining the change
5. Reference any related issues

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add TasteScore-weighted mempool ordering
fix: correct batch submission retry logic
docs: update RPC method documentation
test: add integration tests for block producer
```

## Issue Reporting

- Use GitHub Issues for bugs and feature requests
- Include steps to reproduce for bugs
- Tag issues appropriately (bug, enhancement, documentation)

## Architecture Decisions

For significant changes, open a discussion or RFC issue first. We use Architecture Decision Records (ADRs) in the `docs/adr/` directory.

## License

By contributing, you agree that your contributions will be licensed under Apache-2.0.
