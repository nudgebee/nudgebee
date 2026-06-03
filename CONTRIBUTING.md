# Contributing to Nudgebee

Thanks for your interest in contributing to Nudgebee. This document
explains how to file issues, propose changes, and get a pull request
merged.

By contributing, you agree that your contributions will be licensed
under the Apache License, Version 2.0 (see [LICENSE](./LICENSE)).

## Contributor License Agreement (CLA)

Before your first PR can be merged, you'll be asked to sign our
Contributor License Agreement. This is handled automatically by the
[CLA Assistant](https://cla-assistant.io/) bot — when you open your
first PR, it will post a comment with a one-click link to sign.
Subsequent PRs require no further action.

The CLA confirms that (a) you have the right to contribute the code
you're submitting and (b) you grant Nudgebee the license to use it
under Apache 2.0. It does not transfer copyright — you retain it for
your contributions.

## Code of Conduct

All participation in this project is governed by our
[Code of Conduct](./CODE_OF_CONDUCT.md). Please read it before
engaging in issues, discussions, or pull requests. Report unacceptable
behavior to **legal@nudgebee.com**.

## Ways to Contribute

- **Report a bug** — open a GitHub issue using the bug template.
- **Request a feature** — open a GitHub issue using the feature
  template and describe the use case.
- **Improve documentation** — typo fixes, clarifications, and new
  guides are always welcome.
- **Submit code** — see the workflow below.

## Project Layout

Nudgebee is a monorepo of 16 services (Go, Python, TypeScript)
deployed on Kubernetes. See [`CLAUDE.md`](./CLAUDE.md) for a full
service map and per-service build commands. Each service has its own
`Makefile` (where applicable) and CI workflow under
`.github/workflows/`.

## Development Workflow

1. **Fork** the repository and create a feature branch from `main`:
   ```bash
   git checkout -b feat/short-description
   ```
   Branch naming: `feat/...`, `fix/...`, `docs/...`, `refactor/...`,
   `chore/...`.

2. **Make your changes.** Keep the change focused — avoid mixing
   unrelated refactors into a feature PR.

3. **Validate locally before pushing.** Each service has its own
   validation command. The most common ones:

   | Service type | Command |
   |---|---|
   | Go service with Makefile | `make validate` |
   | Python service with Makefile | `make lint && make test` |
   | Python service without Makefile | `poetry run black --check . && poetry run flake8 .` |
   | TypeScript frontend (`app/`) | `npm run lint2 && npm run test` |

   See [`CLAUDE.md`](./CLAUDE.md) for the full per-service table.

4. **Write tests.** Bug fixes should include a regression test;
   features should include unit tests at minimum.

5. **Commit** using the Conventional Commits format:
   ```
   <type>(<scope>): <short description>
   ```
   - **type**: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`,
     `test`, `chore`, `revert`, `ci`, `infra`, `release`
   - **scope** (optional): `autopilot`, `workflow`, `ml`, `llm`,
     `notifications`, `ui`, `tickets`, `relay`, `collector`, `deps`,
     etc.

   Examples:
   ```
   fix(llm): handle null pointer in config loader
   feat(ui): add user settings page
   docs: clarify local validation steps
   ```

6. **Open a pull request** against `main`. CI will run the relevant
   service's lint, type check, and test suite automatically.

## Pull Request Guidelines

- **Link an issue.** PRs targeting `main` should reference a GitHub
  issue (`Fixes #<n>` or `Refs #<n>`). If no issue exists for what
  you're working on, open one first so the change can be discussed
  before code review.
- **Keep PRs small.** Aim for under ~400 lines of diff where
  possible. Split larger work into a series of PRs.
- **Fill in the PR template.** Describe the change, link to the
  issue, and check the applicable "type of change" boxes.
- **All CI checks must pass** before review.
- **Be responsive to review feedback.** Push fixups as new commits;
  we'll squash on merge.

## Branch Model

- The `main` branch is the development branch that all
  contributions target.
- Open your pull request against `main`.
- Maintainers handle releases and any downstream branches; you do
  not need to target them in your PR.

## Security

If you believe you've found a security vulnerability, **do not open
a public issue**. Report it privately via the process in
[SECURITY.md](./SECURITY.md) — email **security@nudgebee.com** or
GitHub's [private vulnerability reporting](https://github.com/nudgebee/nudgebee/security/advisories/new).
We acknowledge receipt within two business days and coordinate a fix
and disclosure timeline with you.

## Trademarks

The name "Nudgebee" and any associated logos are trademarks of
Nudgebee. The Apache 2.0 license does not grant trademark rights.
See [TRADEMARKS.md](./TRADEMARKS.md) for the project's trademark
policy.

## Questions

Open a GitHub Discussion or issue, or email **legal@nudgebee.com**
for matters that are not appropriate for public discussion.
