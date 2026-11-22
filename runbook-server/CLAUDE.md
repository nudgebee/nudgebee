# CLAUDE.md — Runbook Server (`runbook-server`)

Guidance for agents working on automations. [`README.md`](README.md) is the full reference
(architecture, task framework, triggers, config, API, troubleshooting) — read the section for the
area you touch. This file only adds the rules a change must not break.

## What it is

Go service (Gin + Temporal + PostgreSQL + RabbitMQ) that stores, versions and runs automation
workflows. One process runs the HTTP API, Temporal workers, RabbitMQ consumers and pollers
(`cmd/main.go`). Tasks live under `internal/tasks/<category>/` and are registered in
[`internal/tasks/registry.go`](internal/tasks/registry.go).

## Commands

```bash
make validate          # lint + test — must pass before commit
make fmt
make start-local-env   # Postgres + Temporal (+ UI on :8080) for integration tests
RUN_INTEGRATION_TESTS=true go test ./tests/integration/...
make swag              # regenerate OpenAPI after changing handler annotations
```

## Rules that bite

1. **Credentials never reach logs, step inputs, errors or state.** Anything rendered from
   `{{ Secrets.* }}` is tracked and masked (`internal/workflow/redaction.go`); `set_state` refuses
   secret-derived values. A step's logger and returned error mask the account's secret values
   (`internal/tasks/secret_scrub.go`), but a URL typed in plain is not a secret: log it with
   `logsafe.RedactURL`. Log task results with `internal/logsafe` (`logsafe.Shape`), never the
   raw value. Run the `redaction_*_test.go` suite when touching templating, executor or outputs.
2. **Tasks run against real systems.** Dry-run is *not* a sandbox: only a few tasks honour
   `_dry_run` (see README → Glossary). A new task with side effects should honour it or say why
   not.
3. **Template inputs are untrusted.** Values interpolated into `kubectl` / shell / script tasks
   come from users, alerts and LLMs. Don't rely on quoting alone; validate inputs (e.g. DNS-1123
   names) before they reach a command.
4. **Report failure honestly.** A task must fail when the external call failed or returned no
   result it can prove succeeded — never `COMPLETED` with `data: null`. Keep the
   `map[string]any` output contract (plain strings wrap as `{"data": ...}`).
5. **Tenant scoping comes from headers.** Every record carries `TenantID` + `AccountID`; the API
   reads `x-tenant-id` / `x-user-id` from callers that hold the service token. A missing user is
   refused, never admin. Never return data across tenants, and don't add a route that skips the
   token without its own credential.
6. **Bundled templates re-sync on startup** (`internal/templatesource/`). Editing a template YAML
   changes every tenant's copy on the next deploy — test it in `bundled_test.go`.
7. **Every task needs a real `GetDescription()`** stating purpose, inputs and outputs — it is
   what `GET /tasks`, the builder UI and LLM authoring see.
8. **Every step runs as the run's principal.** A new task type must declare its
   `{module, class}` in `internal/tasks/authz.go`, or it is refused at run time (a test fails first).
   The same entry declares its tier (read / change / unrestricted: how much the step can change,
   a different question from who may run it); a run with a tier ceiling refuses steps above it.

## Keep the docs current

New trigger, task category or env var → update `README.md` (and `docs/TRIGGERS.md` for
triggers) in the same PR. `config/config.go`, `api/server.go` and `internal/tasks/registry.go`
are the authoritative lists.
