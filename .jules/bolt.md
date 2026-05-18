# Bolt's Journal

## 2026-05-18 - Codebase already benchmarked regexp.MustCompile hoisting
**Learning:** The traces package has a benchmark test (`traces/workload_bench_test.go:6`) that proves inline `regexp.MustCompile` costs ~12.7us + 14KB + 120 allocs/op vs package-level compilation. This is a validated pattern in this codebase. Found 4+ additional files with the same anti-pattern that hadn't been fixed yet.
**Action:** When profiling Go services, always `grep -rn "regexp.MustCompile\|regexp.Compile" | grep -v "^.*:.*:var "` to find function-scoped compilations.

## 2026-05-18 - Tests require running infrastructure (Postgres, RabbitMQ)
**Learning:** Most api-server/services tests require a running Postgres and RabbitMQ. They'll fail in CI-less environments with "connection refused". Build + vet is the verification ceiling without infra.
**Action:** Use `go build ./...` and `go vet ./...` for verification when DB isn't available. Don't waste time trying to make integration tests pass.
