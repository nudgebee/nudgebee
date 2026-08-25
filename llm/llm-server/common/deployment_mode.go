// deployment_mode.go — NUDGEBEE_DEPLOYMENT_MODE=oss runtime kill-switch.
//
// Matches the env var name used by api-server (see
// api-server/services/ee/license/deployment_mode.go) and by the FE bundle
// (see app/next.config.js's `env:` block that bridges the same name into
// the client bundle). Same env var name across all services.
//
// This helper lives in `common` so both agents/core/memory_v2.go and
// api/memory_v2.go can share it — avoids duplicating the tiny env-read
// logic across the two EE-only files that need it.
package common

import (
	"os"
	"strings"
)

// IsOSSDeploymentMode reports whether NUDGEBEE_DEPLOYMENT_MODE=oss is set.
// EE-only files use this to skip their init() overrides so the runtime
// behaviour of a standard binary matches an OSS-stripped snapshot without
// having to build the snapshot.
func IsOSSDeploymentMode() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NUDGEBEE_DEPLOYMENT_MODE")), "oss")
}
