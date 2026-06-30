# Root orchestration Makefile for the nudgebee monorepo.
#
# Dispatches common targets across every service. Each service keeps its own
# Makefile; this file just fans out to them via `$(MAKE) -C <dir> <target>`.
# CI calls each service's commands directly and does NOT depend on this file,
# so adding it changes no existing build behavior.
#
# Each target requires the corresponding service toolchains to be installed
# (Go + golangci-lint, Poetry/uv, Node). Run `make help` to list targets.
#
# Per-service work is expressed as <target>-<service> rules so GNU Make can run
# them in parallel, e.g. `make -j test`.

GO_SERVICES := \
	api-server/services \
	ticket-server \
	runbook-server \
	collector-server/cloud-collector \
	collector-server/k8s-collector/relay-server \
	llm/code-analysis \
	llm/llm-server

PY_SERVICES := \
	ml-k8s-server \
	llm/rag-server \
	collector-server/k8s-collector/app \
	notifications-server \
	llm/benchmark

TS_SERVICES := app
E2E_SERVICES := app-e2e-tests

# Only services whose Makefile defines the target participate in each fan-out.
# `test` is UNIT tests only (fast, no external setup). End-to-end suites
# (app-e2e-tests) are deliberately kept OUT of `test`/`validate` because they
# need a configured live environment (target cluster, tenant, credentials);
# run them explicitly with `make e2e`.
FMT_SERVICES  := $(GO_SERVICES) $(PY_SERVICES) $(TS_SERVICES)
LINT_SERVICES := $(GO_SERVICES) $(PY_SERVICES) $(TS_SERVICES)
TEST_SERVICES := $(GO_SERVICES) $(PY_SERVICES) $(TS_SERVICES)

.PHONY: help fmt lint test validate e2e
.PHONY: $(addprefix fmt-,$(FMT_SERVICES)) $(addprefix lint-,$(LINT_SERVICES)) $(addprefix test-,$(TEST_SERVICES)) $(addprefix e2e-,$(E2E_SERVICES))

help: ## Show available targets
	@echo "Nudgebee monorepo — root targets (fan out to every service):"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-9s\033[0m %s\n", $$1, $$2}'

fmt: ## Format code in every service
fmt: $(addprefix fmt-,$(FMT_SERVICES))

lint: ## Lint every service
lint: $(addprefix lint-,$(LINT_SERVICES))

test: ## Run unit tests in every service (excludes e2e)
test: $(addprefix test-,$(TEST_SERVICES))

e2e: ## Run end-to-end suites (needs a configured live env — see app-e2e-tests)
e2e: $(addprefix e2e-,$(E2E_SERVICES))

validate: lint test ## Lint then unit-test every service

$(addprefix fmt-,$(FMT_SERVICES)): fmt-%:
	@echo "==> fmt $*"
	@$(MAKE) -C $* fmt

$(addprefix lint-,$(LINT_SERVICES)): lint-%:
	@echo "==> lint $*"
	@$(MAKE) -C $* lint

$(addprefix test-,$(TEST_SERVICES)): test-%:
	@echo "==> test $*"
	@$(MAKE) -C $* test

$(addprefix e2e-,$(E2E_SERVICES)): e2e-%:
	@echo "==> e2e $*"
	@$(MAKE) -C $* test
