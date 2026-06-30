import PrometheusCPUUsageCardComponent, { getDataFromProemtheus } from './PrometheusCPUUsageCardComponent';
import CPUUsageIcon from '@assets/investigation/cpu-usage.svg';

class PrometheusCPUUsageCard {
  constructor() {
    this.id = 'PrometheusCPUUsageCard';
    this.icon = CPUUsageIcon;
    this.text = 'CPU Usage';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.prometheusCPUUsage = {};
  }

  canRenderContent = async (_, event) => {
    if (event.aggregation_key === 'image_pull_backoff_reporter') {
      this.renderContent = false;
      return this.renderContent;
    }
    if (event?.subject_namespace && event?.subject_name && event?.subject_type == 'pod') {
      const result = await getDataFromProemtheus(event);
      this.event = event;
      this.prometheusCPUUsage = result;
      if (result) {
        this.renderContent = true;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => <PrometheusCPUUsageCardComponent event={this.event} data={this.prometheusCPUUsage} />];
  };
}

export default PrometheusCPUUsageCard;
