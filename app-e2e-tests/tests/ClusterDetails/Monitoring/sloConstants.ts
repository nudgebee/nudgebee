export interface SLOWorkload {
  namespace: string;
  workload: string;
}

const namespace = process.env.KUBECTL_NAMESPACE ?? "nudgebee";
const NS_TEST = process.env.KUBECTL_NAMESPACE ?? "nudgebee-test";

export const sloWorkloads: SLOWorkload[] = [
  { namespace: namespace, workload: "app-dev" },
  { namespace: namespace, workload: "benchmark-server" },
  { namespace: namespace, workload: "cloud-collector-server" },
  { namespace: namespace, workload: "k8s-collector" },
  { namespace: namespace, workload: "k8s-collector-worker" },
  { namespace: namespace, workload: "llm-server" },
  { namespace: namespace, workload: "ml-k8s-server" },
  { namespace: namespace, workload: "notifications" },
  { namespace: namespace, workload: "ticket-server" },
  { namespace: namespace, workload: "workflow-server" },

  { namespace: NS_TEST, workload: "k8s-collector" },
  { namespace: NS_TEST, workload: "llm-server" },
  { namespace: NS_TEST, workload: "ml-k8s-server" },
  { namespace: NS_TEST, workload: "notifications" },
  { namespace: NS_TEST, workload: "ticket-server" },
  { namespace: NS_TEST, workload: "workflow-server" },
];

export const SLO_CONFIG = {
  availabilityObjective: 99,
  latencyObjective: 99,
  latencyThresholdMs: "5",
};
