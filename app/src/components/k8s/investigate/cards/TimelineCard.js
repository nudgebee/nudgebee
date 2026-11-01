import TimelineIcon from '@assets/kubernetes/timeline-icon.svg';
import DevOpsTimelineMUI from '@shared/widgets/DevOpsTimelineMUI';
import apiKubernetes1 from '@api1/kubernetes1';
import { summarizeTimeline } from './cardSummaries';

class TimelineCard {
  constructor(event) {
    this.id = 'TimelineCard';
    this.icon = TimelineIcon;
    this.text = 'Timeline';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = true;
    this.event = event;
    this.timeline = null;
    this.onDataUpdate = null;
    this.refreshRenderId = 0;
  }

  // Method to set data update callback
  setDataUpdateCallback(callback) {
    this.onDataUpdate = callback;
    this.refreshRenderId += 1;
  }

  // The page awaits this while building the card list, so the timeline is fetched here: the
  // collapsed card can then say what it holds, and the opened card reuses the result instead of
  // fetching it again.
  canRenderContent = async () => {
    try {
      const res = await apiKubernetes1.getTimelineData(this.event.id);
      this.timeline = res?.data?.data?.event_get_timeline || null;
      this.insightData = summarizeTimeline(this.timeline?.timeline);
    } catch (error) {
      console.error('Error fetching timeline:', error);
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderTimelineData()];
  };

  renderTimelineData = () => {
    return <DevOpsTimelineMUI eventId={this.event.id} prefetched={this.timeline} />;
  };
}

export default TimelineCard;
