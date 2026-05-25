import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import AttachMoneyIcon from '@mui/icons-material/AttachMoney';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface SpotRecommendationEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
}

const SpotRecommendationEvidence = ({ recommendation, estimatedSavings }: SpotRecommendationEvidenceProps) => {
  const rec = safeParseJSON(recommendation);

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Spot Instance Recommendation' muiIcon={<AttachMoneyIcon sx={{ fontSize: '16px' }} />} />

      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '10px',
          border: `1px solid ${colors.border.secondaryLight}`,
        }}
      >
        {rec.type && <MetricRow label='Workload Type' value={rec.type} />}
        {rec.namespace && <MetricRow label='Namespace' value={rec.namespace} />}
        {rec.controller_name && <MetricRow label='Controller' value={rec.controller_name} />}
        {rec.replica_count != null && <MetricRow label='Replicas' value={rec.replica_count} />}
        {rec.estimated_saving != null && <MetricRow label='Est. Saving' value={`$${rec.estimated_saving}`} highlight />}
        {rec.reason && <MetricRow label='Reason' value={rec.reason} />}
      </Box>

      {/* Suitability explanation */}
      <Box
        sx={{
          mt: '12px',
          p: '12px',
          borderRadius: '8px',
          backgroundColor: colors.background.primaryLightest,
          border: '1px solid #BFDBFE',
        }}
      >
        <Typography sx={{ fontSize: '12px', fontWeight: 600, color: '#1E40AF', mb: '4px' }}>Why Spot Instances?</Typography>
        <Typography sx={{ fontSize: '12px', color: '#1E40AF', lineHeight: 1.5 }}>
          This workload is a candidate for Spot instances. Spot instances provide up to 90% cost savings compared to on-demand pricing. They are ideal
          for fault-tolerant, stateless workloads that can handle interruptions gracefully.
        </Typography>
      </Box>

      {/* Considerations */}
      <Box
        sx={{
          mt: '8px',
          p: '12px',
          borderRadius: '8px',
          backgroundColor: '#FEF3C7',
          border: '1px solid #FDE68A',
        }}
      >
        <Typography sx={{ fontSize: '12px', fontWeight: 600, color: '#92400E', mb: '4px' }}>Considerations</Typography>
        <Typography sx={{ fontSize: '12px', color: '#92400E', lineHeight: 1.5 }}>
          Spot instances can be reclaimed with 2 minutes notice. Ensure your workload supports graceful shutdown and can tolerate interruptions before
          enabling spot scheduling.
        </Typography>
      </Box>

      {/* Render any remaining fields */}
      {renderRemainingFields(rec)}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const KNOWN_FIELDS = new Set(['type', 'namespace', 'controller_name', 'replica_count', 'estimated_saving', 'reason']);

function renderRemainingFields(rec: any) {
  const remaining = Object.entries(rec).filter(([key, value]) => !KNOWN_FIELDS.has(key) && value != null && typeof value !== 'object');
  if (remaining.length === 0) return null;

  return (
    <Box
      sx={{
        mt: '8px',
        backgroundColor: colors.background.tertiaryLightestestest,
        borderRadius: '8px',
        p: '10px',
        border: `1px solid ${colors.border.secondaryLight}`,
      }}
    >
      {remaining.slice(0, 6).map(([key, value]) => (
        <MetricRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </Box>
  );
}

export default SpotRecommendationEvidence;
