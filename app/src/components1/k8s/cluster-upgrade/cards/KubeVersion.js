import TraceIcon from '@assets/kubernetes/trace-icon.svg';
import KubernetesKubeVersionRecommendation from '@components1/recommendations/KubernetesKubeVersionRecommendation';

class KubeVersion {
  constructor() {
    this.id = 'KubeVersion';
    this.icon = TraceIcon;
    this.text = 'Kube Proxy Version';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.accountId = '';
  }

  canRenderContent = async (accountId) => {
    this.renderContent = true;
    this.accountId = accountId;
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderKubeVersion()];
  };

  renderKubeVersion = () => {
    return <KubernetesKubeVersionRecommendation accountId={this.accountId} />;
  };
}

export default KubeVersion;
