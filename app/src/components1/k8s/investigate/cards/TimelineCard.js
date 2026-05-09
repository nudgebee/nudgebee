import TimelineIcon from '@assets/kubernetes/timeline-icon.svg';
import DevOpsTimelineMUI from '@components1/common/DevOpsTimelineMUI';

class TimelineCard {
  constructor(event) {
    this.id = 'TimelineCard';
    this.icon = TimelineIcon;
    this.text = 'Timeline';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = true;
    this.event = event;
    this.onDataUpdate = null;
    this.refreshRenderId = 0;
  }

  // Method to set data update callback
  setDataUpdateCallback(callback) {
    this.onDataUpdate = callback;
    this.refreshRenderId += 1;
  }

  canRenderContent = async () => {
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderTimelineData()];
  };

  renderTimelineData = () => {
    return <DevOpsTimelineMUI eventId={this.event.id} />;
  };
}

export default TimelineCard;
