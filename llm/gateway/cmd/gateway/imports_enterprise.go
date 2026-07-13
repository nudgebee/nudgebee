// This file blank-imports the gateway's EE extension packages so their init()
// side effects (registering the DB routing-rule loader, the Redis rate-limit
// builder, the per-tenant BYO-key resolver, and the full-body/PHI log sink) run at
// startup. main.go itself references none of these packages — it wires them only
// through the OSS registration hooks (routing.RegisterRuleLoader,
// ratelimit.RegisterBuilder, proxy.RegisterCredResolver, metering.RegisterBodyLogSink).
//
// The OSS distribution is produced by FOLDER-REMOVAL (see the repo-root
// .oss-exclude): the ee/ subtree and THIS bridge file are physically deleted, and
// the OSS build then compiles against the OSS defaults (static routing rules only,
// no rate limiting, operator-default credentials, no body logging). No build tag is
// needed — removal is the mechanism.
//
// Add new EE extension registrations here, not in main.go.

package main

import (
	_ "nudgebee/llm-gateway/ee/bodylog"   // registers the full-body/PHI log sink
	_ "nudgebee/llm-gateway/ee/ratelimit" // registers the Redis rate-limit builder
	_ "nudgebee/llm-gateway/ee/routing"   // registers the DB routing-rule loader
	_ "nudgebee/llm-gateway/ee/secrets"   // registers the per-tenant BYO-key resolver
)
