install:
	poetry install

fmt:
	poetry run black .

lint:
	poetry run black --check .
	poetry run flake8 .
	poetry run mypy ./**/*.py --namespace-packages

run:
	python server.py

test:
	poetry run pytest

clean:
	rm -rf .mypy_cache __pycache__ .pytest_cache dist build *.egg-info
