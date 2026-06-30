# notifications-server (Python / Poetry)
# Targets mirror the commands run in CI (.github/workflows/notifications.yaml).
.PHONY: install fmt lint typecheck test run clean

install:
	poetry install

fmt:
	poetry run black .

lint:
	poetry run black --check .
	poetry run flake8 .

typecheck:
	poetry run mypy notifications_server --namespace-packages

test:
	poetry run pytest

run:
	poetry run python -m notifications_server.server

clean:
	rm -rf .mypy_cache __pycache__ .pytest_cache dist build *.egg-info
