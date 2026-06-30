# app (TypeScript / Next.js)
# Targets wrap the npm scripts used in CI (.github/workflows/app.yaml).
.PHONY: install fmt lint test build run clean

install:
	npm ci --legacy-peer-deps

fmt:
	npm run lint2:fix

lint:
	npm run lint2

test:
	npm run test

build:
	npm run build

run:
	npm run dev

clean:
	rm -rf .next node_modules/.cache
