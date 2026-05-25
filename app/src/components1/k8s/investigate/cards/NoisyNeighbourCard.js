import { safeJSONParse } from 'src/utils/common';
import NoisyNeighbour from './NoisyNeighbour';
import NoisyNeighbourSummary from './NoisyNeighbourSummary';
import NoisyNeighbourIcon from '@assets/investigation/noise-neighbours.svg';

class NoisyNeighbourCard {
  constructor() {
    this.id = 'NoisyNeighbourCard';
    this.icon = NoisyNeighbourIcon;
    this.text = 'Noisy Neighbours';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.troubleShootingEvent = {};
    this.metricGraphData = {};
    this.nodeGraphData = {};
    this.podGraphData = {};
    this.memLimit = null;
    this.podMemoryAllocationItem = {};
  }

  canRenderContent = async (evidenceData, troubleShootingEvent) => {
    const noisyInsight = evidenceData
      .filter((item) => item.type === 'json')
      .filter((index) => {
        // Check if the data is a valid JSON and contains the 'name' field
        if (!index?.data || typeof index.data !== 'string') {
          return false;
        }
        return safeJSONParse(index?.data)?.name == 'noisy_neighbours' && index?.data;
      });
    if (noisyInsight && noisyInsight.length > 0) {
      this.renderContent = true;
      this.insightData = noisyInsight[0]?.insight;
    }
    this.troubleShootingEvent = troubleShootingEvent;
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => <NoisyNeighbourSummary row={this.troubleShootingEvent} />, () => <NoisyNeighbour row={this.troubleShootingEvent} />];
  };
}

export default NoisyNeighbourCard;
