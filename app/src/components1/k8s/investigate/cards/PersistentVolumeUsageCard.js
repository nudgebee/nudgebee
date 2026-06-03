import { KubernetesPVCUtilization } from '@components1/k8s/common/KubernetesTable2';
import PVCIcon from '@assets/kubernetes/app-nodes-icons/PV-icon.icon.svg';

class PersistentVolumeUsageCard {
  constructor() {
    this.id = 'PersistentVolumeUsageCard';
    this.icon = PVCIcon;
    this.text = 'PVC Usage';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
  }

  canRenderContent = async (evidenceData, event) => {
    if (event.aggregation_key !== 'KubePersistentVolumeFillingUp') {
      this.renderContent = false;
      return this.renderContent;
    }

    let pvcName, namespace;

    const filterTableType = evidenceData.filter((item) => item.type === 'table' && item.data.table_name.includes('Alert labels'));
    if (filterTableType && filterTableType.length > 0) {
      let t = filterTableType[0];
      for (let r of t.data.rows) {
        if (r[0] === 'persistentvolumeclaim') {
          pvcName = r[1];
        }
        if (r[0] === 'namespace') {
          namespace = r[1];
        }
      }
    }

    if (!(pvcName && namespace)) {
      this.renderContent = false;
      return this.renderContent;
    }

    this.pvcName = pvcName;
    this.namespace = namespace;
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
        <KubernetesPVCUtilization
          accountId={this.event.cloud_account_id}
          query={{
            pvcName: this.pvcName,
            namespaceName: this.namespace,
          }}
        />
      ),
    ];
  };
}

export default PersistentVolumeUsageCard;
