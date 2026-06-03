import TraceIcon from '@assets/kubernetes/trace-icon.svg';
import KubernetesHelmCompatibleRecommendation from '@components1/recommendations/KubernetesHelmCompatibleRecommendation';

class HelmUpgrade {
  constructor() {
    this.id = 'HelmUpgrade';
    this.icon = TraceIcon;
    this.text = 'Helm Compatibility';
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
    return [() => this.renderHelmUpgrade()];
  };

  renderHelmUpgrade = () => {
    return <KubernetesHelmCompatibleRecommendation accountId={this.accountId} />;
  };
}

export default HelmUpgrade;
