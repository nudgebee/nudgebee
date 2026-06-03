import DescriptionIcon from '@assets/kubernetes/description-icon.svg';
import { DrilldownChartComponent } from '@components1/k8s/details/KubernetesAnomaly';
import { titleCase } from '@lib/formatter';

class AnomalyCard {
  constructor(data, event) {
    this.id = 'AnomalyCard';
    this.icon = DescriptionIcon;
    this.text = titleCase(data?.additional_info?.title || `Anomaly`);
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.event = event;
    this.anomalyData = data;
  }

  canRenderContent = async () => {
    const isAnomaly = this.anomalyData?.is_anomaly == true;
    if (isAnomaly) {
      this.renderContent = true;
      const insightData = this.anomalyData.insight;
      if (insightData?.length > 0) {
        this.insightData = insightData;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderAnomaly()];
  };

  renderAnomaly = () => {
    return <DrilldownChartComponent value={this.anomalyData} />;
  };
}

export default AnomalyCard;
