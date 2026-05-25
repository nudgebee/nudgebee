import AppDashboard, { getDashboardStats, getDashboardData } from '@components1/dashboards/AppDashboard';
import ApplicationsIconblue from '@assets/kubernetes/app-nodes-icons/ApplicationsIconBlue.icon.svg';

class ApplicationMetricsCard {
  constructor() {
    this.id = 'ApplicationMetricsCard';
    this.icon = ApplicationsIconblue;
    this.text = 'Application Metrics';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.alertLabels = {};
  }

  canRenderContent = async (_evidenceData, event) => {
    this.event = event;
    try {
      if (event.subject_type == 'pod') {
        let workload = event.service_key;
        if (workload) {
          let workloadSplit = workload.split('/');
          if (workloadSplit[workloadSplit.length - 2] != 'pod') {
            workload = workloadSplit[workloadSplit.length - 1];
          } else {
            let pod = workloadSplit[workloadSplit.length - 1];
            let podSplit = pod.split('-');
            workload = podSplit.slice(0, podSplit.length - 2).join('-');
          }
        }
        if (!workload) {
          return false;
        }
        this.workloadName = workload;
        this.namespaceName = event.subject_namespace;
        this.accountId = event.cloud_account_id;

        let resp = await getDashboardStats({
          accountId: event.cloud_account_id,
          namespaceName: event.subject_namespace,
          workloadName: workload,
        });
        try {
          if (!resp?.available || !resp?.lang) {
            return false;
          }
          const workloadAppType = resp.lang;
          const workloadDashboardName = resp?.dashboardName ?? '';
          const data = await getDashboardData(workloadAppType, workloadDashboardName);
          return data !== null;
        } catch {
          return false;
        }
      }
    } catch (e) {
      console.error(e);
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderAppDashboard()];
  };

  renderAppDashboard = () => {
    let createdAt = new Date(this.event.starts_at.endsWith('Z') ? this.event.starts_at : this.event.starts_at + 'Z');
    let startDate = new Date(createdAt.getTime() - 1 * 60 * 60 * 1000);
    let endDate = new Date(createdAt.getTime() + 10 * 60 * 1000);
    return (
      <AppDashboard
        accountId={this.accountId}
        namespaceName={this.namespaceName}
        workloadName={this.workloadName}
        podName={this.event.subject_name}
        startDate={startDate}
        endDate={endDate}
      />
    );
  };
}

export default ApplicationMetricsCard;
