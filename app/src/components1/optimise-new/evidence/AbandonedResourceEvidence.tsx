import { Box, Typography, LinearProgress } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import ReportProblemIcon from '@mui/icons-material/ReportProblem';
import InfoIcon from '@mui/icons-material/Info';
import BoltIcon from '@mui/icons-material/Bolt';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface AbandonedResourceEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
  cloudResource?: any;
}

const AbandonedResourceEvidence = ({ recommendation, estimatedSavings, cloudResource }: AbandonedResourceEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec) return null;

  const traffic = rec.traffic ?? rec.network_traffic;
  const threshold = rec.threshold;
  const duration = rec.duration || '7';
  const message = rec.message || '';
  const cpu = rec.cpu;
  const memory = rec.memory;

  const resourceName = cloudResource?.name || '';
  const resourceType = cloudResource?.type || '';
  const namespace = cloudResource?.meta?.namespace || cloudResource?.meta?.config?.namespace || '';
  const controller = cloudResource?.meta?.controller || '';
  const controllerKind = cloudResource?.meta?.controllerKind || '';

  // Traffic as percentage of threshold
  let trafficPct: number | null = null;
  if (traffic != null && threshold != null && threshold > 0) {
    trafficPct = (traffic / threshold) * 100;
  }

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Abandoned Workload Detection' muiIcon={<ReportProblemIcon sx={{ fontSize: '16px' }} />} />

      {/* Warning banner */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: '10px',
          p: '12px',
          backgroundColor: '#FEF3C7',
          borderRadius: '8px',
          border: '1px solid #FDE68A',
          mb: '12px',
        }}
      >
        <WarningAmberIcon sx={{ fontSize: '18px', color: '#92400E', mt: '1px' }} />
        <Box>
          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: '#92400E', mb: '4px' }}>Low Activity Detected</Typography>
          <Typography sx={{ fontSize: '12px', color: '#78350F', lineHeight: 1.5 }}>
            {message || `This workload shows minimal network traffic over the last ${duration} days, suggesting it may be abandoned or unused.`}
          </Typography>
        </Box>
      </Box>

      {/* Resource info */}
      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '12px',
          border: `1px solid ${colors.border.secondaryLight}`,
          mb: '12px',
        }}
      >
        {resourceName && <MetricRow label='Resource' value={resourceName} />}
        {resourceType && <MetricRow label='Type' value={resourceType} />}
        {namespace && <MetricRow label='Namespace' value={namespace} />}
        {controller && <MetricRow label='Controller' value={`${controllerKind}/${controller}`} />}
        <MetricRow label='Observation Period' value={`${duration} days`} />
      </Box>

      {/* Traffic metric */}
      {traffic != null && (
        <>
          <SectionTitle title='Network Traffic' muiIcon={<InfoIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '12px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '12px',
            }}
          >
            <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: '8px' }}>
              <Box>
                <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Current Traffic</Typography>
                <Typography sx={{ fontSize: '16px', fontWeight: 700, color: '#DC2626' }}>{Number(traffic).toLocaleString()} bytes</Typography>
              </Box>
              {threshold != null && (
                <Box sx={{ textAlign: 'right' }}>
                  <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Threshold</Typography>
                  <Typography sx={{ fontSize: '16px', fontWeight: 600, color: colors.text.secondary }}>
                    {Number(threshold).toLocaleString()} bytes
                  </Typography>
                </Box>
              )}
            </Box>
            {trafficPct != null && (
              <Box>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: '4px' }}>
                  <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Traffic vs Threshold</Typography>
                  <Typography sx={{ fontSize: '10px', fontWeight: 600, color: '#DC2626' }}>{trafficPct.toFixed(1)}%</Typography>
                </Box>
                <LinearProgress
                  variant='determinate'
                  value={Math.min(trafficPct, 100)}
                  sx={{
                    height: '6px',
                    borderRadius: '3px',
                    backgroundColor: colors.border.secondaryLightest,
                    '& .MuiLinearProgress-bar': {
                      borderRadius: '3px',
                      backgroundColor: trafficPct < 10 ? '#DC2626' : '#F59E0B',
                    },
                  }}
                />
              </Box>
            )}
          </Box>
        </>
      )}

      {/* Resource usage */}
      {(cpu != null || memory != null) && (
        <>
          <SectionTitle title='Resource Usage' muiIcon={<BoltIcon sx={{ fontSize: '16px' }} />} />
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px', mb: '12px' }}>
            {cpu != null && (
              <Box
                sx={{
                  p: '10px',
                  borderRadius: '8px',
                  backgroundColor: colors.background.tertiaryLightestestest,
                  border: `1px solid ${colors.border.secondaryLight}`,
                  borderLeft: '3px solid #3B82F6',
                }}
              >
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>CPU</Typography>
                <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, fontFamily: 'monospace' }}>
                  {Number(cpu).toFixed(3)} cores
                </Typography>
              </Box>
            )}
            {memory != null && (
              <Box
                sx={{
                  p: '10px',
                  borderRadius: '8px',
                  backgroundColor: colors.background.tertiaryLightestestest,
                  border: `1px solid ${colors.border.secondaryLight}`,
                  borderLeft: '3px solid #8B5CF6',
                }}
              >
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>Memory</Typography>
                <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, fontFamily: 'monospace' }}>
                  {Number(memory).toFixed(0)} Mi
                </Typography>
              </Box>
            )}
          </Box>
        </>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

export default AbandonedResourceEvidence;
