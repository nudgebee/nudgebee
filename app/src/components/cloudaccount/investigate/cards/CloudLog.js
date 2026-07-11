import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';
import { safeJSONParse } from 'src/utils/common';
import CloudLogViewer from './CloudLogViewer';

// Default card titles per log source, used when the enricher didn't stamp an explicit
// additional_info.title (e.g. evidence captured before title propagation). Without this
// the audit-log and application-log cards both fall back to "Cloud Logs".
const ACTION_TITLES = {
  cloud_gcp_audit_log: 'Recent Changes & Deployments',
  cloud_logs: 'Cloud Logs',
};

const MESSAGE_KEYS = ['message', 'msg', 'body', 'error', 'event', 'log'];

// The cloud_logs insight is "<severity>:<raw log body>". For structured JSON logs the
// body is the entire record; reduce it to its inner message so the highlight reads
// "ERROR: <message>" instead of dumping JSON. The backend now does this at the source;
// this also cleans up evidence captured before that fix.
const normalizeInsightMessage = (msg) => {
  if (typeof msg !== 'string') {
    return msg;
  }
  const sep = msg.indexOf(':');
  if (sep === -1) {
    return msg;
  }
  const prefix = msg.slice(0, sep);
  const rest = msg.slice(sep + 1).trim();
  if (rest.startsWith('{') && rest.endsWith('}')) {
    const parsed = safeJSONParse(rest);
    if (parsed && typeof parsed === 'object') {
      const key = MESSAGE_KEYS.find((k) => typeof parsed[k] === 'string' && parsed[k].trim() !== '');
      if (key) {
        return `${prefix}: ${parsed[key]}`;
      }
    }
  }
  return msg;
};

class CloudLog {
  constructor(data, _event) {
    this.id = `CloudLog_${_event}`;
    const action = data?.additional_info?.action_name;
    this.text = titleCase(data?.additional_info?.title) || ACTION_TITLES[action] || 'Cloud Logs';
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
    return this.insightData.map((item) =>
      item && typeof item === 'object' && typeof item.message === 'string' ? { ...item, message: normalizeInsightMessage(item.message) } : item
    );
  };

  getContentComponents = () => {
    return [() => <CloudLogViewer logs={this.logs} />];
  };
}

export default CloudLog;
