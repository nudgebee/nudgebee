import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';
import { safeJSONParse } from 'src/utils/common';
import CloudLogViewer from './CloudLogViewer';

class CloudLog {
  constructor(data, _event) {
    this.id = `CloudLog_${_event}`;
    this.text = titleCase(data?.additional_info?.title) || 'Cloud Logs';
    this.icon = LogsIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.logs = [];
    this.disabled = data?.additional_info?.status === 'skipped';
  }

  canRenderContent = async () => {
    this.renderContent = false;
    const isCloudLogData = ['cloud_logs', 'cloud_gcp_audit_log'].includes(this.enricherData?.additional_info?.action_name);
    if (isCloudLogData) {
      const serverLogParsedData = safeJSONParse(this.enricherData.data);
      if (serverLogParsedData && Array.isArray(serverLogParsedData.data) && serverLogParsedData.data.length > 0) {
        // Keep the full structured record per entry (timestamp, message, attributes).
        // GCP request logs (httpRequest entries) carry no textPayload, so their data
        // lives entirely in `attributes`; the viewer surfaces it generically.
        this.logs = serverLogParsedData.data;
        this.insightData = this.enricherData?.insight ? [...this.enricherData.insight] : [];
        this.renderContent = true;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => <CloudLogViewer logs={this.logs} />];
  };
}

export default CloudLog;
