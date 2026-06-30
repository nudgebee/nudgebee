# llm/benchmark (Python / uv)
# Install mirrors CI (.github/workflows/benchmark-server.yaml): the pinned
# dependency set first (uv.lock is a `uv pip compile` requirements file despite
# its name), then the package itself in editable mode. fmt/lint/test use the
# black, flake8 and pytest tools declared in pyproject.toml.
.PHONY: install fmt lint test clean

install:
	uv pip install --system -r uv.lock
	uv pip install --system -e .

fmt:
	black .

lint:
	black --check .
	flake8 .

test:
	pytest

clean:
	rm -rf .mypy_cache __pycache__ .pytest_cache dist build *.egg-info
