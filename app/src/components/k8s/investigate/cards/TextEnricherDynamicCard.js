// TextEnricherDynamicCard.js
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import MarkDowns from '@shared/viewers/MarkDowns';
import { Box } from '@mui/material';

class TextEnricherDynamicCard {
  constructor(data, index) {
    this.id = `TextEnricherCard_${index}`; // unique per card
    this.text = data?.title || `Diagnostic Summary`;
    this.icon = CubeIcon;
    this.resolveButton = false;
    this.enricherData = data;
    this.disabled = data?.additional_info?.status == 'skipped';
    // Set only when an automation attached this evidence (events.add_evidence);
    // the card header turns it into a link to that run. Undefined for enricher
    // evidence, which renders exactly as before.
    this.sourceWorkflow = data?.additional_info?.source_workflow;
    // Running one step on its own leaves no run to link to, so the header falls
    // back to saying an automation wrote this.
    this.authoredByAutomation = data?.additional_info?.actual_action_name === 'workflow_evidence';
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
