# notifications-server (Python / uv)
# Targets mirror the commands run in CI (.github/workflows/notifications-*.yaml).
.PHONY: install fmt lint typecheck test run clean

install:
	uv sync --locked

fmt:
	uv run black .

lint:
	uv run black --check .
	uv run flake8 .

typecheck:
	uv run mypy notifications_server --namespace-packages

test:
	uv run pytest

run:
	uv run python -m notifications_server.server

clean:
	rm -rf .mypy_cache __pycache__ .pytest_cache dist build *.egg-info
