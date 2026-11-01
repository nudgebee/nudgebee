import { safeJSONParse } from 'src/utils/common';

// The facts about the alerting pod that the investigation panel shows: restarts, which containers
// are ready, why a container is waiting or how it last exited, and its memory against its limit.
//
// The panel was written against an evidence block named `pod_details`. The server now stores the
// pod itself under the `pod_enricher` action instead, so the panel's Restarts, Container and
// Previous-run sections rendered for nobody. This reads either shape into the one the panel uses.

// Evidence keys arrive snake_case or camelCase depending on which collector wrote them.
const pick = (obj, snake, camel) => (obj && typeof obj === 'object' ? obj[snake] ?? obj[camel] : undefined);

const parseData = (data) => (typeof data === 'string' ? safeJSONParse(data) : data);

const STATE_LABELS = { waiting: 'Waiting', running: 'Running', terminated: 'Terminated' };

// A container state ({waiting | running | terminated: {...}}) in the fields the panel shows.
// Undefined for an empty state, which is what a container that never ran before reports as its
// last state.
const describeState = (state) => {
  const key = Object.keys(STATE_LABELS).find((k) => state?.[k] && typeof state[k] === 'object');
  if (!key) return undefined;
  const described = { state: STATE_LABELS[key] };
  if (state[key].reason) described.reason = state[key].reason;
  const exitCode = pick(state[key], 'exit_code', 'exitCode');
  if (exitCode !== undefined && exitCode !== null) described.exitCode = exitCode;
  return described;
};

// The highest memory sample recorded for each container, in bytes, from the event's own metric
// evidence. A sampled peak can sit below the value at the kill: the kill happens between samples.
const sampledMemoryPeaks = (evidences) => {
  const peaks = {};
  (evidences || []).forEach((item) => {
    if (item?.type !== 'json') return;
    const block = parseData(item.data);
    if (block?.name !== 'pod_metric' || String(block?.resource_type ?? 'memory').toLowerCase() !== 'memory') return;
    (Array.isArray(block.data) ? block.data : []).forEach((series) => {
      const container = series?.metric?.container;
      const values = (series?.values || []).map((v) => parseFloat(v)).filter((v) => Number.isFinite(v));
      if (!container || values.length === 0) return;
      peaks[container] = Math.max(peaks[container] ?? 0, ...values);
    });
  });
  return peaks;
};

const fromPod = (pod, memoryPeaks) => {
  const statuses = pick(pod.status, 'container_statuses', 'containerStatuses');
  const specs = Array.isArray(pod.spec?.containers) ? pod.spec.containers : [];
  const containers = (Array.isArray(statuses) ? statuses : []).map((cs) => {
    const spec = specs.find((c) => c?.name === cs?.name);
    const resources = cs?.resources || spec?.resources || {};
    return {
      name: cs?.name,
      imageName: cs?.image || spec?.image,
      ready: cs?.ready === true,
      status: describeState(cs?.state) || {},
      lastStatus: describeState(pick(cs, 'last_state', 'lastState')),
      memoryLimit: resources.limits?.memory,
      memoryRequest: resources.requests?.memory,
      memoryPeakBytes: memoryPeaks[cs?.name],
    };
  });
  return {
    name: 'pod_details',
    data: {
      restarts: (Array.isArray(statuses) ? statuses : []).reduce((sum, cs) => sum + (Number(pick(cs, 'restart_count', 'restartCount')) || 0), 0),
      qosClass: pick(pod.status, 'qos_class', 'qosClass'),
      containersReady: { ready: containers.filter((c) => c.ready).length, total: containers.length },
      containers,
    },
  };
};

/**
 * The alerting pod's details in the shape the panel reads, from the event's evidence: the pod
 * recorded by `pod_enricher` mapped onto that shape, or, on an event stored before that existed,
 * its `pod_details` block as stored. Null when the event carries neither, or the pod was already
 * gone.
 *
 * These are the values recorded when the alert fired, not the pod's state now.
 *
 * @param {object[]} evidences the event's evidence list
 */
export const podDetailsFromEvidence = (evidences) => {
  const items = Array.isArray(evidences) ? evidences : [];
  for (const item of items) {
    if (item?.additional_info?.action_name !== 'pod_enricher') continue;
    const data = parseData(item.data);
    const pod = Array.isArray(data) ? data[0] : data;
    if (pod?.status && typeof pod.status === 'object') return fromPod(pod, sampledMemoryPeaks(items));
  }
  for (const item of items) {
    if (item?.type === 'json' && typeof item.data === 'string') {
      const block = safeJSONParse(item.data);
      if (block?.name === 'pod_details') return block;
    }
  }
  return null;
};

/**
 * A container's memory as one line: the sampled peak where the event recorded one, and the limit.
 * Empty when the container has neither.
 * @param {{memoryLimit?: string, memoryRequest?: string, memoryPeakBytes?: number}} container
 */
export const describeContainerMemory = (container) => {
  const parts = [];
  if (Number.isFinite(container?.memoryPeakBytes)) parts.push(`sampled peak ${Math.round(container.memoryPeakBytes / (1024 * 1024))} MiB`);
  if (container?.memoryLimit) parts.push(`limit ${container.memoryLimit}`);
  else if (container?.memoryRequest) parts.push(`no limit, request ${container.memoryRequest}`);
  return parts.join(' · ');
};
