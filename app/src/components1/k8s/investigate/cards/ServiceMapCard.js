import KubernetesServiceMapWrapper from '@components1/k8s/details/KubernetesServiceMap';
import { formatDateForPlusMinusDuration } from 'src/utils/common';
import ServiceMapIcon from '@assets/kubernetes/monitoring/service-map-icon.icon.svg';
import apiKubernetes from '@api1/kubernetes';
import { formatDate } from '@lib/formatter';

class ServiceMapCard {
  constructor(evidenceData, event, index) {
    this.id = `ServiceMapCard_${index}`;
    this.icon = ServiceMapIcon;
    this.text = 'Service Map';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.impactedAPIData = {};
    this.regex = /-?(\w{9,10})?-(\w{1}|(\w{5}))$/;
    this.serviceMapDataFromEvidence = [];
    this.cleanup = null;
    this.alertLabelData = {}; // Initialize to prevent undefined access
    this.event = event; // Initialize to prevent undefined access
    this.evidenceData = evidenceData;
  }

  extractAlertLabels(evidenceData) {
    if (!Array.isArray(evidenceData)) {
      console.warn('extractAlertLabels: evidenceData is not an array');
      this.alertLabelData = {};
      return;
    }

    const alertLabelsTable = evidenceData.find((item) => item?.type === 'table' && item?.data?.table_name?.includes('Alert labels'));

    if (alertLabelsTable?.data?.rows) {
      this.alertLabelData = alertLabelsTable.data.rows.reduce((obj, row) => {
        // Ensure row is an array with at least 2 elements
        if (Array.isArray(row) && row.length >= 2) {
          const [key, value] = row;
          if (key != null) {
            // Check for null/undefined keys
            obj[key] = value;
          }
        }
        return obj;
      }, {});
    } else {
      this.alertLabelData = {};
    }
  }

  getSourceWorkloadName() {
    // Check event.subject_owner first
    if (this.event?.subject_owner) {
      return this.event.subject_owner;
    }

    // Check alertLabelData.src_workload_name
    if (this.alertLabelData?.src_workload_name) {
      return this.alertLabelData.src_workload_name;
    }

    // Check alertLabelData.pod
    if (this.alertLabelData?.pod) {
      try {
        return this.alertLabelData.pod.replace(this.regex, '');
      } catch (error) {
        console.error('Error processing pod name with regex:', error);
        return this.alertLabelData.pod; // Return original if regex fails
      }
    }

    // Check event.subject_name
    if (this.event?.subject_name && this.event.subject_name !== 'Unresolved') {
      try {
        return this.event.subject_name.replace(this.regex, '');
      } catch (error) {
        console.error('Error processing subject_name with regex:', error);
        return this.event.subject_name; // Return original if regex fails
      }
    }

    return '';
  }

  async executeAlternativeLogic() {
    try {
      // Check if event is pod-related
      if (this.event?.subject_type !== 'pod') {
        return false;
      }

      const sourceWorkloadName = this.getSourceWorkloadName();

      // Validate required data
      if (!this.event?.subject_namespace || !sourceWorkloadName) {
        console.warn('executeAlternativeLogic: Missing required namespace or workload name');
        return false;
      }

      // Validate event.starts_at
      if (!this.event?.starts_at) {
        console.error('executeAlternativeLogic: Missing starts_at timestamp');
        return false;
      }

      // Check trace data
      const timeRangeValue = formatDateForPlusMinusDuration(new Date(`${this.event.starts_at}Z`).getTime(), 30);

      // Validate cloud_account_id
      if (!this.event?.cloud_account_id) {
        console.error('executeAlternativeLogic: Missing cloud_account_id');
        return false;
      }

      const data = {
        no_sinks: true,
        body: {
          account_id: this.event.cloud_account_id,
          action_name: 'service_map',
          action_params: {
            r_start_time: formatDate(timeRangeValue.dateMinusMinutes),
            r_end_time: formatDate(timeRangeValue.datePlusMinutes),
            workload_filter: {
              workload_name: sourceWorkloadName,
              workload_namespace: this.event.subject_namespace,
            },
          },
        },
        cache: false,
      };

      const response = await apiKubernetes.relayForwardRequest(data);

      if (response?.data?.data && Array.isArray(response.data.data)) {
        const filterMonitoringAndControlPlane = response.data.data
          .filter((d) => d?.Category?.category !== 'monitoring' && d?.Category?.category !== 'control-plane')
          .map((e) => ({
            ...e,
            Status: e?.Status || 'ok',
          }));

        if (filterMonitoringAndControlPlane.length > 0) {
          this.serviceMapDataFromEvidence = filterMonitoringAndControlPlane;
          this.insightData = response?.data?.insight || [];
          this.renderContent = true;
          return true;
        }
        this.renderContent = false;
        this.serviceMapDataFromEvidence = [];
        this.triggerCleanup();
        return false;
      }
      console.warn('executeAlternativeLogic: Invalid or empty response data');
      this.renderContent = false;
      this.triggerCleanup();
      return false;
    } catch (error) {
      console.error('Error in executeAlternativeLogic:', error);
      this.renderContent = false;
      this.triggerCleanup();
      return false;
    }
  }

  triggerCleanup() {
    if (this.cleanup && typeof this.cleanup === 'function') {
      try {
        this.cleanup(this);
      } catch (error) {
        console.error('Error during cleanup:', error);
      }
    }
  }

  setCleanupCallback(callback) {
    if (typeof callback === 'function') {
      this.cleanup = callback;
    } else {
      console.warn('setCleanupCallback: callback is not a function');
    }
  }

  async canRenderContent() {
    try {
      this.extractAlertLabels(this.event?.evidences);
      if (this.evidenceData?.data?.length > 0) {
        this.serviceMapDataFromEvidence = this.evidenceData.data;
        if (typeof this.serviceMapDataFromEvidence === 'string') {
          let parsedData = JSON.parse(this.serviceMapDataFromEvidence);
          if (Array.isArray(parsedData)) {
            this.serviceMapDataFromEvidence = parsedData;
          } else if (Array.isArray(parsedData.data)) {
            this.serviceMapDataFromEvidence = parsedData.data;
          } else {
            this.serviceMapDataFromEvidence = [];
          }
        }

        this.insightData = this.evidenceData?.insight || [];
        this.renderContent = true;
      } else if (this.evidenceData.data && typeof this.evidenceData.data == 'string') {
        try {
          const serviceMapDataFromEvidence = JSON.parse(this.evidenceData.data) || {};
          if (serviceMapDataFromEvidence?.data?.length > 0) {
            this.serviceMapDataFromEvidence = serviceMapDataFromEvidence.data;
            this.insightData = serviceMapDataFromEvidence?.insight || [];
            this.renderContent = true;
          } else {
            this.renderContent = await this.executeAlternativeLogic();
          }
        } catch (error) {
          console.error('Error parsing service map JSON:', error);
          this.renderContent = await this.executeAlternativeLogic();
        }
      } else {
        this.renderContent = await this.executeAlternativeLogic();
      }

      return this.renderContent;
    } catch (error) {
      console.error('Error in canRenderContent:', error);
      this.renderContent = false;
      return false;
    }
  }

  getHighLightsData() {
    return Array.isArray(this.insightData) ? this.insightData : [];
  }

  getContentComponents() {
    return [() => this.serviceMap()];
  }

  serviceMap() {
    try {
      const sourceWorkloadName = this.getSourceWorkloadName();

      if (!this.event?.starts_at) {
        console.error('serviceMap: Missing starts_at timestamp');
        return null;
      }

      const timeRangeValue = formatDateForPlusMinusDuration(new Date(`${this.event.starts_at}Z`).getTime(), 30);

      // Validate required props
      if (!this.event?.cloud_account_id) {
        console.error('serviceMap: Missing cloud_account_id');
        return null;
      }

      return (
        <KubernetesServiceMapWrapper
          accountId={this.event.cloud_account_id}
          appName={sourceWorkloadName}
          namespaceName={this.event?.subject_namespace}
          dateRange={{
            startDateInMilli: timeRangeValue.dateMinusMinutes,
            endDateInMilli: timeRangeValue.datePlusMinutes,
          }}
          dataForServiceMap={this.serviceMapDataFromEvidence}
          showSourceType={false}
        />
      );
    } catch (error) {
      console.error('Error rendering service map:', error);
      return null;
    }
  }
}

export default ServiceMapCard;
