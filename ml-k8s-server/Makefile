install:
	poetry install

fmt:
	poetry run black .

lint:
	poetry run black --check .
	poetry run flake8 .
	poetry run mypy server --namespace-packages

run:
	python -m server.app

test:
	poetry run pytest

clean:
	rm -rf .mypy_cache __pycache__ .pytest_cache dist build *.egg-info
