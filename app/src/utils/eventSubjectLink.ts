import { AGGREGATION_KEY, SUBJECT_TYPE } from 'src/data/investigateConstants';

// Cloud-resource `type` for an actual Pod row. Distinct from SUBJECT_TYPE.POD,
// which is the event's own (lowercased) subject vocabulary.
export const RESOURCE_TYPE_POD = 'Pod';

// Kinds that have a page under Kubernetes > Applications (backed by
// k8s_workloads). Deliberately an allow-list: ReplicaSet is a k8s_workloads
// kind too but is excluded from that listing, and a node is not a workload at
// all — linking either one dead-ends.
const WORKLOAD_RESOURCE_TYPES = ['Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'Rollout'];

// The event-side (lowercased) spelling of those same kinds. Non-k8s subjects —
// cloudsql_database, ec2-instance, gke_cluster, loadbalancer, queue, node —
// all carry a cloud_resource_id too, but none of them has a page this link can
// reach, so they must not be styled as links.
const WORKLOAD_SUBJECT_TYPES = ['deployment', 'statefulset', 'daemonset', 'job', 'cronjob', 'rollout'];

type EventRow = {
  subject_type?: string;
  subject_name?: string;
  subject_namespace?: string;
  cloud_resource_id?: string;
  cloud_account_id?: string;
  aggregation_key?: string;
};

type Resource = {
  id?: string;
  name?: string;
  type?: string;
};

const workloadHref = (accountId: string, namespace: string, workloadName: string) =>
  `/kubernetes/details/${accountId}?namespace=${encodeURIComponent(namespace)}&workloadName=${encodeURIComponent(
    workloadName
  )}#kubernetes/applications`;

const podHref = (accountId: string, resourceId: string) =>
  `/kubernetes/podDetails/${resourceId}?PodDetails=${resourceId}&accountId=${accountId}#pod-details`;

const findWorkload = (candidates: Resource[], resourceId?: string) =>
  candidates.find((r) => r.id === resourceId && r.name && WORKLOAD_RESOURCE_TYPES.includes(r.type || ''));

/**
 * Whether the subject is worth rendering as a link, without resolving it. Used
 * only to decide the link styling; the destination still comes from
 * buildEventSubjectHref after the lookup.
 */
export function hasEventSubjectLink(row: EventRow | null | undefined): boolean {
  if (!row) return false;
  if (row.aggregation_key === AGGREGATION_KEY.ANOMALY) return true;
  if (!row.cloud_resource_id) return false;
  const subjectType = (row.subject_type || '').toLowerCase();
  return subjectType === SUBJECT_TYPE.POD || WORKLOAD_SUBJECT_TYPES.includes(subjectType);
}

/**
 * Destination for the event sidebar's "Where" subject link.
 *
 * The field shows the subject pod, so the link opens that pod. The event's own
 * `cloud_resource_id` cannot be used for it: api-server deliberately links a
 * pod-subject event to the pod's OWNING WORKLOAD (event/service.go
 * linkK8sCloudResource), falling back to the pod only when the owner is
 * unknown. The pod is therefore looked up by name and passed in here.
 *
 * `resources` is the candidate set from that lookup. When the pod row is gone,
 * the linked workload is used rather than dead-ending the click — never
 * `subject_owner`, which collectors sometimes report as the intermediate
 * ReplicaSet, a kind with no page in the product.
 */
export function buildEventSubjectHref(
  row: EventRow | null | undefined,
  resources: Resource[] | null | undefined,
  fallbackAccountId?: string
): string | null {
  if (!row) return null;
  const accountId = row.cloud_account_id || fallbackAccountId;
  if (!accountId) return null;
  const candidates = resources || [];

  if (row.subject_type === SUBJECT_TYPE.POD) {
    const pod = candidates.find((r) => r.type === RESOURCE_TYPE_POD && r.name === row.subject_name && r.id);
    if (pod?.id) {
      return podHref(accountId, pod.id);
    }
    const workload = findWorkload(candidates, row.cloud_resource_id);
    if (workload?.name) {
      return workloadHref(accountId, row.subject_namespace || '', workload.name);
    }
    return null;
  }

  // Workload subject (deployment / statefulset / daemonset / job): the event is
  // linked to that workload itself, so open its page.
  if (WORKLOAD_SUBJECT_TYPES.includes((row.subject_type || '').toLowerCase())) {
    const workload = findWorkload(candidates, row.cloud_resource_id);
    if (workload?.name) {
      return workloadHref(accountId, row.subject_namespace || '', workload.name);
    }
  }

  if (row.aggregation_key === AGGREGATION_KEY.ANOMALY && row.subject_name) {
    // Anomaly subjects are the workload itself (deployment / statefulset).
    return workloadHref(accountId, row.subject_namespace || '', row.subject_name);
  }

  return null;
}
