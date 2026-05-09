# Notifications Server

The `notifications-server` module is a system for sending notifications. It consists of a server that consumes messages from a message queue and sends notifications to various services.

## Components

### Server

The server is a FastAPI application that provides an HTTP API for interacting with the notifications system. It is responsible for:

- Receiving requests to send notifications
- Adding notification jobs to the message queue

### Message Queue

The notifications server uses RabbitMQ as a message queue. The server consumes messages from the queue and sends notifications to the appropriate services.

### Notification Services

The notifications server supports the following notification services:

- Slack
- Microsoft Teams

## Getting Started

To get started with the notifications server, you will need to:

1.  Install the dependencies
2.  Configure the module
3.  Run the server

### Dependencies

The `notifications-server` module uses [Poetry](https://python-poetry.org/) to manage its dependencies. To install the dependencies, run the following command:

```bash
poetry install
```

### Configuration

The `notifications-server` module is configured using environment variables. The following environment variables are available:

- `RABBIT_MQ_HOST`: The hostname of the RabbitMQ server
- `RABBIT_MQ_PORT`: The port of the RabbitMQ server
- `RABBIT_MQ_USERNAME`: The username for the RabbitMQ server
- `RABBIT_MQ_PASSWORD`: The password for the RabbitMQ server
- `NOTIFICATIONS_QUEUE`: The name of the notifications queue
- `SLACK_BOT_TOKEN`: The bot token for the Slack app
- `SLACK_SIGNING_SECRET`: The signing secret for the Slack app
- `MS_TEAMS_CLIENT_ID`: The OAuth client ID for the Microsoft Teams app (used for Graph API and Bot Framework)
- `MS_TEAMS_CLIENT_SECRET`: The OAuth client secret for the Microsoft Teams app (used for Graph API and Bot Framework)

**Note on Teams Bot Configuration:**
The Bot Framework connector (`teams_reply` method) uses the same credentials as the OAuth flow. Make sure:
1. Your Azure AD app is registered as a bot in Azure Bot Service
2. The bot has been added to your Teams app manifest
3. The app has been consented with the required permissions including `Chat.ReadBasic.All`
4. The messaging endpoint is properly configured in Azure Bot Service to point to `/webhooks/teams/events`

### Running the Server

To run the server, use the following command:

```bash
poetry run uvicorn notifications_server.server:app --host 0.0.0.0 --port 8080 --reload
```

---

## Development Context

### Project Overview
The **Notifications Server** is a Python-based system responsible for delivering alerts and operational notifications to external messaging platforms.

### Key Technologies
- **Language:** Python 3.11+
- **Framework:** FastAPI
- **Messaging:** RabbitMQ (for consuming notification jobs)
- **Integrations:** Slack (Webhooks & Bolt), Microsoft Teams (Graph API & Bot Framework)

### Development Conventions
- **Build System:** Poetry (for dependency management)
- **Linting:** Enforced via `black` (line-length 120), `flake8`, and `mypy`.
- **Validation:** Run `poetry run black --check .`, `poetry run flake8 .`, and `poetry run pytest` before pushing changes.
- **Testing:** Unit and integration tests use `pytest`.
