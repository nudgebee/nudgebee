# API / RPC Reference

Nudgebee's backend exposes its operations as **RPC actions** routed through a single in-app gateway. There is one source of truth for what actions exist and how they're authorized: **[`app/src/lib/actions.yaml`](../app/src/lib/actions.yaml)** — currently **~370 actions across 62 modules** (run `grep -c '^  - name:' app/src/lib/actions.yaml` for the exact count).

This page explains *how to read that file* and *how to add a new action*. The YAML itself is the contract; this doc is the map.

## How the gateway works

```
   Browser  ──GraphQL──▶  Next.js /api/graphql  ──RPC──▶  upstream service /rpc/*
                              (@lib/rpcGateway)
                              ↑
                              │
                       reads actions.yaml
                       (routing + role gate)
```

1. The frontend issues a GraphQL operation against `/api/graphql`.
2. `@lib/rpcGateway` parses the operation, looks up the matching action in `actions.yaml`, and verifies the user's `allowed_roles` against the action's `permissions:` block. **Super-admin sessions bypass this gate.**
3. The gateway forwards the call to the action's `handler` URL — a `/rpc/*` endpoint on the upstream service (api-server, llm-server, ticket-server, …) — stamping `X-ACTION-TOKEN` for server-to-server auth.
4. The upstream handler **re-validates** via its security context (tenant scoping, account access, super-admin checks). The role gate in YAML is *front-of-gate fast-fail*; never rely on it alone.

## Anatomy of an action

Each entry in `actions.yaml` has this shape:

```yaml
- name: integrations_aggregate         # <module>_<verb>_<description>
  definition:
    kind: "synchronous"                # or "async" for fire-and-forget jobs
    handler: '{{SERVICE_API_SERVER_URL}}/rpc/query'
    forward_client_headers: true       # pass trace / cookie headers through
    headers:
      - name: X-ACTION-TOKEN
        value_from_env: ACTION_API_SERVER_TOKEN
  permissions:                         # role allow-list — gateway fast-fails
    - role: tenant_admin               # if the caller has none of these
    - role: tenant_admin_readonly
```

| Field | Purpose |
| --- | --- |
| `name` | Unique action key; matches `<module>_<verb>_<description>` from the [naming convention](../CLAUDE.md#rpc-action-naming-convention) |
| `definition.kind` | `synchronous` (wait for response) or `async` (queue + return immediately) |
| `definition.handler` | Templated URL of the upstream `/rpc/*` endpoint |
| `definition.forward_client_headers` | If true, forward incoming HTTP headers (trace, cookies, …) |
| `definition.headers` | Headers the gateway *adds* — typically `X-ACTION-TOKEN` |
| `permissions` | Role allow-list; **must agree with what the upstream handler enforces** |

## Naming convention (verb taxonomy)

Action names follow `<module>_<verb>_<description>[_<version>]`. The verb taxonomy is fixed — `get`, `list`, `aggregate`, `count`, `check`, `create`, `update`, `upsert`, `delete`, `apply`, `execute`, `replay`, `cancel`, `pause`, `resume`, `publish`, `sync`, `generate`, `enable`, `disable` — and the rules for picking the right one are in [**CLAUDE.md → RPC action naming convention**](../CLAUDE.md#rpc-action-naming-convention).

**Don't invent verbs.** When two verbs both seem to fit, pick the more specific one and re-read the decision tree in CLAUDE.md.

## Adding a new action

1. **Implement the handler** on the upstream service (api-server, llm-server, ticket-server, …). The handler must enforce its own security context (`tenant_id`, account access, super-admin, etc.) — the gateway role gate is not enough.
2. **Add an entry to `actions.yaml`** with the right `name`, `handler` URL, `kind`, and `permissions:` block. The role list **must** match (or be a subset of) what the handler enforces.
3. **Exercise the action** through the dev server (`cd app && npm run dev`). The dev server parses `actions.yaml` at boot and will fail fast on malformed entries; runtime type errors from the upstream handler surface as 400/500s from `/api/graphql`.
4. **Run `cd app && npm run lint2`** to check format.

## Validating changes to `actions.yaml`

```bash
cd app
npm run dev      # parses actions.yaml at boot; exercises the changed action
npm run lint2    # oxlint + prettier check
```

The dev server fails fast on a malformed `actions.yaml`. Type errors in upstream Go handlers surface as 400 / 500s from `/api/graphql` at runtime, so exercise the action end-to-end before opening a PR.

## Looking up an existing action

Run the following from the repository root (`cd` back out of `app/` first if you ran the validation commands above).

The fastest path is `grep` against the YAML:

```bash
grep -n 'name: <module>_' app/src/lib/actions.yaml          # all actions in a module
grep -B1 -A8 'name: integrations_aggregate' app/src/lib/actions.yaml   # full definition
```

For the upstream handler implementation, search the relevant service:

```bash
grep -rnE '"<action-name>"|/rpc/<endpoint>' api-server/services/  # or whichever service the handler URL points at
```

## Related references

- [CLAUDE.md → RPC action naming convention](../CLAUDE.md#rpc-action-naming-convention) — verb taxonomy + the decision tree for picking one
- [CLAUDE.md → Database Migrations & RPC Actions](../CLAUDE.md#database-migrations--rpc-actions) — Postgres migration scaffold for actions that need schema changes
- [`app/src/lib/actions.yaml`](../app/src/lib/actions.yaml) — the contract; this doc is just the map
