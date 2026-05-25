import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import BarChartIcon from '@mui/icons-material/BarChart';
import TrendingUpIcon from '@mui/icons-material/TrendingUp';
import BoltIcon from '@mui/icons-material/Bolt';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface ReplicaRightSizingEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
}

const ReplicaRightSizingEvidence = ({ recommendation, estimatedSavings }: ReplicaRightSizingEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec) return null;

  const allocatedReplica = rec.allocated_replica ?? rec.allocated?.[rec.allocated?.length - 1]?.replicas;
  const recommendedReplica = rec.recommended_replica ?? rec.recommended?.[rec.recommended?.length - 1]?.replicas;
  const recommendedType = rec.recommended_type || '';
  const duration = rec.duration || '7';
  const errorMsg = rec.error;

  // Extract evidence summary (latest values)
  const evidence = Array.isArray(rec.evidence) ? rec.evidence : [];
  const latestEvidence = evidence.length > 0 ? evidence[evidence.length - 1] : null;

  // Chart data for allocated vs recommended replicas over time
  const allocatedSeries = Array.isArray(rec.allocated) ? rec.allocated : [];
  const recommendedSeries = Array.isArray(rec.recommended) ? rec.recommended : [];

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Replica Right-Sizing' muiIcon={<BarChartIcon sx={{ fontSize: '16px' }} />} />

      {errorMsg && (
        <Box sx={{ backgroundColor: colors.background.accordionSummay, borderRadius: '8px', p: '10px', border: '1px solid #FECACA', mb: '12px' }}>
          <Typography sx={{ fontSize: '12px', color: '#991B1B' }}>{errorMsg}</Typography>
        </Box>
      )}

      {/* Replica comparison */}
      {allocatedReplica != null && recommendedReplica != null && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '16px',
            p: '14px',
            backgroundColor: colors.background.primaryLightest,
            borderRadius: '8px',
            border: '1px solid #BFDBFE',
            mb: '12px',
            justifyContent: 'center',
          }}
        >
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>Current</Typography>
            <Typography sx={{ fontSize: '24px', fontWeight: 700, color: '#DC2626' }}>{allocatedReplica}</Typography>
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>replicas</Typography>
          </Box>
          <ArrowForwardIcon sx={{ fontSize: '20px', color: '#1E40AF' }} />
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>Recommended</Typography>
            <Typography sx={{ fontSize: '24px', fontWeight: 700, color: '#16A34A' }}>{recommendedReplica}</Typography>
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>replicas</Typography>
          </Box>
        </Box>
      )}

      {/* Metadata */}
      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '12px',
          border: `1px solid ${colors.border.secondaryLight}`,
          mb: '12px',
        }}
      >
        {recommendedType && <MetricRow label='Strategy' value={recommendedType.replace(/_/g, ' ')} />}
        <MetricRow label='Observation Duration' value={`${duration} days`} />
        {allocatedReplica != null && <MetricRow label='Current Replicas' value={allocatedReplica} />}
        {recommendedReplica != null && <MetricRow label='Recommended Replicas' value={recommendedReplica} highlight />}
        {rec.usage?.memory_request && <MetricRow label='Memory Request' value={rec.usage.memory_request} />}
        {rec.usage?.memory_usage && <MetricRow label='Memory Usage' value={rec.usage.memory_usage} />}
      </Box>

      {/* Replica trend mini-chart (text-based) */}
      {allocatedSeries.length > 0 && (
        <>
          <SectionTitle title={`Replica History (${allocatedSeries.length} data points)`} muiIcon={<TrendingUpIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '10px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '12px',
              maxHeight: '150px',
              overflow: 'auto',
            }}
          >
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 80px 80px', gap: '2px' }}>
              <Typography sx={{ fontSize: '10px', fontWeight: 600, color: colors.text.tertiary }}>Time</Typography>
              <Typography sx={{ fontSize: '10px', fontWeight: 600, color: colors.text.tertiary, textAlign: 'center' }}>Allocated</Typography>
              <Typography sx={{ fontSize: '10px', fontWeight: 600, color: colors.text.tertiary, textAlign: 'center' }}>Recommended</Typography>
              {allocatedSeries.slice(-10).map((item: any, idx: number) => {
                const recItem = recommendedSeries[recommendedSeries.length - Math.min(10, allocatedSeries.length) + idx];
                const time = item.timestamp
                  ? new Date(item.timestamp).toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
                  : `—`;
                return (
                  <Box key={item.timestamp || `ts-${idx}`} sx={{ display: 'contents' }}>
                    <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, fontFamily: 'monospace' }}>{time}</Typography>
                    <Typography sx={{ fontSize: '10px', color: '#DC2626', textAlign: 'center', fontWeight: 500 }}>{item.replicas}</Typography>
                    <Typography sx={{ fontSize: '10px', color: '#16A34A', textAlign: 'center', fontWeight: 500 }}>
                      {recItem?.replicas ?? '—'}
                    </Typography>
                  </Box>
                );
              })}
            </Box>
          </Box>
        </>
      )}

      {/* Evidence metrics */}
      {latestEvidence && (
        <>
          <SectionTitle title='Workload Metrics (Latest)' muiIcon={<BoltIcon sx={{ fontSize: '16px' }} />} />
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px', mb: '12px' }}>
            {latestEvidence.cpu != null && <MetricBox label='CPU Usage' value={`${Number(latestEvidence.cpu).toFixed(3)} cores`} color='#3B82F6' />}
            {latestEvidence.memory != null && (
              <MetricBox label='Memory Usage' value={`${Number(latestEvidence.memory).toFixed(0)} Mi`} color='#8B5CF6' />
            )}
            {latestEvidence.rps != null && <MetricBox label='Requests/s' value={`${Number(latestEvidence.rps).toFixed(1)}`} color='#F59E0B' />}
            {latestEvidence.latency != null && <MetricBox label='Latency' value={`${Number(latestEvidence.latency).toFixed(3)} s`} color='#EF4444' />}
          </Box>
        </>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const MetricBox = ({ label, value, color }: { label: string; value: string; color: string }) => (
  <Box
    sx={{
      p: '10px',
      borderRadius: '8px',
      backgroundColor: colors.background.tertiaryLightestestest,
      border: `1px solid ${colors.border.secondaryLight}`,
      borderLeft: `3px solid ${color}`,
    }}
  >
    <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>{label}</Typography>
    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, fontFamily: 'monospace' }}>{value}</Typography>
  </Box>
);

export default ReplicaRightSizingEvidence;
