export interface SLOWorkload {
  namespace: string;
  workload: string;
}

// Defaults are obviously-synthetic so the constants file alone reveals no
// internal topology. Real e2e runs override via KUBECTL_NAMESPACE.
const namespace = process.env.KUBECTL_NAMESPACE ?? "test-cluster";
const NS_TEST = process.env.KUBECTL_NAMESPACE ?? "test-cluster-test";

export const sloWorkloads: SLOWorkload[] = [
  { namespace: namespace, workload: "app-dev" },
  { namespace: namespace, workload: "benchmark-server" },

  { namespace: NS_TEST, workload: "ticket-server" },
  { namespace: NS_TEST, workload: "workflow-server" },
];

export const SLO_CONFIG = {
  availabilityObjective: 99,
  latencyObjective: 99,
  latencyThresholdMs: "5",
};
