// Provider-based predicates for filtering the cloud-accounts list in ownership UIs.
// `cloud_provider` is 'K8s' for Kubernetes clusters and 'AWS' | 'Azure' | 'GCP' |
// 'CloudFoundry' for cloud accounts. Today's ownership rules are K8s-only, so the
// account pickers in K8s rule flows should only offer K8s clusters; cloud rule flows
// (Phase 2) offer cloud accounts.
export const K8S = (provider) => provider === 'K8s';
export const CLOUD = (provider) => !!provider && provider !== 'K8s';
