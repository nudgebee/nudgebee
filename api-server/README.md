# API Server

## Description
- This repo hosts the central API server for Nudgebee. The frontend issues GraphQL-shaped operations, which the in-app gateway (`app/src/lib/rpcGateway.ts`) parses and dispatches to action handlers here (current upstream URLs are `/hasura/*` for historical reasons).
- Requests are stateless; callers authenticate with a bearer/JWT token (or `X-ACTION-TOKEN` for service-to-service traffic).
- Handlers in `services/api/actions_*.go` register against action names declared in [`app/src/lib/actions.yaml`](../app/src/lib/actions.yaml).
- Database schema is managed by golang-migrate. See [`api-server/migrations/README.md`](migrations/README.md).

## Technologies
- Go / Gin
- PostgreSQL
- golang-migrate (schema migrations)
- Docker

## Running locally

The fastest local loop is to run the Go binary directly with `.env` populated from `.env.example`:

```bash
cd api-server/services
make run                 # go run ./cmd
make validate            # fmt + lint + test (run before pushing)
```

Postgres expectations: the runner reads `APP_DATABASE_URL`. A local default is `postgres://postgres:postgres@localhost:5432/nudgebee?sslmode=disable`; override in `.env` as needed.

## Database migrations

Schema changes live under [`api-server/migrations/migrations/app/`](migrations/migrations/app) (flat `{ts_ms}_V{N}_*.sql` layout). Use the scaffold script — never hand-pick timestamps. Run from the repository root:

```bash
./api-server/migrations/new-migration.sh <snake_case_name>
```

See [`api-server/migrations/README.md`](migrations/README.md) for the full reference (Clickhouse + RabbitMQ trees, recovery from `dirty=true`, etc.).

## Authentication
- Frontend traffic carries a NextAuth session cookie; the rpcGateway extracts the JWT and forwards session variables (`x-hasura-user-tenant-id`, `x-hasura-allowed-roles`, …) to upstream handlers as the wire-format contract.
- Service-to-service callers present `X-ACTION-TOKEN: $ACTION_API_SERVER_TOKEN`.

## API surface
- All actions are registered in [`app/src/lib/actions.yaml`](../app/src/lib/actions.yaml). Handler URLs route to `{{SERVICE_API_SERVER_URL}}/hasura/<group>` (or other services' equivalent path for non-api-server actions). The `/hasura/` prefix is the path the in-app gateway forwards to today; the engine itself is gone.
- See `swagger.yaml` / `swagger.json` under [`docs/swagger/`](services/docs/swagger) for the per-endpoint contract.

## Adding a new RPC action

1. Pick a name following the `<module>_<verb>_<description>_[<version>]` convention (root [`CLAUDE.md`](../CLAUDE.md#rpc-action-naming-convention) has the rules).
2. Add the action entry to [`app/src/lib/actions.yaml`](../app/src/lib/actions.yaml) (handler URL, permissions, forward-client-headers).
3. Implement the handler under `services/api/actions_<group>.go`, dispatching on the action name. Use `buildContextFromPayload(c, actionPayload, …)` to construct the security context.
4. Validate with `make validate` (api-server) and exercise the action end-to-end via `cd app && npm run dev`.
