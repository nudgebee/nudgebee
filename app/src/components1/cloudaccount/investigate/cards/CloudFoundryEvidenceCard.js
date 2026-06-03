import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import MarkDowns from '@components1/common/MarkDowns';
import { Box } from '@mui/material';
import Text from '@components1/common/format/Text';
import { ds } from '@utils/colors';

export const CF_ACTION_TYPES = [
  'app_details',
  'process_stats',
  'recent_builds',
  'recent_deployments',
  'service_bindings',
  'service_instance_details',
  'app_logs',
  'app_routes',
  'audit_timeline',
  'service_plan_details',
];

class CloudFoundryEvidenceCard {
  constructor(evidenceData, index) {
    this.id = `CloudFoundryEvidence_${index}`;
    this.text = evidenceData?.additional_info?.action_name || 'Cloud Foundry Evidence';
    this.icon = CubeIcon;
    this.enricherData = evidenceData;
  }

  async canRenderContent() {
    const actionType = this.enricherData?.additional_info?.action_type;
    return CF_ACTION_TYPES.includes(actionType) && !!this.enricherData?.data;
  }

  getHighLightsData = () => {
    return this.enricherData?.insight || [];
  };

  getContentComponents = () => {
    return [() => this.renderCardContent(this.enricherData)];
  };

  renderCardContent = (rawEventData) => {
    const actionType = rawEventData?.additional_info?.action_type;
    let markDownData = '';
    try {
      const parsed = typeof rawEventData.data === 'string' ? JSON.parse(rawEventData.data) : rawEventData.data;

      if (actionType === 'app_logs' && Array.isArray(parsed)) {
        // Format log entries as readable log lines
        const logLines = parsed
          .map((entry) => {
            const ts = entry.timestamp || '';
            const logType = entry.log_type === 'ERR' ? '[ERR]' : '[OUT]';
            const source = entry.source_type ? `[${entry.source_type}/${entry.instance_id || '0'}]` : '';
            const message = entry.message || '';
            return `${ts} ${logType} ${source} ${message}`;
          })
          .join('\n');
        markDownData = '```\n' + logLines + '\n```\n';
      } else if (actionType === 'audit_timeline' && Array.isArray(parsed)) {
        // Format audit event timeline as readable entries
        const timelineLines = parsed
          .map((entry) => {
            const time = entry.time || '';
            const eventType = entry.event_type || '';
            const actor = entry.actor || 'unknown';
            const actorType = entry.actor_type ? `(${entry.actor_type})` : '';
            return `${time}  ${eventType}  by ${actor} ${actorType}`;
          })
          .join('\n');
        markDownData = '```\n' + timelineLines + '\n```\n';
      } else {
        const formatted = JSON.stringify(parsed, null, 2);
        markDownData = '```json\n' + formatted + '\n```\n';
      }
    } catch (e) {
      console.error('Unable to parse CF evidence data', e);
    }
    return (
      <Box sx={{ p: 2 }}>
        {rawEventData?.additional_info?.action_name && (
          <Text value={rawEventData.additional_info.action_name} sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, mb: ds.space[2] }} />
        )}
        <MarkDowns
          key='cf-evidence-data'
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

export default CloudFoundryEvidenceCard;
