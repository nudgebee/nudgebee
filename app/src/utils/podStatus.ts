/**
 * The pod status a human recognises — what `kubectl get pods` prints.
 *
 * `k8s_pods.status` holds the pod PHASE, and the phase cannot express "running
 * but broken": a pod in CrashLoopBackOff has phase `Running`, one stuck pulling
 * an image has phase `Pending`. Showing the phase alone made a crashlooping pod
 * indistinguishable from a healthy one in the pods table.
 *
 * `container_status` is computed by the API from the container statuses the
 * agent reports, and is null when nothing is waiting or terminating — i.e. for a
 * healthy pod — so the phase remains the answer in the normal case. It is also
 * null for pods last reported by an agent too old to send container statuses,
 * which is why this falls back rather than showing an empty cell.
 */
export const podDisplayStatus = (pod: { status?: string | null; container_status?: string | null }): string =>
  pod.container_status || pod.status || '-';
