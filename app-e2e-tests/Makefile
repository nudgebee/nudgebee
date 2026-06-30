# app-e2e-tests (Playwright / Node)
# Cross-service end-to-end tests. These require a configured live environment
# (target cluster, tenant and credentials via .env / E2E_ENVIRONMENT — see
# playwright.config.ts and the README) plus `make install`. They are NOT part of
# the root `make test`/`validate` (unit) flow; run them via the root `make e2e`.
# See the package.json scripts for environment-scoped variants (test:dev/test:test).
.PHONY: install test report clean

install:
	npm ci

test:
	npx playwright test

report:
	npx playwright show-report

clean:
	rm -rf node_modules/.cache playwright-report test-results
