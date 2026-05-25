# notifications-server

FastAPI service that delivers notifications to Slack, Microsoft Teams, Google Chat, and email. Consumes notification events from RabbitMQ and exposes an HTTP API for synchronous send, OAuth installation flows, and incoming platform webhooks.

Part of the [Nudgebee](https://github.com/nudgebee/nudgebee) monorepo. See the root [README](../README.md) for the full-stack quick start.

## Quick Start (Local Development)

### Prerequisites

- Python 3.11+
- [Poetry](https://python-poetry.org/docs/#installation)
- Running Postgres (`postgres:16`) and RabbitMQ (`rabbitmq:3-management`)

Bring up the infra containers from the repo root:

```bash
docker compose up -d postgres rabbitmq
```

### 1. Install dependencies

```bash
cd notifications-server
poetry install
```

### 2. Configure environment

Create `notifications-server/.env` with the minimum local-dev values:

```bash
cat > .env <<'EOF'
# Database
NOTIFICATION_DB_URL=postgresql+asyncpg://postgres:postgrespassword@localhost:5432/nudgebee

# RabbitMQ
RABBIT_MQ_HOST=localhost
RABBIT_MQ_PORT=5672
RABBIT_MQ_USERNAME=guest
RABBIT_MQ_PASSWORD=guest

# Auth — must match api-server's ACTION_API_SERVER_TOKEN
ACTION_API_SERVER_TOKEN=youractiontoken

# Frontend base URL (used in email/Slack/Teams links)
BASE_URL=http://localhost:3000
EOF
```

Add credentials only for the channels you intend to test (Slack, Teams, Google Chat, email) — see [Configuration](#configuration) below.

### 3. Run the server

```bash
poetry run uvicorn notifications_server.server:app --host 0.0.0.0 --port 8080 --reload
```

Listens on <http://localhost:8080>. Health check: <http://localhost:8080/health>.

### 4. Run tests

```bash
poetry run pytest
```

### 5. Lint

```bash
poetry run black --check .
poetry run flake8 .
poetry run mypy notifications_server
```

Auto-fix formatting: `poetry run black .`

## Architecture

```
            +--------------------+        +-----------+
            |   api-server       |        |  app      |
            |   workflow-server  |        |  (Next.js)|
            +---------+----------+        +-----+-----+
                      |  RabbitMQ                | HTTP
                      v  (queue: notifications)  v
                +-----+--------------------------+-----+
                |          notifications-server        |
                |  +-----------+   +----------------+  |
                |  | Consumer  |   |  HTTP routers  |  |
                |  +-----+-----+   +--------+-------+  |
                |        |                  |          |
                |        v                  v          |
                |   NotificationProcessor / CommonService
                +---+----+---+----------+-----+--------+
                    |    |   |          |     |
                    v    v   v          v     v
                  Slack Teams GChat    SMTP   Postgres
```

**Two ingress paths:**

1. **RabbitMQ consumer** — Background asyncio task consumes `notifications` queue. Used by api-server, workflow-server, and collectors to fire-and-forget event notifications (findings, anomalies, daily highlights, etc.).
2. **HTTP API** — Synchronous endpoints used by the frontend (OAuth installs, send-email for signup) and other backend services (send-message, channel/user listing, reactions).

## HTTP API Surface

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Liveness probe |
| POST | `/api/messages/send` | Send a notification to one or more configured channels |
| POST | `/api/emails/send` | Send an email (raw body or template) |
| POST | `/api/users/send` | Send a direct message to a platform user |
| POST | `/api/channels/list` | List channels for a platform (RPC action) |
| POST | `/api/channels/join` | Bot joins a channel (Slack) |
| POST | `/api/channels/message` | Send a message to a channel (Slack) |
| POST | `/api/users/list` | List users for a platform (RPC action) |
| POST | `/api/reactions/add` | Add an emoji reaction to a message |
| POST | `/api/threads/messages` | Fetch messages from a thread |
| POST | `/api/notifications/test` | Send a test notification (RPC action) |
| POST | `/api/rules/save` | Create/update a notification routing rule (RPC action) |
| GET | `/api/integrations/install/ms-teams` | Begin MS Teams OAuth install |
| POST | `/api/integrations/callback/ms-teams` | Complete MS Teams OAuth install |
| GET | `/api/integrations/install/google` | Begin Google Chat OAuth install |
| POST | `/api/integrations/callback/google` | Complete Google Chat OAuth install |
| GET | `/slack/install` | Begin Slack OAuth install |
| GET | `/slack/oauth_redirect` | Slack OAuth callback |
| POST | `/webhooks/slack/events` | Slack Events API webhook |
| POST | `/webhooks/slack/interactive` | Slack interactive components webhook |
| POST | `/webhooks/msteams/events` | MS Teams Bot Framework webhook |
| POST | `/webhooks/google-chat/events` | Google Chat events webhook |
| POST | `/llm/response` | Async callback for LLM completions |

Endpoints under `/api/` that mutate routing rules or test notifications require the `X-ACTION-TOKEN` header (must match `ACTION_API_SERVER_TOKEN`). Webhook endpoints validate platform-specific signatures (Slack signing secret, etc.) at the transport layer.

## Configuration

All configuration is via environment variables. Defaults assume a Kubernetes deployment with service-discovery DNS; override for local dev.

### Core

| Variable | Default | Purpose |
|---|---|---|
| `NOTIFICATION_DB_URL` | `postgresql://postgres:password@127.0.0.1:5432/nudgebee` | Async Postgres DSN |
| `BASE_URL` | *(empty)* | Frontend base URL used in outbound links |
| `ACTION_API_SERVER_TOKEN` | *(empty)* | Shared secret for RPC-action endpoints |
| `LLM_SERVER_TOKEN` | *(empty)* | Shared secret for LLM callbacks |

### RabbitMQ

| Variable | Default |
|---|---|
| `RABBIT_MQ_HOST` | `localhost` |
| `RABBIT_MQ_PORT` | `5672` |
| `RABBIT_MQ_USERNAME` | `guest` |
| `RABBIT_MQ_PASSWORD` | `password` |
| `NOTIFICATIONS_QUEUE` | `notifications` |
| `RABBITMQ_PREFETCH_COUNT` | `20` |
| `RABBITMQ_DEAD_LETTER_DELAY_MS` | `5000` |

### Redis / cache

| Variable | Default |
|---|---|
| `CACHE_PROVIDER` | `memory` (alt: `redis`) |
| `REDIS_SERVER_HOST` | `localhost` |
| `REDIS_SERVER_PORT` | `6379` |
| `REDIS_USER_PASSWORD` | *(empty for local)* |
| `CACHE_EXPIRATION_MINUTES` | `30` |

### Slack

| Variable | Required for |
|---|---|
| `SLACK_CLIENT_ID` | OAuth install flow |
| `SLACK_CLIENT_SECRET` | OAuth install flow |
| `SLACK_SIGNING_SECRET` | Webhook signature verification |
| `SLACK_BOT_TOKEN` | Bot-token mode (alternative to OAuth) |
| `SLACK_AUTH_TYPE` | `OAuth` (default) or `BotToken` |

Configure the Slack App's redirect URL to `${BASE_URL}/slack/oauth_redirect` and the event subscription URL to `${BASE_URL}/webhooks/slack/events`.

### Microsoft Teams

| Variable | Required for |
|---|---|
| `MS_TEAMS_CLIENT_ID` | OAuth + Graph API |
| `MS_TEAMS_CLIENT_SECRET` | OAuth + Graph API |
| `MS_TEAMS_AUTHORITY` | `https://login.microsoftonline.com/common` |
| `MS_TEAMS_BOT_ENDPOINT` | Bot Framework endpoint |

Register the Azure AD app as a bot in Azure Bot Service, add it to your Teams app manifest, and point the Bot Service messaging endpoint at `${BASE_URL}/webhooks/msteams/events`.

### Google Chat

| Variable | Required for |
|---|---|
| `GOOGLE_CLIENT_ID` | OAuth |
| `GOOGLE_CLIENT_SECRET` | OAuth |
| `GOOGLE_CHAT_PROJECT_NUMBER` | Chat API |

Configure the Google Cloud project's OAuth redirect to `${BASE_URL}/api/integrations/callback/google` and the Chat app's event endpoint to `${BASE_URL}/webhooks/google-chat/events`.

### Email (SMTP)

| Variable | Purpose |
|---|---|
| `EMAIL_SERVER_HOST` | SMTP host |
| `EMAIL_SERVER_PORT` | SMTP port |
| `EMAIL_SERVER_USER` | SMTP username |
| `EMAIL_SERVER_PASSWORD` | SMTP password |
| `EMAIL_FROM` | Default From address |
| `EMAIL_MAX_CONCURRENT_SENDS` | Concurrency limit (default 10) |

### Upstream service URLs

| Variable | Default | Used for |
|---|---|---|
| `SERVICE_API_SERVER_URL` | `http://services-server:8000` | Audit logs, security context, AI feedback |
| `AUTO_PILOT_URL` | `http://auto-pilot-server:9988` | Skip scheduled optimize executions |
| `LLM_SERVER_URL` | `http://llm-server:8000` | LLM completions |
| `WORKFLOW_SERVER_URL` | `http://workflow-server:8000` | Workflow approvals |

## Branding

Email templates and bot replies pull their look and feel from a theme file. The bundled default ships at `notifications_server/branding/default/theme.json`.

To customize for your deployment, mount your own theme file and point the server at it:

```bash
TENANT_BRANDING_FILE=/etc/nudgebee/theme.json
```

The schema is documented inline in [`branding/default/theme.json`](notifications_server/branding/default/theme.json). The `email` block (logo, support email, colors, address, copyright year) drives email rendering; `colorTokens` and `theme.palette` are shared with the frontend's theme.

## RabbitMQ Message Format

The consumer reads JSON messages from the `notifications` queue. Failed messages are sent to a delayed queue (`notifications_delayed`) and retried via the `dlx` dead-letter exchange after `RABBITMQ_DEAD_LETTER_DELAY_MS` ms.

Message envelope (see `notifications_server/schemas/message.py` for the full Pydantic model):

```json
{
  "tenant_id": "<uuid>",
  "type": "finding",
  "parameters": { /* type-specific payload */ },
  "thread": { "platform": "slack", "channel_id": "...", "team_id": "..." }
}
```

`type` accepts `"generic"`, `"finding"`, or a template name. The matching template handler renders the message and dispatches to all configured channels for the tenant.

## Project Layout

```
notifications_server/
├── server.py              # FastAPI entrypoint, lifespan, router mounting
├── routers/               # HTTP route handlers
│   ├── common.py          # /api/messages, /api/emails, /api/channels, ...
│   ├── tools.py           # OAuth install + callback flows
│   ├── actions.py         # /webhooks/* (Slack, Teams, Google Chat events)
│   └── llm_callbacks.py   # /llm/response
├── services/              # Business logic per platform + shared services
├── clients/               # Thin wrappers over Slack/Teams/GChat SDKs
├── processors/            # RabbitMQ message processors
├── rabbitmq/              # Consumer + connection management
├── repositories/          # SQLAlchemy data access
├── models/                # ORM models, enums
├── schemas/               # Pydantic request/response models
├── message_templates/     # Per-platform rendering (slack, ms_teams, google_chat)
├── emailer/               # SMTP sender + Jinja templates
├── branding/default/      # Default theme.json + assets
└── configs/settings.py    # Pydantic Settings (all env vars)
```

## Contributing

This service follows the monorepo conventions documented in the root [CONTRIBUTING.md](../CONTRIBUTING.md). In short:

- Run `poetry run black .`, `poetry run flake8 .`, `poetry run mypy notifications_server`, and `poetry run pytest` before pushing.
- Open PRs against `main`; CI deploys to test and prod via subsequent automated PRs.
