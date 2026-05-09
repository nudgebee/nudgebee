install:
	pip install -e ".[dev,test]"

fmt:
	black .

lint:
	black --check .
	flake8 .
	mypy ./**/*.py --namespace-packages

run:
	uvicorn server:app --reload --port 9999

run-search:
	python rag/search_service.py

test:
	pytest

clean:
	rm -rf .mypy_cache __pycache__ .pytest_cache dist build *.egg-info
