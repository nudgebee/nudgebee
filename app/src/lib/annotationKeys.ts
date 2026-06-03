/**
 * Centralized constants for the Kubernetes annotation keys this app reads
 * from and writes to on workload manifests.
 *
 * The keys are split into two prefixes:
 *   - CI_PREFIX (`ci.<domain>`)        — used by the recommendation / right-sizing
 *                                        flows to locate the deployment repo, branch,
 *                                        and Helm values file for PR creation.
 *   - WORKLOADS_PREFIX (`workloads.<domain>`) — used by the LLM code-analysis flow
 *                                        to locate the source code repo and commit.
 *
 * The domain is currently hardcoded to `nudgebee.com` to preserve the keys that
 * existing operators have already set on their clusters. A follow-up change will
 * make this domain configurable via env so forks can rebrand without breaking
 * existing operators (who keep the default).
 */

const ANNOTATION_DOMAIN = 'nudgebee.com';

export const CI_PREFIX = `ci.${ANNOTATION_DOMAIN}`;
export const WORKLOADS_PREFIX = `workloads.${ANNOTATION_DOMAIN}`;

export const ANNOTATIONS = {
  CI_GIT_REPO: `${CI_PREFIX}/git.repo`,
  CI_GIT_HASH: `${CI_PREFIX}/git.hash`,
  CI_GIT_BRANCH: `${CI_PREFIX}/git.branch`,
  CI_HELM_VALUES_PATH: `${CI_PREFIX}/helm.values.filePath`,
  WORKLOAD_GIT_REPO: `${WORKLOADS_PREFIX}/git.repo`,
  WORKLOAD_GIT_HASH: `${WORKLOADS_PREFIX}/git.hash`,
} as const;

/**
 * The annotation keys that get sent to the backend as required CI metadata
 * when creating recommendation PRs.
 */
export const CI_REQUEST_ANNOTATIONS = [ANNOTATIONS.CI_GIT_HASH, ANNOTATIONS.CI_GIT_REPO, ANNOTATIONS.CI_HELM_VALUES_PATH] as const;
