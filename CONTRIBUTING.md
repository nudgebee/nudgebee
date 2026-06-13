# Contributing to Nudgebee

Thanks for your interest in contributing to Nudgebee. This document
explains how to file issues, propose changes, and get a pull request
merged.

By contributing, you agree that your contributions will be licensed
under the Apache License, Version 2.0 (see [LICENSE](./LICENSE)).

> **New here?** Two fast paths in before this doc:
>
> - **Want to get the stack running?** → [README → Quick Start](./README.md#quick-start-local-development) (or the shorter [docs/QUICKSTART.md](./docs/QUICKSTART.md)).
> - **Want to understand what fits where?** → [README → Architecture](./README.md#architecture) (or [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md) for the deep-dive entry points).
>
> Then come back here for the workflow (fork → branch → validate → PR).

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

   | Service type                    | Command                                             |
   | ------------------------------- | --------------------------------------------------- |
   | Go service with Makefile        | `make validate`                                     |
   | Python service with Makefile    | `make lint && make test`                            |
   | Python service without Makefile | `poetry run black --check . && poetry run flake8 .` |
   | TypeScript frontend (`app/`)    | `npm run lint2 && npm run test`                     |

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

## Local Development Setup

Each service ships a `.env.example`. The flow is the same for every
service: bring up the shared infrastructure once with Docker Compose,
copy the `.env.example` to `.env` for each service you intend to run,
edit only what you need, then run that service from source.

### 1. Bring up shared infrastructure

```bash
docker compose up -d postgres rabbitmq redis qdrant temporal
```

You rarely need all 20+ compose services running at once — see the
"Common minimal stacks" table in the root [README](./README.md) for
typical combinations.

### 2. Configure per-service environment

```bash
# Example: services-server
cp api-server/services/.env.example api-server/services/.env
# Edit values as needed
$EDITOR api-server/services/.env
```

Repeat for every service you plan to run from source. The bundled
`.env.example` files document every variable inline; you usually only
need to set the ones that differ from the defaults.

### 3. Run servers from source

Each service has a `make run` (or `npm run dev` / `poetry run …`)
target — see the per-service `README.md` for specifics. Start the
frontend last, after the backends it depends on are healthy.

### Cross-service env vars that MUST match

A few values are shared across services and must be **identical** in
every `.env` of every running service — otherwise services fail to
authenticate or to decrypt each other's data.

| Variable                  | Where it must match                                                           |
| ------------------------- | ----------------------------------------------------------------------------- |
| `NUDGEBEE_ENCRYPTION_KEY` | `app`, `api-server/services`, anywhere that reads `integration_config_values` |
| `ACTION_API_SERVER_TOKEN` | `app` ↔ `api-server/services`, `notifications-server`, `ticket-server`, …     |
| `NEXTAUTH_SECRET`         | `app` only, but must be stable across restarts                                |

If you rotate one of these in one service and forget to update the others, the symptom is usually a `401 Unauthorized` on RPC calls between services, or an `Invalid IV` / decryption failure when reading stored integration credentials.

### Troubleshooting

**`fetch failed` on RPC calls between services.** Usually one of the upstream services isn't running or isn't reachable from the caller. Check `RELAY_SERVER_ENDPOINT`, `SERVICE_API_SERVER_URL`, `LLM_SERVER_URL`, etc. in the caller's `.env` — they default to in-cluster service-discovery names that won't resolve outside Kubernetes.

**Postgres `connection refused`.** The compose service is named `postgres` (resolves inside the compose network) but if you're running a service from source on the host, it should point at `localhost:5432`. Check the `APP_DATABASE_URL` host name.

**RabbitMQ messages stuck in the queue.** The consumer for that queue isn't running. Each service's `README.md` lists which queues it consumes. Check `docker logs rabbitmq` or visit the management UI at `http://localhost:15672` (user/pass: `guest`/`guest`).

**Frontend says "Invalid IV" / decryption errors when reading integrations.** `NUDGEBEE_ENCRYPTION_KEY` mismatch between the service that wrote the row and the service reading it. Make sure every `.env` you're running has the same value.

**`npm install` fails with peer-dependency errors.** Use `npm install --legacy-peer-deps` in the `app/` directory — required by some legacy MUI v4 / React Hook Form combinations.

**`npm run lint2` fails on `[warn] README.md` (prettier).** You added a markdown table whose column widths don't match prettier's auto-formatting. Run `cd app && npx prettier@2.8.8 --write ../README.md` (or `npx prettier --write README.md` from the repo root) and re-commit. CI uses prettier v2, so pin the version when running from `app/`.

**Build OOM on `npm run build`.** Set `NODE_OPTIONS=--max_old_space_size=4096` before running. The CI workflow does this automatically; local builds need it manually for large branches.

**Go module errors / `cannot find module`.** Run `go mod download` then `go mod tidy` in the service directory. If you see private-module errors, the monorepo is fully public — there are no private dependencies, so an error here usually means a corrupted module cache (`go clean -modcache` to nuke and re-fetch).

**Python `mypy` errors after pulling main.** Stale `.mypy_cache/`. Remove it recursively: `find . -name ".mypy_cache" -type d -exec rm -rf {} +`. The `make clean` target in each Python service also does this for that service.

If none of the above match, search the issue tracker — many setup issues have prior threads. Still stuck? Reach out to the contacts on the [new-issue page](https://github.com/nudgebee/nudgebee/issues/new).

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

Open a GitHub Discussion or issue, join us on [Discord](https://discord.gg/g2spbkGFC5),
or email **legal@nudgebee.com** for matters that are not appropriate for public
discussion.
