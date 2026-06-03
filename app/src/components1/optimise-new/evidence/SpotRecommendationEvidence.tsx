import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
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
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: '10px',
          border: `1px solid ${ds.gray[200]}`,
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
          mt: ds.space[3],
          p: ds.space[3],
          borderRadius: ds.radius.lg,
          backgroundColor: ds.blue[100],
          border: `1px solid ${ds.blue[200]}`,
        }}
      >
        <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.blue[700], mb: ds.space[1] }}>
          Why Spot Instances?
        </Typography>
        <Typography sx={{ fontSize: ds.text.small, color: ds.blue[700], lineHeight: 1.5 }}>
          This workload is a candidate for Spot instances. Spot instances provide up to 90% cost savings compared to on-demand pricing. They are ideal
          for fault-tolerant, stateless workloads that can handle interruptions gracefully.
        </Typography>
      </Box>

      {/* Considerations */}
      <Box
        sx={{
          mt: ds.space[2],
          p: ds.space[3],
          borderRadius: ds.radius.lg,
          backgroundColor: ds.amber[100],
          border: `1px solid ${ds.amber[200]}`,
        }}
      >
        <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.amber[700], mb: ds.space[1] }}>
          Considerations
        </Typography>
        <Typography sx={{ fontSize: ds.text.small, color: ds.amber[700], lineHeight: 1.5 }}>
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
        mt: ds.space[2],
        backgroundColor: ds.gray[100],
        borderRadius: ds.radius.lg,
        p: '10px',
        border: `1px solid ${ds.gray[200]}`,
      }}
    >
      {remaining.slice(0, 6).map(([key, value]) => (
        <MetricRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </Box>
  );
}

export default SpotRecommendationEvidence;
