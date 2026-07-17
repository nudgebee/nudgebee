import { Box, Typography, LinearProgress } from '@mui/material';
import { ds } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components/optimise-new/EvidencePanel';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import ReportProblemIcon from '@mui/icons-material/ReportProblem';
import InfoIcon from '@mui/icons-material/Info';
import BoltIcon from '@mui/icons-material/Bolt';
import { safeParseJSON } from '@components/optimise-new/utils';

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
  // The payload carries the observation window as `window` (e.g. "7 DAY"); older
  // recs used `duration`. Normalise "7 DAY" → "7 days".
  const windowLabel =
    typeof rec.window === 'string' && rec.window ? rec.window.toLowerCase().replace(/\bday\b/, 'days') : `${rec.duration || '7'} days`;
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
    <Box sx={{ p: ds.space.mul(0, 7) }}>
      <SectionTitle title='Abandoned Workload Detection' muiIcon={<ReportProblemIcon sx={{ fontSize: ds.text.title }} />} />

      {/* Warning banner */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: ds.space.mul(0, 5),
          p: ds.space[3],
          backgroundColor: ds.amber[100],
          borderRadius: ds.radius.lg,
          border: `1px solid ${ds.amber[200]}`,
          mb: ds.space[3],
        }}
      >
        <WarningAmberIcon sx={{ fontSize: ds.text.title, color: ds.amber[700], mt: ds.space[0] }} />
        <Box>
          <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.amber[700], mb: ds.space[1] }}>
            Low Activity Detected
          </Typography>
          <Typography sx={{ fontSize: ds.text.small, color: ds.amber[700], lineHeight: 1.5 }}>
            {message || `This workload shows minimal network traffic over the last ${windowLabel}, suggesting it may be abandoned or unused.`}
          </Typography>
        </Box>
      </Box>

      {/* Resource info */}
      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
        }}
      >
        {resourceName && <MetricRow label='Resource' value={resourceName} />}
        {resourceType && <MetricRow label='Type' value={resourceType} />}
        {namespace && <MetricRow label='Namespace' value={namespace} />}
        {controller && <MetricRow label='Controller' value={`${controllerKind}/${controller}`} />}
        <MetricRow label='Observation Period' value={windowLabel} />
      </Box>

      {/* Traffic metric */}
      {traffic != null && (
        <>
          <SectionTitle title='Network Traffic' muiIcon={<InfoIcon sx={{ fontSize: ds.text.title }} />} />
          <Box
            sx={{
              backgroundColor: ds.gray[100],
              borderRadius: ds.radius.lg,
              p: ds.space[3],
              border: `1px solid ${ds.gray[200]}`,
              mb: ds.space[3],
            }}
          >
            <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: ds.space[2] }}>
              <Box>
                <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Current Traffic</Typography>
                <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.red[600] }}>
                  {Number(traffic).toLocaleString()} bytes
                </Typography>
              </Box>
              {threshold != null && (
                <Box sx={{ textAlign: 'right' }}>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Threshold</Typography>
                  <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                    {Number(threshold).toLocaleString()} bytes
                  </Typography>
                </Box>
              )}
            </Box>
            {trafficPct != null && (
              <Box>
                <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: ds.space[1] }}>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Traffic vs Threshold</Typography>
                  <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: ds.red[600] }}>
                    {trafficPct.toFixed(1)}%
                  </Typography>
                </Box>
                <LinearProgress
                  variant='determinate'
                  value={Math.min(trafficPct, 100)}
                  sx={{
                    height: '6px',
                    borderRadius: ds.radius.sm,
                    backgroundColor: ds.gray[200],
                    '& .MuiLinearProgress-bar': {
                      borderRadius: ds.radius.sm,
                      backgroundColor: trafficPct < 10 ? ds.red[600] : ds.amber[500],
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
          <SectionTitle title='Resource Usage' muiIcon={<BoltIcon sx={{ fontSize: ds.text.title }} />} />
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[2], mb: ds.space[3] }}>
            {cpu != null && (
              <Box
                sx={{
                  p: ds.space.mul(0, 5),
                  borderRadius: ds.radius.lg,
                  backgroundColor: ds.gray[100],
                  border: `1px solid ${ds.gray[200]}`,
                  borderLeft: `3px solid ${ds.blue[500]}`,
                }}
              >
                <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: ds.space[0] }}>CPU</Typography>
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], fontFamily: 'monospace' }}>
                  {Number(cpu).toFixed(3)} cores
                </Typography>
              </Box>
            )}
            {memory != null && (
              <Box
                sx={{
                  p: ds.space.mul(0, 5),
                  borderRadius: ds.radius.lg,
                  backgroundColor: ds.gray[100],
                  border: `1px solid ${ds.gray[200]}`,
                  borderLeft: `3px solid ${ds.purple[500]}`,
                }}
              >
                <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: ds.space[0] }}>Memory</Typography>
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], fontFamily: 'monospace' }}>
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
