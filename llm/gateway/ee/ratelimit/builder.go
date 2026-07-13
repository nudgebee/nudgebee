package ratelimit

import (
	"log/slog"
	"os"
	"time"

	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/ratelimit"
)

func init() {
	ratelimit.RegisterBuilder(build)
}

// build wires the EE rate limiter: distributed Redis (or in-memory) counters + the
// metastore-backed limit source. It returns nil when no metastore is configured
// (gateway_db_url unset) — a nil *ratelimit.Limiter is allow-all (Enabled() is
// nil-safe), matching the OSS "no limits" behavior. A counter-store init failure is
// fatal, preserving main's prior fail-fast at boot.
//
// The LimitStore's refresh goroutine is released via Limiter.Close() (deferred in
// main), which closes the limit source it wraps.
func build() *ratelimit.Limiter {
	if config.Config.GatewayDBURL == "" {
		return nil
	}
	counters, err := NewStoreFromConfig()
	if err != nil {
		slog.Error("main: rate-limit counter store init failed", "error", err)
		os.Exit(1)
	}
	limits := NewLimitStore(DBLoader(),
		time.Duration(config.Config.RoutingRefreshSeconds)*time.Second)
	return ratelimit.New(counters, limits)
}
