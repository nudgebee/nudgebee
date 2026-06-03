import InvestigationCharts from '@components1/k8s/common/InvestigationChart';
import { formatMemory } from '@lib/formatter';
import KubernetesRightSizingUpdateForm from '@components1/recommendations/KubernetesRightSizingUpdateForm';
import MemoryAllocationSummary from './MemoryAllocationSummary';
import PodAllocationSummary from './PodAllocationSummary';
import MemoryAllocationIcon from '@assets/investigation/resource-allocation.svg';
import RenderMemoryLeakData from './RenderMemoryLeakData';
import { safeJSONParse } from 'src/utils/common';
import { Box } from '@mui/material';

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
    this.resourceType = '';
    this.containerName = '';
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
            parsedData.name === 'node_request_memory_metric'
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
          this.containerName = element.metric.container;
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
          this.resourceType = jsonData?.resource_type ?? 'memory';
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
            let values = element.values;
            if (this.resourceType === 'memory') {
              values = element.values?.map((val) => (val / (1024 * 1024)).toFixed());
            } else if (this.resourceType === 'cpu') {
              values = element.values?.map((val) => (val * 1000).toFixed());
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
            const peakUsage = numericValues.length ? Math.max(...numericValues) : 0;
            newobject5.push({
              pod: element.metric.pod,
              container: element.metric.container,
              request: element.metric?.requests?.memory,
              limits: element.metric?.limits?.memory,
              cpu_limit: element.metric?.limits?.cpu,
              cpu_request: element.metric?.requests?.cpu,
              resource_type: this.resourceType,
              peakUsage,
            });
            this.containerName = element.metric.container;
          }
        }
        this.podMemoryAllocationItem = newobject5;
        if (item?.insight && item.insight.length > 0) {
          this.insightData = this.insightData.concat(...item.insight);
        }
      } else if (jsonData.name === 'node_metric') {
        this.resourceType = jsonData?.resource_type ?? 'memory';
        if (jsonData?.data[0]?.timestamps?.length > 0) {
          const labelLength = jsonData.data[0].timestamps.length;
          newobject2.labels = jsonData.data[0].timestamps.map((timestamp, i) => {
            if (i % 5 === 0 || i === labelLength - 1) {
              let d = new Date(timestamp * 1000);
              return d.toLocaleTimeString();
            }
            return '';
          });
          newobject2.data = jsonData.data[0].values?.map((val) => parseFloat(formatMemory(val, 'bytes', 'gb', false)));
        }
        if (item?.insight && item.insight.length > 0) {
          this.insightData = this.insightData.concat(...item.insight);
        }
      }
    });
    if (newobject1?.data?.length > 0 || newobject2?.data?.length > 0 || newobject4?.length > 0) {
      this.renderContent = true;
    }
    if (this.memoryAllocationItem?.length == 0 && this.podMemoryAllocationItem?.length == 0) {
      this.resolveButton = false;
    }
    this.metricGraphData = newobject1;
    this.nodeGraphData = newobject2;
    this.podGraphData = newobject4;

    if (this.onDataUpdate && typeof this.onDataUpdate === 'function') {
      this.onDataUpdate(this);
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  ResolveMemoryComponent = (props) => {
    let data = {};
    let namespace = this.event?.subject_namespace,
      workload,
      workloadType,
      container = '';

    if (this.event.subject_type === 'pod') {
      let serviceKeys = this.event.service_key?.split('/');
      workload = serviceKeys[2];
      workloadType = serviceKeys[1];
    }

    if (!workload) {
      for (let e of this.event.evidences) {
        if (e.type === 'json' && typeof e.data === 'string') {
          let jsonData = safeJSONParse(e.data);
          if (jsonData) {
            if (jsonData.name === 'noisy_neighbours') {
              for (let n of jsonData.data.neighbours) {
                if (n.pod_name === this.event.subject_name && n.namespace === this.event.subject_namespace) {
                  let kind = n.kind[0];
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

    for (let e of this.event.evidences) {
      if (e.type === 'table' && e.data.table_name === '*Pod and Node OOMKilled data*') {
        for (let r of e.data.rows) {
          if (r[0] === 'Container name') {
            container = r[1];
          }
        }
      }
    }

    workload = workload || this.event.subject_name;
    if (this.memoryAllocationItem.length > 0) {
      data = {
        id: this.event.id,
        accountId: this.event?.cloud_account_id,
        card_id: this.id,
        container_name: container || this.containerName,
        cloud_resourse: {
          meta: {
            namespace: namespace,
            controller: workload,
            controllerKind: workloadType,
            container: container || this.containerName,
            name: this.event.subject_name,
          },
        },
        memory: {
          request: formatMemory(this.memoryAllocationItem[0]?.request?.memory * 1.1, 'bytes', 'mb')?.replace(',', ''),
          limit: formatMemory(this.memoryAllocationItem[0]?.limits?.memory * 1.1, 'bytes', 'mb')?.replace(',', ''),
          oldRequest: formatMemory(this.memoryAllocationItem[0]?.request?.memory, 'bytes', 'mb')?.replace(',', ''),
          oldLimit: formatMemory(this.memoryAllocationItem[0]?.limits?.memory, 'bytes', 'mb')?.replace(',', ''),
        },
      };
    } else if (this.podMemoryAllocationItem.length > 0) {
      data = {
        id: this.event.id,
        accountId: this.event?.cloud_account_id,
        card_id: this.id,
        container_name: container || this.containerName,
        cloud_resourse: {
          meta: {
            namespace: namespace,
            controller: workload,
            container: container || this.containerName,
            controllerKind: workloadType,
            name: '',
          },
        },
      };
      // Ignoring CPU resource_type Because KubernetesRightSizingPopupForm require CPU info like 99%, 97%
      const memoryItems = this.podMemoryAllocationItem.filter((g) => g.resource_type === 'memory');
      const cpuItems = this.podMemoryAllocationItem.filter((g) => g.resource_type === 'cpu');
      const memoryObject = memoryItems[0];
      const cpuObject = cpuItems[0];
      // When the pod has no requests/limits set, fall back to the highest observed
      // usage across pods of the same resource_type so the recommendation reflects
      // real consumption rather than zero × buffer.
      const memPeak = memoryItems.reduce((m, g) => Math.max(m, g.peakUsage || 0), 0);
      const cpuPeak = cpuItems.reduce((m, g) => Math.max(m, g.peakUsage || 0), 0);
      // formatMemory(0) returns '-' (formatNumber treats 0 as no-value), which the
      // form then parseFloat()s into NaN and renders blank. Format the raw MB number
      // for zero current values so the Current column shows "0" instead of empty.
      const formatMem = (bytes) => {
        if (!bytes) return 0;
        return formatMemory(bytes, 'bytes', 'mb')?.replace(',', '');
      };
      if (memoryObject) {
        const reqBase = memoryObject.request > 0 ? memoryObject.request : memPeak;
        const limitBase = memoryObject.limits > 0 ? memoryObject.limits : memPeak;
        // nbalgoBase is the unbuffered MB base the popup's algo/buffer toggles
        // multiply against. When current request/limit are zero, the form's old
        // `parsedOldRequest || ''` path collapses to '' and toggles clear the
        // input; nbalgoBase lets the form prefer the usage-derived value.
        const nbalgoBaseMB = memPeak > 0 ? memPeak / (1024 * 1024) : undefined;
        // Leave Recommended blank when there's no signal (current = 0 AND no
        // observed peak). Pushing a 0 recommendation through would land an
        // invalid spec on the workload.
        data['memory'] = {
          request: reqBase > 0 ? formatMem(reqBase * 1.1) : undefined,
          limit: limitBase > 0 ? formatMem(limitBase * 1.1) : undefined,
          oldRequest: formatMem(memoryObject.request),
          oldLimit: formatMem(memoryObject.limits),
          nbalgoBase: nbalgoBaseMB,
        };
      }
      if (cpuObject) {
        const reqBase = cpuObject.cpu_request > 0 ? cpuObject.cpu_request : cpuPeak;
        data['cpu'] = {
          request: reqBase > 0 ? reqBase : undefined,
          limit: undefined,
          oldRequest: cpuObject.cpu_request || 0,
          oldLimit: undefined,
          nbalgoBase: cpuPeak > 0 ? cpuPeak : undefined,
        };
      }
    }
    if (data && Object.keys(data).length) {
      return (
        <KubernetesRightSizingUpdateForm
          open={props.open}
          onClose={props.onCloseComponent}
          onSuccess={props.onCloseComponent}
          onFailure={props.onCloseComponent}
          data={data}
          updateResourceType={'resourceChange'}
          recommendationSource='event'
          title={`Tune Resource Configuration - ${workload}`}
        />
      );
    }
    return undefined;
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
                    />
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
              resourceType={this.resourceType}
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
          resourceType={this.resourceType}
        />
      ));
    }
    if (this.resourceType === 'memory') {
      components.push(() => <RenderMemoryLeakData />);
    }
    return components;
  };
}

export default MemoryAllocationCard;
