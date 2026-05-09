// TextEnricherDynamicCard.js
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import MarkDowns from '@components1/common/MarkDowns';
import { Box } from '@mui/material';

class TextEnricherDynamicCard {
  constructor(data, index) {
    this.id = `TextEnricherCard_${index}`; // unique per card
    this.text = data?.title || `Diagnostic Summary`;
    this.icon = CubeIcon;
    this.resolveButton = false;
    this.enricherData = data;
    this.disabled = data?.additional_info?.status == 'skipped';
  }

  async canRenderContent() {
    return !!this.enricherData;
  }

  getHighLightsData = () => {
    return this.enricherData?.insight || [];
  };

  getContentComponents = () => {
    return [() => this.renderCardContent(this.enricherData)];
  };

  renderCardContent = (data) => {
    return (
      <Box sx={{ p: 2 }}>
        <MarkDowns
          key={`text-data`}
          data={data.data?.trim()}
          sx={{
            maxHeight: 'unset',
            overflowY: 'unset',
          }}
        />
      </Box>
    );
  };
}

export default TextEnricherDynamicCard;
