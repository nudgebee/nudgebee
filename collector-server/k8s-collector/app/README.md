# Kubernetes Collector App

## Project Overview
The **Kubernetes Collector App** is a Python-based component of the Kubernetes collection stack. It is responsible for aggregating metrics and events from the local cluster and relaying them to the central Nudgebee platform.

## Building and Running

### Commands
*   `make install`: Install dependencies via Poetry.
*   `make lint`: Run linters (black, flake8, mypy).
*   `make test`: Run unit tests via pytest.
*   `make run`: Start the collector locally.

---

## Development Context

### Project Overview
This service aggregates Kubernetes-native metrics and events. It works in conjunction with the Go-based relay server to provide a bridge between local clusters and the Nudgebee cloud.

### Key Technologies
- **Language:** Python 3.11+
- **Agent:** Kubernetes Python Client
- **Infrastructure:** Deployed as a DaemonSet or Deployment within target clusters.

### Development Conventions
- **Build System:** Poetry (dependency management).
- **Linting:** Enforced via `black` (line-length 120), `flake8`, and `mypy`.
- **Validation:** Run `make lint` and `make test` before pushing changes.
- **Testing:** Unit and integration tests using `pytest`.
