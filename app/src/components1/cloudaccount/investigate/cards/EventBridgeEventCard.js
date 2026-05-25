// TextEnricherDynamicCard.js
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import MarkDowns from '@components1/common/MarkDowns';
import { Box } from '@mui/material';

class EventBridgeEventCard {
  constructor(_evidence, data, index) {
    this.id = `EventBridgeEventCard_${index}`; // unique per card
    this.text = `EventBridge Event`;
    this.icon = CubeIcon;
    this.enricherData = data;
  }

  async canRenderContent() {
    return this.enricherData?.source === 'AWS_EventBridge' && this.enricherData?.evidences?.some((e) => e.additional_info?.source === 'Event.Raw');
  }

  getHighLightsData = () => {
    return this.enricherData?.insight || [];
  };

  getContentComponents = () => {
    let rawEvent = this.enricherData?.evidences?.filter((e) => e.additional_info?.source === 'Event.Raw');
    return [() => this.renderCardContent(rawEvent?.[0], this.enricherData)];
  };

  renderCardContent = (rawEventData, _data) => {
    let markDownData = '';
    try {
      let jsonMap = JSON.parse(rawEventData.data);
      let rawData = JSON.stringify(jsonMap, null, 2);
      markDownData = '```json\n' + rawData + '\n```\n';
    } catch (e) {
      console.error('unable to parse data', e);
    }
    return (
      <Box sx={{ p: 2 }}>
        <MarkDowns
          key={`json-data`}
          data={markDownData}
          sx={{
            maxHeight: 'unset',
            overflowY: 'unset',
            width: '100%',
            maxWidth: '100%',
          }}
        />
      </Box>
    );
  };
}

export default EventBridgeEventCard;
