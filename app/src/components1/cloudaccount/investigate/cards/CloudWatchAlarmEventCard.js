// TextEnricherDynamicCard.js
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import MarkDowns from '@components1/common/MarkDowns';
import { Box } from '@mui/material';

class CloudWatchAlarmEventCard {
  constructor(evidenceData, index) {
    this.id = `CloudWatchAlarmEventCard_${index}`; // unique per card
    this.text = `CloudWatch Alarm`;
    this.icon = CubeIcon;
    this.enricherData = evidenceData;
  }

  async canRenderContent() {
    if (this.enricherData?.data) {
      return true;
    }
    return false;
  }

  getHighLightsData = () => {
    return this.enricherData?.insight || [];
  };

  getContentComponents = () => {
    return [() => this.renderCardContent(this.enricherData)];
  };

  renderCardContent = (rawEventData) => {
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

export default CloudWatchAlarmEventCard;
