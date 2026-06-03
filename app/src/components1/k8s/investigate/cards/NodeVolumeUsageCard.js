import apiKubernetes from '@api1/kubernetes';
import { KubernetesNodeStorageUtilization } from '@components1/k8s/common/KubernetesTable2';
import CPUUsageIcon from '@assets/investigation/cpu-usage.svg';

class NodeVolumeUsageCard {
  constructor() {
    this.id = 'NodeVolumeUsageCard';
    this.icon = CPUUsageIcon;
    this.text = 'Node Disk Usage';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
  }

  canRenderContent = async (evidenceData, event) => {
    if (event.aggregation_key !== 'KubernetesVolumeOutOfDiskSpace') {
      this.renderContent = false;
      return this.renderContent;
    }

    let instanceName;

    const filterTableType = evidenceData.filter((item) => item.type === 'table' && item.data.table_name.includes('Alert labels'));
    if (filterTableType && filterTableType.length > 0) {
      let t = filterTableType[0];
      for (let r of t.data.rows) {
        if (r[0] === 'instance') {
          instanceName = r[1];
        }
      }
    }

    if (!instanceName) {
      this.renderContent = false;
      return this.renderContent;
    }

    let nodes = await apiKubernetes.getK8sNodes({
      accountId: event.cloud_account_id,
      nodeName: instanceName,
      isActive: null,
    });

    let nodeIp = nodes.data.k8s_nodes?.[0]?.internal_ip;

    this.instanceName = instanceName;
    this.instanceIp = nodeIp;
    this.event = event;
    this.renderContent = true;
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [
      () => (
        <KubernetesNodeStorageUtilization
          accountId={this.event.cloud_account_id}
          query={{
            nodeIp: this.instanceIp || this.instanceName,
          }}
        />
      ),
    ];
  };
}

export default NodeVolumeUsageCard;
