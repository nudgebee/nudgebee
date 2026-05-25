import DescriptionIcon from '@assets/kubernetes/description-icon.svg';
import MarkDowns from '@components1/common/MarkDowns';

class WebhookEventDescription {
  constructor(evidenceData, event, index) {
    this.id = `WebhookEventDescriptionCard_${index}`;
    this.icon = DescriptionIcon;
    this.text = 'Webhook Event Description';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.event = event;
    this.webhookData = {};
    this.evidenceData = evidenceData;
  }

  canRenderContent = async () => {
    const eventData = this.evidenceData;
    if (eventData?.data?.name == 'Event Description') {
      this.webhookData = eventData;
      this.renderContent = true;
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
    let data2 = this.webhookData?.data?.data;
    if (data2) {
      data2 = data2.replace(/%%%/g, '');
    }
    return <MarkDowns data={data2} />;
  };
}

export default WebhookEventDescription;
