// Standalone module so the validation harness pulls no dependencies into the
// gateway itself. Pure stdlib — builds offline, runs in CI with `go run .`.
module nudgebee/gateway-matrix

go 1.26
