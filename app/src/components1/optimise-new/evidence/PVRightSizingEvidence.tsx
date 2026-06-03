import { Box, Typography, LinearProgress } from '@mui/material';
import { ds } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import { formatMemory } from '@lib/formatter';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import StorageIcon from '@mui/icons-material/Storage';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface PVRightSizingEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
  cloudResource?: any;
}

const getUtilizationColor = (pct: number): string => {
  if (pct > 90) return ds.red[600];
  if (pct > 70) return ds.amber[500];
  return ds.green[600];
};

const UtilizationBar = ({ pct }: { pct: number }) => (
  <Box sx={{ mb: ds.space[3] }}>
    <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: ds.space[1] }}>
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Storage Utilization</Typography>
      <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{pct.toFixed(1)}%</Typography>
    </Box>
    <LinearProgress
      variant='determinate'
      value={Math.min(pct, 100)}
      sx={{
        height: '8px',
        borderRadius: ds.radius.sm,
        backgroundColor: ds.gray[200],
        '& .MuiLinearProgress-bar': {
          borderRadius: ds.radius.sm,
          backgroundColor: getUtilizationColor(pct),
        },
      }}
    />
  </Box>
);

const PVRightSizingEvidence = ({ recommendation, estimatedSavings, cloudResource }: PVRightSizingEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec) return null;

  // Extract storage fields
  const capacity = rec.capacity || rec.spec?.capacity?.storage;
  const currentUsage = rec.usage?.current;
  const recommendedSize = rec.recommend_size;
  const duration = rec.duration || '7';
  const pvcName = rec.metadata?.name || rec.spec?.claimRef?.name || cloudResource?.name || '';
  const namespace = rec.metadata?.namespace || rec.spec?.claimRef?.namespace || cloudResource?.meta?.namespace || '';

  // Format storage values
  const formatStorage = (val: any, unit?: string): string => {
    if (val == null) return '—';
    if (typeof val === 'string') return val; // already formatted like "10Gi"
    if (unit === 'gb') return `${Number(val).toFixed(1)} GB`;
    // Assume bytes
    return formatMemory(val, 'bytes', 'gb', true) || `${val}`;
  };

  const capacityStr = formatStorage(capacity, typeof capacity === 'number' && capacity < 10000 ? 'gb' : undefined);
  const usageStr = currentUsage != null ? formatStorage(currentUsage) : null;
  const recommendedStr = recommendedSize != null ? `${Number(recommendedSize).toFixed(1)} GB` : null;

  // Utilization percentage
  let utilizationPct: number | null = null;
  if (currentUsage != null && capacity != null && typeof capacity === 'number' && capacity > 0) {
    utilizationPct = (currentUsage / capacity) * 100;
  }

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Persistent Volume Right-Sizing' muiIcon={<StorageIcon sx={{ fontSize: '16px' }} />} />

      {/* Size comparison */}
      {capacityStr !== '—' && recommendedStr && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[4],
            p: '14px',
            backgroundColor: ds.blue[100],
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.blue[200]}`,
            mb: ds.space[3],
            justifyContent: 'center',
          }}
        >
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: '2px' }}>Current</Typography>
            <Typography sx={{ fontSize: ds.text.heading, fontWeight: ds.weight.semibold, color: ds.red[600] }}>{capacityStr}</Typography>
          </Box>
          <ArrowForwardIcon sx={{ fontSize: '20px', color: ds.blue[700] }} />
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: '2px' }}>Recommended</Typography>
            <Typography sx={{ fontSize: ds.text.heading, fontWeight: ds.weight.semibold, color: ds.green[600] }}>{recommendedStr}</Typography>
          </Box>
        </Box>
      )}

      {/* Utilization bar */}
      {utilizationPct != null && <UtilizationBar pct={utilizationPct} />}

      {/* Volume metadata */}
      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
        }}
      >
        {pvcName && <MetricRow label='PVC Name' value={pvcName} />}
        {namespace && <MetricRow label='Namespace' value={namespace} />}
        <MetricRow label='Current Allocation' value={capacityStr} />
        {usageStr && <MetricRow label='Current Usage' value={usageStr} />}
        {recommendedStr && <MetricRow label='Recommended Size' value={recommendedStr} highlight />}
        <MetricRow label='Observation Duration' value={`${duration} days`} />
        {rec.metadata?.creationTimestamp && <MetricRow label='Created' value={new Date(rec.metadata.creationTimestamp).toLocaleDateString()} />}
      </Box>

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

export default PVRightSizingEvidence;
