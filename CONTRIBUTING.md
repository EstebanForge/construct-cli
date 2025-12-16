# Contributing to Construct CLI

Thank you for your interest in contributing! 

## Guidelines

This document provides community guidelines for a safe, respectful, productive, and collaborative place for any person who is willing to contribute to the this project. It applies to all “collaborative space”, which is defined as community communications channels (such as mailing lists, submitted patches, commit comments, etc.).

- Participants will be tolerant of opposing views.
- Participants must ensure that their language and actions are free of personal attacks and disparaging personal remarks.
- When interpreting the words and actions of others, participants should always assume good intentions.
- Behaviour which can be reasonably considered harassment will not be tolerated.

## Dev Quick Start

```bash
git clone https://github.com/EstebanForge/construct-cli.git
cd construct-cli
make build
make test
```

## Development Workflow

1. Fork and create a feature branch
2. Make changes and add tests
3. Run `make ci` to verify
4. Submit a pull request

## Code Style

- Run `make fmt` before committing
- Ensure `make lint` passes
- Add tests for new features
- Update documentation

## Testing

```bash
make test          # All tests
make test-unit     # Unit tests only
make test-integration  # Integration tests
```

## Pull Request Guidelines

- Clear description of changes
- Tests included
- Documentation updated
- All CI checks passing

For detailed guidelines, see the README.md testing section.
