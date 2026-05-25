// Package annotations centralizes the Kubernetes annotation keys this
// service reads from and writes to on workload manifests.
//
// The keys are split into two prefixes:
//   - CIPrefix (`ci.<domain>`)        — used by the recommendation /
//     right-sizing flows to locate the deployment repo, branch, and Helm
//     values file for PR creation.
//   - WorkloadsPrefix (`workloads.<domain>`) — used by the LLM
//     code-analysis flow to locate the source code repo and commit.
//
// The domain defaults to `nudgebee.com` to preserve the keys that existing
// operators have already set on their clusters. OSS forks can override it
// by setting the ANNOTATION_DOMAIN env var. The override is read directly
// from the process environment via os.Getenv, so it does not depend on
// the config / viper package having loaded yet — making the keys safe to
// reference from any package's init() and from package-level var
// initializers without an import-order trick.
package annotations

import (
	"os"
	"strings"
)

// DefaultDomain is the historical annotation domain used by all existing
// operators. ANNOTATION_DOMAIN must default to this exact string to
// preserve backward compatibility.
const DefaultDomain = "nudgebee.com"

func resolveDomain() string {
	if d := strings.TrimSpace(os.Getenv("ANNOTATION_DOMAIN")); d != "" {
		return d
	}
	return DefaultDomain
}

// Resolved annotation keys. Computed once at package load (so consumers
// can use them as plain string values and as case expressions).
var (
	domain          = resolveDomain()
	CIPrefix        = "ci." + domain
	WorkloadsPrefix = "workloads." + domain

	CIGitRepo                   = CIPrefix + "/git.repo"
	CIGitHash                   = CIPrefix + "/git.hash"
	CIGitBranch                 = CIPrefix + "/git.branch"
	CIHelmValuesPath            = CIPrefix + "/helm.values.filePath"
	CIHelmRootPath              = CIPrefix + "/helm.values.rootPath"
	CIHelmReplicaJSONPath       = CIPrefix + "/helm.values.replicaJsonPath"
	CIHelmCPURequestJSONPath    = CIPrefix + "/helm.values.cpuRequestJsonPath"
	CIHelmCPULimitJSONPath      = CIPrefix + "/helm.values.cpuLimitJsonPath"
	CIHelmMemoryRequestJSONPath = CIPrefix + "/helm.values.memoryRequestJsonPath"
	CIHelmMemoryLimitJSONPath   = CIPrefix + "/helm.values.memoryLimitJsonPath"

	WorkloadGitRepo   = WorkloadsPrefix + "/git.repo"
	WorkloadGitHash   = WorkloadsPrefix + "/git.hash"
	WorkloadGitBranch = WorkloadsPrefix + "/git.branch"

	// Recommendation-PR metadata annotations written alongside the
	// generated workload spec.
	WorkloadUserID           = WorkloadsPrefix + "/user.id"
	WorkloadRecommendationID = WorkloadsPrefix + "/recommendation.id"
	WorkloadTime             = WorkloadsPrefix + "/time"

	// Autopilot annotations stored on the workload by the auto-optimize
	// flow. `WorkloadAutopilotOptimizePrefix` is intended for
	// strings.HasPrefix matches; the dotted form is convenient for
	// strings.TrimPrefix on per-field keys
	// (`workloads.<domain>/autopilot.autoOptimize.<field>`).
	WorkloadAutopilotID                = WorkloadsPrefix + "/autopilot.id"
	WorkloadAutopilotOptimizePrefix    = WorkloadsPrefix + "/autopilot.autoOptimize"
	WorkloadAutopilotOptimizeDotPrefix = WorkloadsPrefix + "/autopilot.autoOptimize."
)

// CIKey builds an annotation key under the CI prefix for the given
// suffix. Reserved for dynamic suffixes built at runtime (e.g.
// `helm.values.<resource><Request|Limit>JsonPath` constructed inside
// the right-sizing loop). Static keys should be added as named
// constants above instead.
func CIKey(suffix string) string {
	return CIPrefix + "/" + suffix
}

// WorkloadKey builds an annotation key under the workloads prefix.
// Same guidance as CIKey: reserved for dynamic suffixes; static keys
// belong as named constants.
func WorkloadKey(suffix string) string {
	return WorkloadsPrefix + "/" + suffix
}
