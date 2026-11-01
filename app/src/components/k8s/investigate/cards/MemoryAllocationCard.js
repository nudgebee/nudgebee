import InvestigationCharts from '@components/k8s/common/InvestigationChart';
import { formatMemory } from '@lib/formatter';
import KubernetesRightSizingUpdateForm from '@components/recommendations/KubernetesRightSizingUpdateForm';
import MemoryAllocationSummary from './MemoryAllocationSummary';
import PodAllocationSummary from './PodAllocationSummary';
import MemoryAllocationIcon from '@assets/investigation/resource-allocation.svg';
import RenderMemoryLeakData from './RenderMemoryLeakData';
import { safeJSONParse } from 'src/utils/common';
import { Box, Typography } from '@mui/material';
import { useMemo, useState } from 'react';
import { ds } from '@utils/colors';
import { mentionsContainer, summarizeAllocation } from './cardSummaries';

// Owner kinds as events store them (the Go agent writes them lowercase) → the
// k8s_workloads.kind spelling the workload lookup and the apply path match on.
const WORKLOAD_KINDS = {
  deployment: 'Deployment',
  statefulset: 'StatefulSet',
  daemonset: 'DaemonSet',
  job: 'Job',
  cronjob: 'CronJob',
  rollout: 'Rollout',
};

const OOM_TABLE_NAMES = ['*Pod and Node OOMKilled data*', 'Pod and Node OOMKilled data'];

// Share of the limit at which usage counts as resource pressure. Measured on
// dev over 7 days: 0.85 keeps 133 of 140 memory-reaching-limit alerts (their
// rule fires at ~85%) while dropping resize from every replica-mismatch,
// pod-not-ready and crash-loop event; 0.9 lost two thirds of the limit alerts.
const PRESSURE_RATIO = 0.85;

const peakOf = (values) => {
  const numbers = (values || []).map((v) => parseFloat(v)).filter((v) => Number.isFinite(v));
  return numbers.length ? Math.max(...numbers) : 0;
};

const nearLimit = (peak, limit) => Number(limit) > 0 && peak >= PRESSURE_RATIO * Number(limit);

const MEMORY_UNITS = { k: 1024, m: 1024 ** 2, g: 1024 ** 3, t: 1024 ** 4 };

// "<n><unit> request" / "<n><unit> limit" → bytes, or undefined when absent.
const parseMemoryQuantity = (text, which) => {
  const match = String(text).match(new RegExp(`([0-9.]+)\\s*([KMGT])(?:i|B)?\\s+${which}`, 'i'));
  if (!match) return undefined;
  const bytes = parseFloat(match[1]) * MEMORY_UNITS[match[2].toLowerCase()];
  return Number.isFinite(bytes) && bytes > 0 ? bytes : undefined;
};

// A real component, so the chosen container survives the parent's re-renders
// and the form's data only changes when the container (or the card's evidence)
// does — the form re-seeds its inputs whenever `data` changes.
const TuneResourceForm = ({ card, open, onClose }) => {
  const { workload, workloadType } = card.resolveWorkload();
  const containers = card.resolveContainers();
  const [selected, setSelected] = useState('');
  // CollapsableCard mounts this before the evidence is parsed, so the default is
  // re-derived rather than frozen into the initial state. A pick is kept even if
  // the evidence never named it: the form also offers the workload spec's
  // containers.
  const container = selected || containers[0] || '';
  const data = useMemo(
    () => card.buildResolveData(container, workload, workloadType),
    [card, container, workload, workloadType, card.memoryAllocationItem, card.podMemoryAllocationItem]
  );
  return (
    <KubernetesRightSizingUpdateForm
      open={open}
      onClose={onClose}
      onSuccess={onClose}
      onFailure={onClose}
      data={data}
      updateResourceType={'resourceChange'}
      recommendationSource='event'
      title={`Tune Resource Configuration - ${workload}`}
      containerOptions={containers}
      onContainerChange={setSelected}
      alertContainer={card.oomKilledContainer()}
    />
  );
};

// Kubernetes admits no request above its limit, so a current request above the
// current limit means the evidence counted it wrong (a second exporter publishing
// the same series doubled it). Propose from the limit rather than from that.
const requestWithinLimit = (request, limit) => (request > 0 && limit > 0 ? Math.min(request, limit) : request);

class MemoryAllocationCard {
  constructor() {
    this.id = 'MemoryAllocationCard';
    this.icon = MemoryAllocationIcon;
    this.text = 'Check if Resource allocation is sufficient';
    this.resolveButton = true;
    this.insightData = [];
    this.renderContent = false;
    this.metricGraphData = {};
    this.nodeGraphData = {};
    this.podGraphData = [];
    this.kubernetesRightSizingUpdateFormData = {};
    this.memoryAllocationItem = [];
    this.openKubernetesRightSizingUpdateForm = false;
    this.podMemoryAllocationItem = [];
    this.memLimit = [];
    // node_request_memory_metric draws on the node chart without a node_metric.
    this.nodeResourceType = 'memory';
    this.onDataUpdate = null;
    this.refreshRenderId = 0;
  }

  setDataUpdateCallback(callback) {
    this.onDataUpdate = callback;
    this.refreshRenderId += 1;
  }

  canRenderContent = async (evidenceData, troubleShootingEvent) => {
    let newobject3 = []; // For container_metric info data (limit, name, request)
    let newobject4 = []; // For pod_metric chart data (labels, data, container, oompoint)
    let newobject5 = []; // For pod_metric info data (limit, request, name)
    let newobject1 = []; // For container_metric chart data (labels, data)
    let newobject2 = {}; // For node_metric chart data (labels, data)
    let legacyPressure = false; // container_metric: usage reached the limit
    this.event = troubleShootingEvent;
    const parsedData = evidenceData;
    const row = troubleShootingEvent;
    const filteredData = parsedData.filter((item) => {
      if (item && item.type === 'json' && item.data && typeof item.data === 'string') {
        try {
          let parsedData = JSON.parse(item.data);
          if (
            parsedData.name === 'container_metric' ||
            parsedData.name === 'node_metric' ||
            parsedData.name === 'pod_metric' ||
            parsedData.name === 'node_request_memory_metric' ||
            parsedData.name === 'pod_node_metrics'
          ) {
            return true;
          }
        } catch {
          return false;
        }
      }
      return false;
    });
    if (filteredData.length === 0) {
      return false;
    }

    filteredData.forEach((item) => {
      const jsonData = JSON.parse(item.data);
      if (jsonData.name === 'container_metric') {
        for (const element of jsonData.data) {
          const containerMetricObject = {};
          const occurredAt = new Date(row?.starts_at + 'Z').toLocaleTimeString();
          const labelLength = jsonData.data[0].timestamps.length;
          const timeRange = jsonData.data[0].timestamps.map((d) => new Date(d * 1000).toLocaleTimeString());
          const insertIndex = timeRange.findIndex((time, index) => {
            const nextTime = timeRange[index + 1];
            return time < occurredAt && (nextTime === undefined || occurredAt < nextTime);
          });
          containerMetricObject.labels = element.timestamps.map((timestamp, i) => {
            const d = new Date(timestamp * 1000).toLocaleTimeString();
            if (i % 5 === 0 || i === labelLength - 1) {
              return d;
            }
            return '';
          });
          containerMetricObject.labels.splice(insertIndex, 0, occurredAt);
          containerMetricObject.data = element.values?.map((val) => val / (1024 * 1024));
          containerMetricObject.data.splice(insertIndex, 0, containerMetricObject.data[insertIndex + 1]);
          containerMetricObject.oomPointIndex = insertIndex;
          containerMetricObject.requestData = containerMetricObject.data.map(() => element.metric.requests?.memory / (1024 * 1024));
          newobject3.push({
            container: element.metric.container,
            request: element.metric.requests,
            limits: element.metric.limits,
          });
          newobject1.push(containerMetricObject);
          this.memLimit.push(element.metric.limits.memory / (1024 * 1024));
          legacyPressure = legacyPressure || nearLimit(peakOf(element.values), element.metric.limits?.memory);
        }
        this.memoryAllocationItem = newobject3;
        if (item?.insight && item.insight.length > 0) {
          this.insightData = this.insightData.concat(...item.insight);
        }
      } else if (jsonData.name === 'node_request_memory_metric') {
        if (jsonData.data?.length > 0) {
          const element = jsonData.data[0];
          const labelLength = element.timestamps.length;
          newobject2.requestData = element.values?.map((val) => parseFloat(formatMemory(val, 'bytes', 'gb', false)));
          newobject2.requestDataLabels = element.timestamps.map((timestamp, i) => {
            if (i % 5 === 0 || i === labelLength - 1) {
              return new Date(timestamp * 1000).toLocaleTimeString();
            }
            return '';
          });
        }
      } else if (jsonData.name === 'pod_metric') {
        if (jsonData.data.length > 0) {
          // Per blob: an event carries a cpu and a memory pod_metric, and one
          // must not relabel the other.
          const resourceType = String(jsonData?.resource_type ?? 'memory').toLowerCase();
          const occurredAt = new Date(row?.starts_at + 'Z').toLocaleTimeString();
          for (const element of jsonData.data) {
            const podMetricObject = {};
            const labelLength = element.timestamps.length;
            const timeRange = element.timestamps.map((d) => new Date(d * 1000).toLocaleTimeString());
            const insertIndex = timeRange.findIndex((time, index) => {
              const nextTime = timeRange[index + 1];
              return time < occurredAt && (nextTime === undefined || occurredAt < nextTime);
            });
            podMetricObject.labels = element.timestamps.map((timestamp, i) => {
              const d = new Date(timestamp * 1000).toLocaleTimeString();
              if (i % 5 === 0 || i === labelLength - 1) {
                return d;
              }
              return '';
            });
            podMetricObject.labels.splice(insertIndex, 0, occurredAt);
            // Bytes → MiB and cores → millicores, unrounded: a sub-unit value is
            // not zero.
            let values = element.values;
            if (resourceType === 'memory') {
              values = element.values?.map((val) => val / (1024 * 1024));
            } else if (resourceType === 'cpu') {
              values = element.values?.map((val) => val * 1000);
            }
            podMetricObject.data = values;
            podMetricObject.data.splice(insertIndex, 0, podMetricObject.data[insertIndex + 1]);
            podMetricObject.oomPointIndex = insertIndex;
            podMetricObject.container = element.metric.container;
            podMetricObject.pod = element.metric.pod;
            newobject4.push(podMetricObject);
            // Peak observed usage in source units (bytes for memory, cores for cpu).
            // Used as recommendation base when the pod has no requests/limits set,
            // so the Tune Resource popup can prefill values from actual usage.
            const numericValues = (element.values || []).map((v) => parseFloat(v)).filter((v) => Number.isFinite(v));
            const peakUsage = peakOf(numericValues);
            newobject5.push({
              pod: element.metric.pod,
              container: element.metric.container,
              request: element.metric?.requests?.memory,
              limits: element.metric?.limits?.memory,
              cpu_limit: element.metric?.limits?.cpu,
              cpu_request: element.metric?.requests?.cpu,
              resource_type: resourceType,
              peakUsage,
              // The raw series, for the tune form's "what it actually used" view.
              samples: numericValues,
              sampleTimes: element.timestamps,
            });
          }
        }
        this.podMemoryAllocationItem = newobject5;
        if (item?.insight && item.insight.length > 0) {
          this.insightData = this.insightData.concat(...item.insight);
        }
      } else if (jsonData.name === 'pod_node_metrics') {
        // The exact pod, all containers summed. Only used where a container has
        // no series of its own.
        const usage = jsonData.data?.memory_usage?.series_list_result?.[0];
        const limit = jsonData.data?.memory_limit?.series_list_result?.[0];
        if (usage?.values?.length) {
          this.podMemoryUsage = {
            samples: usage.values.map((v) => parseFloat(v)).filter((v) => Number.isFinite(v)),
            times: usage.timestamps || [],
            limit: parseFloat((limit?.values || []).at(-1)),
          };
        }
      } else if (jsonData.name === 'node_metric') {
        // Stored as "Memory" / "CPU" as well as lowercase.
        this.nodeResourceType = String(jsonData?.resource_type ?? 'memory').toLowerCase();
        if (jsonData?.data[0]?.timestamps?.length > 0) {
          const labelLength = jsonData.data[0].timestamps.length;
          newobject2.labels = jsonData.data[0].timestamps.map((timestamp, i) => {
            if (i % 5 === 0 || i === labelLength - 1) {
              let d = new Date(timestamp * 1000);
              return d.toLocaleTimeString();
            }
            return '';
          });
          // Memory arrives in bytes and is drawn in GiB; CPU arrives in cores.
          // (Not formatMemory: its "1,536.5" string parses back as 1.)
          newobject2.data = jsonData.data[0].values?.map((val) => (this.nodeResourceType === 'cpu' ? parseFloat(val) : parseFloat(val) / 1024 ** 3));
        }
        if (item?.insight && item.insight.length > 0) {
          this.insightData = this.insightData.concat(...item.insight);
        }
      }
    });
    // The killed container can be missing from the usage evidence entirely: on
    // #38638 it was the metrics backend itself, so nothing about it could be
    // queried. The OOMKilled table still names it and its allocation — list it
    // rather than show only the containers that survived.
    const killed = this.oomKilledContainer();
    if (killed && newobject4.length > 0 && !newobject5.some((item) => item.container === killed)) {
      const allocation = this.oomKilledAllocation();
      const pod = newobject5[0]?.pod;
      newobject4.push({ container: killed, pod, data: [], labels: [], noSamples: true });
      newobject5.push({
        pod,
        container: killed,
        request: allocation.request,
        limits: allocation.limit,
        resource_type: 'memory',
        peakUsage: 0,
        noSamples: true,
      });
    }
    if (newobject1?.data?.length > 0 || newobject2?.data?.length > 0 || newobject4?.length > 0) {
      this.renderContent = true;
    }
    if (this.memoryAllocationItem?.length == 0 && this.podMemoryAllocationItem?.length == 0) {
      this.resolveButton = false;
    } else {
      // Resizing is offered as a fix only when the evidence says resources were
      // the problem. The card renders on every pod event with metrics (API
      // failures, replica mismatches, crash loops…), and on those a resize is a
      // distraction from the real fix. Decided on the signal, not the alert name:
      // alert names are whatever each customer's rules call them.
      this.resolveButton = Boolean(
        killed ||
          legacyPressure ||
          newobject5.some((i) => (i.resource_type === 'cpu' ? nearLimit(i.peakUsage, i.cpu_limit) : nearLimit(i.peakUsage, i.limits))) ||
          nearLimit(peakOf(this.podMemoryUsage?.samples), this.podMemoryUsage?.limit)
      );
    }
    this.metricGraphData = newobject1;
    this.nodeGraphData = newobject2;
    this.podGraphData = newobject4;

    if (this.onDataUpdate && typeof this.onDataUpdate === 'function') {
      this.onDataUpdate(this);
    }
    return this.renderContent;
  };

  // The stored summary when the evidence has one, otherwise the first container's peak against its
  // limit. When a container was killed and the stored summary is about another one, the killed
  // container's line leads: on #38638 the summary described a sidecar while the container that
  // died had no samples at all.
  getHighLightsData = () => {
    const stored = this.insightData?.length > 0 ? this.insightData : [];
    const killed = this.oomKilledContainer();
    if (stored.length > 0 && (!killed || stored.some((line) => mentionsContainer(line, killed)))) return stored;
    const items = killed ? (this.podMemoryAllocationItem || []).filter((i) => i?.container === killed) : this.podMemoryAllocationItem;
    return [...summarizeAllocation(items), ...stored];
  };

  // The workload behind the event's pod. The event's own owner fields are the
  // resolved answer; the service_key / pod-name heuristics below only run for
  // events that carry none (older collectors, webhook alerts).
  resolveWorkload = () => {
    const ownerKind = WORKLOAD_KINDS[this.event?.subject_owner_kind?.toLowerCase()];
    if (this.event?.subject_owner && ownerKind) {
      return { workload: this.event.subject_owner, workloadType: ownerKind };
    }

    let workload, workloadType;
    if (this.event.subject_type === 'pod') {
      let serviceKeys = this.event.service_key?.split('/');
      workload = serviceKeys?.[2];
      workloadType = serviceKeys?.[1];
    }

    if (!workload) {
      for (let e of this.event.evidences) {
        if (e.type === 'json' && typeof e.data === 'string') {
          let jsonData = safeJSONParse(e.data);
          if (jsonData) {
            if (jsonData.name === 'noisy_neighbours') {
              for (let n of jsonData.data.neighbours) {
                if (n.pod_name === this.event.subject_name && n.namespace === this.event.subject_namespace) {
                  let kind = n.kind?.[0];
                  if (kind) {
                    workload = kind.name;
                    workloadType = kind.kind;
                  }
                  break;
                }
              }
            }
          }
        }
      }
    }

    if (!workload || workloadType === 'ReplicaSet') {
      let workloadSplit = this.event.subject_name?.split('-');
      workload = workloadSplit.slice(0, workloadSplit.length - 2).join('-');
      workloadType = 'Deployment';
    }

    return { workload: workload || this.event.subject_name, workloadType };
  };

  // The container the OOMKilled table names — the one the alert is about.
  // Both spellings of the table name are in stored evidence.
  oomKilledContainer = () => {
    for (let e of this.event?.evidences || []) {
      if (e.type === 'table' && OOM_TABLE_NAMES.includes(e.data?.table_name)) {
        for (let r of e.data.rows || []) {
          if (r[0] === 'Container name' && r[1]) {
            return r[1];
          }
        }
      }
    }
    return '';
  };

  // The killed container's request/limit from the OOMKilled table, in bytes:
  // "2048MB request, 8192MB limit" (MB here is MiB) or "2Gi request, 8Gi limit".
  oomKilledAllocation = () => {
    for (let e of this.event?.evidences || []) {
      if (e.type === 'table' && OOM_TABLE_NAMES.includes(e.data?.table_name)) {
        const row = (e.data.rows || []).find((r) => r[0] === 'Container memory');
        if (row?.[1]) {
          return { request: parseMemoryQuantity(row[1], 'request'), limit: parseMemoryQuantity(row[1], 'limit') };
        }
      }
    }
    return {};
  };

  // Every container the evidence knows about, the alerting one first.
  resolveContainers = () => {
    const names = [];
    const add = (name) => {
      if (name && !names.includes(name)) names.push(name);
    };
    add(this.oomKilledContainer());
    add(this.event?.labels?.container);
    this.memoryAllocationItem.forEach((item) => add(item.container));
    this.podMemoryAllocationItem.forEach((item) => add(item.container));
    return names;
  };

  // The tune form's data for one container. Values from a different container
  // must never stand in: a sidecar's 20 MiB proposed for the container that was
  // OOMKilled at 8 GiB is the failure this guards. With nothing for the
  // container, the form reads current values off the workload spec instead.
  buildResolveData = (container, workload, workloadType) => {
    const namespace = this.event?.subject_namespace;
    // Entries without a container label are the pod as a whole.
    const forContainer = (item) => !item.container || !container || item.container === container;
    const base = {
      id: this.event.id,
      accountId: this.event?.cloud_account_id,
      card_id: this.id,
      container_name: container,
    };

    const containerItem = this.memoryAllocationItem.find(forContainer);
    if (containerItem) {
      return {
        ...base,
        cloud_resourse: {
          meta: {
            namespace: namespace,
            controller: workload,
            controllerKind: workloadType,
            container: container,
            name: this.event.subject_name,
          },
        },
        memory: {
          request: formatMemory(requestWithinLimit(containerItem?.request?.memory, containerItem?.limits?.memory) * 1.1, 'bytes', 'mb')?.replace(
            ',',
            ''
          ),
          limit: formatMemory(containerItem?.limits?.memory * 1.1, 'bytes', 'mb')?.replace(',', ''),
          oldRequest: formatMemory(containerItem?.request?.memory, 'bytes', 'mb')?.replace(',', ''),
          oldLimit: formatMemory(containerItem?.limits?.memory, 'bytes', 'mb')?.replace(',', ''),
        },
      };
    }

    const data = {
      ...base,
      cloud_resourse: {
        meta: {
          namespace: namespace,
          controller: workload,
          container: container,
          controllerKind: workloadType,
          name: '',
        },
      },
    };
    const podItems = this.podMemoryAllocationItem.filter(forContainer);
    // Ignoring CPU resource_type Because KubernetesRightSizingPopupForm require CPU info like 99%, 97%
    const memoryItems = podItems.filter((g) => g.resource_type === 'memory');
    const cpuItems = podItems.filter((g) => g.resource_type === 'cpu');
    // A pod_metric blob tagged resource_type 'cpu' still carries the container's
    // memory requests/limits in element.metric, so fall back to any entry that
    // has memory request/limit set. Without this, a CPU-typed event hides the
    // Memory card even when the pod has memory requests/limits configured.
    const memoryObject = memoryItems[0] || podItems.find((g) => g.request != null || g.limits != null);
    const cpuObject = cpuItems[0];
    // When the pod has no requests/limits set, fall back to the highest observed
    // usage of this container so the recommendation reflects real consumption
    // rather than zero × buffer.
    const memPeak = memoryItems.reduce((m, g) => Math.max(m, g.peakUsage || 0), 0);
    const cpuPeak = cpuItems.reduce((m, g) => Math.max(m, g.peakUsage || 0), 0);
    // formatMemory(0) returns '-' (formatNumber treats 0 as no-value), which the
    // form then parseFloat()s into NaN and renders blank. Format the raw MB number
    // for zero current values so the Current column shows "0" instead of empty.
    const formatMem = (bytes) => {
      if (!bytes) return 0;
      return formatMemory(bytes, 'bytes', 'mb')?.replace(',', '');
    };
    // Only a recorded request can be a miscount; a peak stands in for a request that
    // was never set and is real usage, so it is left as measured.
    const reqBase = memoryObject?.request > 0 ? requestWithinLimit(memoryObject.request, memoryObject?.limits) : memPeak;
    // Never propose a limit below the request: with no limit set, the peak can sit
    // under a generous request, and Kubernetes refuses that pair.
    const limitBase = Math.max(memoryObject?.limits > 0 ? memoryObject.limits : memPeak, reqBase);
    // Always present, so the memory section renders for a container the metrics
    // missed; the form then takes current values from the workload spec.
    // nbalgoBase is the unbuffered MB base the popup's algo/buffer toggles
    // multiply against. When current request/limit are zero, the form's old
    // `parsedOldRequest || ''` path collapses to '' and toggles clear the
    // input; nbalgoBase lets the form prefer the usage-derived value.
    // Leave Recommended blank when there's no signal (current = 0 AND no
    // observed peak). Pushing a 0 recommendation through would land an
    // invalid spec on the workload.
    // What the container used, for the form to show next to its numbers. A
    // container with no series of its own falls back to the pod total, and says so.
    const sampled = memoryItems.find((g) => g.samples?.length);
    let usage;
    if (sampled) {
      usage = { samples: sampled.samples, times: sampled.sampleTimes || [], scope: 'container' };
    } else if (this.podMemoryUsage?.samples?.length) {
      usage = { samples: this.podMemoryUsage.samples, times: this.podMemoryUsage.times, scope: 'pod' };
    }
    data['memory'] = {
      usage,
      request: reqBase > 0 ? formatMem(reqBase * 1.1) : undefined,
      limit: limitBase > 0 ? formatMem(limitBase * 1.1) : undefined,
      oldRequest: formatMem(memoryObject?.request),
      oldLimit: formatMem(memoryObject?.limits),
      nbalgoBase: memPeak > 0 ? memPeak / (1024 * 1024) : undefined,
    };
    if (cpuObject) {
      const cpuReqBase = cpuObject.cpu_request > 0 ? cpuObject.cpu_request : cpuPeak;
      data['cpu'] = {
        request: cpuReqBase > 0 ? Number(cpuReqBase).toFixed(4) : undefined,
        limit: undefined,
        oldRequest: cpuObject.cpu_request || 0,
        // The evidence carries the container's CPU limit; dropping it made the
        // Current column claim the workload has none. (Recommended `limit`
        // stays undefined on purpose — the form advises no CPU limit.)
        oldLimit: cpuObject.cpu_limit,
        nbalgoBase: cpuPeak > 0 ? cpuPeak : undefined,
      };
    }
    return data;
  };

  ResolveMemoryComponent = (props) => {
    if (this.memoryAllocationItem.length === 0 && this.podMemoryAllocationItem.length === 0) {
      return undefined;
    }
    return <TuneResourceForm card={this} open={props.open} onClose={props.onCloseComponent} />;
  };

  getResolveComponent = () => {
    return this.ResolveMemoryComponent;
  };

  getContentComponents = () => {
    const components = [];
    let chartsAdded = false;
    if (this.podMemoryAllocationItem.length > 0) {
      chartsAdded = true;
      const podItems = this.podMemoryAllocationItem;
      const podGraphData = this.podGraphData;
      const nodeGraphData = this.nodeGraphData;
      const nodeResourceType = this.nodeResourceType;

      // Group cards by resource_type so a row never mixes CPU with Memory.
      // Each group is a flex-wrap row where every card has flex: 1 1 420px:
      //   - 1 card in a row -> grows to fill the entire row
      //   - 2+ cards in a row -> share the row equally
      //   - Card never shrinks below ~420px before wrapping to the next line
      // Flexbox is used (not CSS grid auto-fit) because grid locks track
      // count by viewport width, leaving an orphan stuck at 1/N width.
      const groupedPods = podItems.reduce((acc, pod, originalIndex) => {
        const type = pod.resource_type || 'memory';
        if (!acc[type]) acc[type] = [];
        acc[type].push({ pod, originalIndex });
        return acc;
      }, {});
      const groupOrder = ['memory', 'cpu'];

      components.push(() => (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3, mt: 2 }}>
          {groupOrder
            .filter((type) => groupedPods[type]?.length > 0)
            .map((type) => (
              <Box
                key={type}
                sx={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  gap: 2,
                }}
              >
                {groupedPods[type].map(({ pod, originalIndex }) => (
                  <Box
                    key={originalIndex}
                    sx={{
                      flex: '1 1 420px',
                      minWidth: 0,
                      display: 'flex',
                      flexDirection: 'column',
                    }}
                  >
                    <PodAllocationSummary podMemoryAllocationItem={pod} />
                    {pod.noSamples ? (
                      <Typography sx={{ mt: ds.space[4], fontSize: ds.text.body, color: ds.gray[600] }}>
                        No usage samples were recorded for this container around the event.
                      </Typography>
                    ) : (
                      <>
                        {/*
                         * Pod cards render only the pod chart (data=[] disables the container chart),
                         * so container-only props (memLimit, dataRequest) are intentionally omitted.
                         *
                         * `occurredAt` must come from podGraphData[originalIndex].oomPointIndex, NOT
                         * metricGraphData.oomPointIndex: metricGraphData is an ARRAY of container
                         * metric objects, so reading `.oomPointIndex` off the array itself yields
                         * undefined. The OOM marker index lives on each per-pod entry.
                         */}
                        <InvestigationCharts
                          data={[]}
                          labels={[]}
                          dataN={nodeGraphData?.data}
                          labelsN={nodeGraphData?.labels}
                          dataRequestN={nodeGraphData?.requestData}
                          dataRequestL={nodeGraphData?.requestDataLabels}
                          occurredAt={podGraphData[originalIndex]?.oomPointIndex}
                          dataP={podGraphData[originalIndex].data}
                          labelsP={podGraphData[originalIndex].labels}
                          podLimitRequest={pod}
                          resourceType={pod.resource_type}
                          nodeResourceType={nodeResourceType}
                        />
                      </>
                    )}
                  </Box>
                ))}
              </Box>
            ))}
        </Box>
      ));
    }
    if (this.memoryAllocationItem.length > 0) {
      chartsAdded = true;
      this.memoryAllocationItem.forEach((p, index) => {
        components.push(
          () => {
            return <MemoryAllocationSummary memoryAllocationItem={p} />;
          },
          () => (
            <InvestigationCharts
              data={this.metricGraphData[index].data}
              labels={this.metricGraphData[index].labels}
              dataN={this.nodeGraphData?.data}
              labelsN={this.nodeGraphData?.labels}
              dataRequest={this.metricGraphData[index].requestData}
              dataRequestN={this.nodeGraphData?.requestData}
              dataRequestL={this.nodeGraphData?.requestDataLabels}
              memLimit={this.memLimit[index]}
              occurredAt={this.metricGraphData[index].oomPointIndex}
              dataP={[]}
              labelsP={[]}
              podLimitRequest={this.podMemoryAllocationItem}
              nodeResourceType={this.nodeResourceType}
            />
          )
        );
      });
    }
    if (!chartsAdded && this.nodeGraphData?.data?.length > 0) {
      components.push(() => (
        <InvestigationCharts
          data={[]}
          labels={[]}
          dataP={[]}
          labelsP={[]}
          dataRequest={undefined}
          memLimit={[]}
          occurredAt={undefined}
          podLimitRequest={[]}
          dataN={this.nodeGraphData?.data}
          labelsN={this.nodeGraphData?.labels}
          dataRequestN={this.nodeGraphData?.requestData}
          dataRequestL={this.nodeGraphData?.requestDataLabels}
          nodeResourceType={this.nodeResourceType}
        />
      ));
    }
    // Whenever memory is charted — not whichever evidence happened to be read last.
    const chartsMemory =
      this.memoryAllocationItem.length > 0 ||
      this.podMemoryAllocationItem.some((item) => item.resource_type === 'memory') ||
      (this.nodeGraphData?.data?.length > 0 && this.nodeResourceType === 'memory');
    if (chartsMemory) {
      components.push(() => <RenderMemoryLeakData />);
    }
    return components;
  };
}

export default MemoryAllocationCard;
