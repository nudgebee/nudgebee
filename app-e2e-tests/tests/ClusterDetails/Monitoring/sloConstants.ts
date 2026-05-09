export interface SLOWorkload {
  namespace: string;
  workload: string;
}

export const sloWorkloads: SLOWorkload[] = [
  // ── namespace: nudgebee ────────────────────────────────────────────────────
  { namespace: "nudgebee", workload: "app-dev" },
  { namespace: "nudgebee", workload: "benchmark-server" },
  { namespace: "nudgebee", workload: "cloud-collector-server" },
  { namespace: "nudgebee", workload: "k8s-collector" },
  { namespace: "nudgebee", workload: "k8s-collector-worker" },
  { namespace: "nudgebee", workload: "llm-server" },
  { namespace: "nudgebee", workload: "ml-k8s-server" },
  { namespace: "nudgebee", workload: "notifications" },
  { namespace: "nudgebee", workload: "ticket-server" },
  { namespace: "nudgebee", workload: "workflow-server" },

  // ── namespace: nudgebee-test ───────────────────────────────────────────────
  { namespace: "nudgebee-test", workload: "k8s-collector" },
  { namespace: "nudgebee-test", workload: "llm-server" },
  { namespace: "nudgebee-test", workload: "ml-k8s-server" },
  { namespace: "nudgebee-test", workload: "notifications" },
  { namespace: "nudgebee-test", workload: "ticket-server" },
  { namespace: "nudgebee-test", workload: "workflow-server" },
];

export const SLO_CONFIG = {
  availabilityObjective: 99,
  latencyObjective: 99,
  latencyThresholdMs: "5",
};
