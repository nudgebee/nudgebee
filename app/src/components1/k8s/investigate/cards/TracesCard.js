import apiTrace from '@api1/kubernetes/trace';
import KubernetesTracesListing from '@components1/k8s/details/KubernetesTracesListing';
import { formatDateForPlusMinusDuration } from 'src/utils/common';
import TracesBlueIcon from '@assets/ask-nudgebee/traces-blue-icon.svg';
import { KubernetesTraceServiceOperation } from '@components1/k8s/common/KubernetesTraceServiceOperation';
import { Box } from '@mui/material';

class TracesCard {
  constructor(evidenceData, event, index) {
    this.id = `TracesCard_${index}`;
    this.icon = TracesBlueIcon;
    this.text = 'Traces';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.alertLabelData = {};
    this.traceDataFromEvidence = {};
    this.evidenceData = evidenceData;
    this.event = event;
  }

  extractAlertLabels(evidenceData) {
    const filterTableType = evidenceData.filter((item) => item.type === 'table' && item.data.table_name.includes('Alert labels'));

    if (filterTableType?.length > 0) {
      const obj = {};
      filterTableType[0].data.rows.forEach(([key, value]) => {
        obj[key] = value;
      });
      this.alertLabelData = obj;
    }
  }

  extractTraceData(evidenceData) {
    if (evidenceData) {
      // data may be a JSON string (legacy) or already a parsed object (New Relic, etc.)
      const tracesData = typeof evidenceData.data === 'string' ? JSON.parse(evidenceData.data) : evidenceData.data;
      if (evidenceData.insight) {
        this.insightData = evidenceData.insight;
      }
      if (tracesData?.data?.length > 0) {
        this.traceDataFromEvidence = tracesData.data;
        return true;
      }
    }
    return false;
  }

  getWorkloadInfo(event) {
    const regex = /-?(\w{9,10})?-(\w{1}|(\w{5}))$/;

    let destinationWorkloadName;
    if (this.alertLabelData?.destination_workload_name) {
      destinationWorkloadName = this.alertLabelData.destination_workload_name;
    } else if (event?.subject_name !== 'Unresolved') {
      destinationWorkloadName = event?.subject_name.replace(regex, '');
    } else if (this.alertLabelData?.pod) {
      destinationWorkloadName = this.alertLabelData.pod.replace(regex, '');
    }

    const srcNamespace = this.alertLabelData?.src_workload_namespace ? [this.alertLabelData.src_workload_namespace] : [];
    const srcWorkload = this.alertLabelData?.src_workload_name ? [this.alertLabelData.src_workload_name] : [];
    const destinationWorkloadNamespace = this.alertLabelData?.destination_workload_namespace
      ? [this.alertLabelData.destination_workload_namespace]
      : event?.subject_namespace
      ? [event.subject_namespace]
      : [];
    const destinationWorkloadNames = destinationWorkloadName ? [destinationWorkloadName] : [];

    return {
      srcNamespace,
      srcWorkload,
      destinationWorkloadNamespace,
      destinationWorkloadNames,
      destinationWorkloadName,
    };
  }

  hasEnoughWorkloadData(workloadInfo) {
    const { srcNamespace, srcWorkload, destinationWorkloadNamespace, destinationWorkloadNames } = workloadInfo;

    return !(
      srcNamespace.length === 0 &&
      srcWorkload.length === 0 &&
      destinationWorkloadNamespace.length === 0 &&
      destinationWorkloadNames.length === 0
    );
  }

  async fetchTraceData(event, workloadInfo) {
    const { srcNamespace, srcWorkload, destinationWorkloadNamespace, destinationWorkloadNames } = workloadInfo;

    const timeRangeValue = formatDateForPlusMinusDuration(new Date(event?.starts_at + 'Z').getTime(), 10);

    try {
      const response = await apiTrace.traceV2({
        accountId: event?.cloud_account_id,
        namespace: srcNamespace,
        workload: srcWorkload,
        destinationNamespace: destinationWorkloadNamespace,
        destinationWorkload: destinationWorkloadNames,
        limit: 0,
        offset: 0,
        startDate: new Date(timeRangeValue.dateMinusMinutes).toISOString(),
        endDate: new Date(timeRangeValue.datePlusMinutes).toISOString(),
        onlyCount: true,
      });

      const traceCount = response?.traces_counts_v3?.count || 0;
      if (traceCount > 0) {
        this.insightData = [{ message: `${traceCount} Total Traces`, severity: 'Info' }];
        return true;
      }
    } catch (error) {
      console.error('Error fetching trace data:', error);
    }

    return false;
  }

  canRenderContent = async () => {
    if (this.extractTraceData(this.evidenceData)) {
      this.renderContent = true;
      return true;
    }

    this.extractAlertLabels(this.event.evidences);
    if (this.event?.subject_type !== 'pod') {
      return false;
    }
    const workloadInfo = this.getWorkloadInfo(this.event);
    if (!this.hasEnoughWorkloadData(workloadInfo)) {
      this.renderContent = false;
      return false;
    }
    this.renderContent = await this.fetchTraceData(this.event, workloadInfo);
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderTraceData()];
  };

  isTraceIdDifferent = (array) => {
    if (array.length === 0) {
      return false;
    }

    const firstTraceId = array[0].TraceId || array[0].trace_id;
    return !array.every((item) => (item.TraceId || item.trace_id) === firstTraceId);
  };

  mapTraceDataArray = (traceArray) => {
    let filteredTraceArray = traceArray;
    const filteredEbpfTraceArray =
      traceArray.length > 1 ? traceArray.filter((g) => g.SpanAttributes?.['otel.scope.name'] !== 'nudgebee-node-agent') : traceArray;
    if (filteredEbpfTraceArray.length > 0) {
      filteredTraceArray = filteredEbpfTraceArray;
    }

    return filteredTraceArray.map((trace) => ({
      resource_attributes: JSON.stringify(trace.ResourceAttributes || '{}'),
      span_name: trace.SpanName || trace.span_name || '',
      status_code: trace.StatusCode || trace.status_code || '',
      timestamp: trace.Timestamp || trace.timestamp,
      duration_ns: trace.Duration || trace.duration_ns,
      span_attributes: JSON.stringify(trace.SpanAttributes) || trace.spanattributes || '{}',
      account_id: this.event?.cloud_account_id,
      trace_id: trace.TraceId || trace.trace_id,
      span_id: trace.SpanId || trace.span_id,
      service_name: trace.ServiceName || trace.workload_name,
      events_attributes: trace['Events.Attributes'],
      events_name: trace['Events.Name'],
      parent_span_id: trace.ParentSpanId || trace.parent_span_id,
      span_kind: trace.SpanKind || trace.span_kind,
      status_message: trace.StatusMessage || trace.status_message || '',
    }));
  };

  mapDataToTraceTableData = (traceArray) => {
    return traceArray.map((item) => {
      const attrs = item.SpanAttributes || {};
      const resourceAttrs = item.ResourceAttributes || {};

      return {
        trace_id: item.TraceId || item.trace_id,
        span_id: item.SpanId || item.span_id,
        parent_span_id: item.ParentSpanId || item.parent_span_id,
        span_name: attrs['http.method'] || item.SpanName || item.span_name,
        workload_namespace: attrs['source.workload_namespace'] || item.workload_namespace || '',
        workload_name: attrs['source.workload_name'] || item.workload_name || '',
        destination_workload_name:
          attrs['destination.name'] ||
          attrs['destination.workload_name'] ||
          resourceAttrs['k8s.deployment.name'] ||
          item.destination_workload_name ||
          '',
        destination_workload_namespace:
          attrs['destination.namespace'] ||
          attrs['destination.workload_namespace'] ||
          resourceAttrs['k8s.namespace.name'] ||
          item.destination_workload_namespace ||
          '',
        destination_name: attrs['destination.name'] || resourceAttrs['service.name'] || item.destination_name || '',
        http_status_code: attrs['http.status_code'] || item.http_status_code || '',
        http_response: attrs['http.response'] || item.http_response || '',
        request_payload: attrs['http.request_payload'] || item.request_payload || '',
        headers: attrs['http.headers'] || item.headers || '',
        resource: attrs['http.url'] || attrs['db.statement'] || attrs['http.route'] || item.resource || '',
        duration_ns: item.Duration || item.duration_ns,
        timestamp: item.Timestamp || item.timestamp,
        status_code: item.StatusCode || item.status_code,
        trace_source: (attrs['otel.scope.name'] || item.status_code) === 'nudgebee-node-agent' ? 'ebpf' : 'otel',
        span_attributes: item.spanattributes || item.span_attributes || {},
      };
    });
  };

  renderTraceData = () => {
    if (this.traceDataFromEvidence.length > 0) {
      if (this.isTraceIdDifferent(this.traceDataFromEvidence)) {
        const traceTableData = this.mapDataToTraceTableData(this.traceDataFromEvidence);
        return (
          <Box sx={{ width: '100%', overflowX: 'auto' }}>
            <KubernetesTracesListing
              displaySideFilters={false}
              traceData={traceTableData}
              showNamespaceFilter={false}
              showTimeFilter={false}
              showWorkloadFilter={false}
              showStatusFilter={false}
              accountId={this.event?.cloud_account_id}
            />
          </Box>
        );
      }
      const traceData = this.mapTraceDataArray(this.traceDataFromEvidence);
      return (
        <Box sx={{ width: '100%', overflowX: 'auto' }}>
          <KubernetesTraceServiceOperation traceData={traceData} query={traceData[0]} />
        </Box>
      );
    }

    const regex = /-?(\w{9,10})?-(\w{1}|(\w{5}))$/;
    const timeRangeValue = formatDateForPlusMinusDuration(new Date(this.event?.starts_at + 'Z').getTime(), 10);

    let destinationWorkloadName;
    if (this.alertLabelData?.destination_workload_name) {
      destinationWorkloadName = this.alertLabelData.destination_workload_name;
    } else if (this.event?.subject_name !== 'Unresolved') {
      destinationWorkloadName = this.event.subject_name.replace(regex, '');
    } else if (this.alertLabelData?.pod) {
      destinationWorkloadName = this.alertLabelData.pod.replace(regex, '');
    }

    return (
      <Box sx={{ width: '100%', overflowX: 'auto' }}>
        <KubernetesTracesListing
          namespace={this.alertLabelData?.src_workload_namespace ?? null}
          workloadName={this.alertLabelData?.src_workload_name || null}
          destinationNamespace={this.alertLabelData?.destination_workload_namespace || this.event?.subject_namespace}
          destinationWorkload={destinationWorkloadName}
          showNamespaceFilter={false}
          showWorkloadFilter={false}
          showTimeFilter={false}
          passedSelectedTimestamp={{
            startTimestamp: timeRangeValue.dateMinusMinutes,
            endTimestamp: timeRangeValue.datePlusMinutes,
          }}
          accountId={this.event?.cloud_account_id}
        />
      </Box>
    );
  };
}

export default TracesCard;
