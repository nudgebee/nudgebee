// Not for OSS

// Shared literals for the Admin -> Ownership suite. Every string here is copied
// from the component that renders it, so a UI reword shows up as one edit.

// Per-test budget. The dev env's /user-management boot is slow (the tab body
// only mounts after the session resolves), so this sits above the 120s config
// default rather than relying on it.
export const SPEC_TIMEOUT_MS = 150000;

// Prefix on every rule this suite creates. The sweep in ownershipHelper keys off
// it, so a crashed run's leftovers are still removable on the next run.
export const E2E_RULE_PREFIX = "e2e-own-";

// app/src/components/user-management/OwnershipRules.jsx
export const LISTING_TITLE = "Ownership rules";

// app/src/components/ownership/OwnershipRuleModal.jsx — DOMAIN_OPTIONS labels.
export const DOMAIN_KUBERNETES = "Kubernetes";
export const DOMAIN_CLOUD = "Cloud";

// SCOPE_OPTIONS (k8s) and CLOUD_SCOPE_OPTIONS labels, in render order.
export const K8S_SCOPE_LABELS = ["Label (key = value)", "Namespace name", "Specific workloads"];
export const CLOUD_SCOPE_LABELS = ["Tag (key = value)", "Resource type", "Region", "Specific resources"];

// Modal titles — the add/edit branch of the same component.
export const ADD_MODAL_TITLE = "Add rule";
export const EDIT_MODAL_TITLE = "Edit rule";

// ds/Select's empty-search state (app/src/components/common/ds/Select.tsx).
export const NO_RESULTS_TEXT = "No results found";

// A search string no owner display name or username can contain, used to force
// the picker's empty state deterministically.
export const UNMATCHABLE_QUERY = "zzqxnotanowner";
