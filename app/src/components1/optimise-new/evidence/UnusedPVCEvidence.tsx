import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import { Label } from '@components1/ds/Label';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import InventoryIcon from '@mui/icons-material/Inventory';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface UnusedPVCEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
  cloudResource?: any;
}

const UnusedPVCEvidence = ({ recommendation, estimatedSavings, cloudResource }: UnusedPVCEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec) return null;

  const name = rec.metadata?.name || cloudResource?.name || '';
  const namespace = rec.spec?.claimRef?.namespace || cloudResource?.meta?.namespace || '';
  const claimName = rec.spec?.claimRef?.name || '';
  const storage = rec.spec?.capacity?.storage || '';
  const createdAt = rec.metadata?.creationTimestamp;

  // Calculate idle duration
  let idleDays: number | null = null;
  if (createdAt) {
    const created = new Date(createdAt);
    const now = new Date();
    idleDays = Math.floor((now.getTime() - created.getTime()) / (1000 * 60 * 60 * 24));
  }

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Unused Persistent Volume Claim' muiIcon={<InventoryIcon sx={{ fontSize: '16px' }} />} />

      {/* Warning banner */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: '10px',
          p: ds.space[3],
          backgroundColor: ds.amber[100],
          borderRadius: ds.radius.lg,
          border: `1px solid ${ds.amber[200]}`,
          mb: ds.space[3],
        }}
      >
        <WarningAmberIcon sx={{ fontSize: '18px', color: ds.amber[700], mt: '1px' }} />
        <Box>
          <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.amber[700], mb: ds.space[1] }}>
            Unbound Volume Detected
          </Typography>
          <Typography sx={{ fontSize: ds.text.small, color: ds.amber[700], lineHeight: 1.5 }}>
            This PVC is not bound to any running pod. It continues to consume storage resources and incur costs.
            {idleDays != null && idleDays > 0 && ` It has been idle for approximately ${idleDays} days.`}
          </Typography>
        </Box>
      </Box>

      {/* Volume details */}
      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
        }}
      >
        {name && <MetricRow label='Volume Name' value={name} />}
        {namespace && <MetricRow label='Last Namespace' value={namespace} />}
        {claimName && <MetricRow label='Last Claim' value={claimName} />}
        {storage && <MetricRow label='Size' value={storage} />}
        {createdAt && <MetricRow label='Created' value={new Date(createdAt).toLocaleDateString()} />}
        {idleDays != null && (
          <MetricRow label='Idle Duration' value={<Label size='sm' tone={idleDays > 30 ? 'critical' : 'warning'}>{`${idleDays} days`}</Label>} />
        )}
      </Box>

      {/* Recommendation */}
      <Box sx={{ backgroundColor: ds.green[100], borderRadius: ds.radius.lg, p: ds.space[3], border: `1px solid ${ds.green[200]}` }}>
        <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.green[700], mb: ds.space[1] }}>
          Recommendation
        </Typography>
        <Typography sx={{ fontSize: ds.text.small, color: ds.green[700], lineHeight: 1.5 }}>
          Consider deleting this unused PVC to free up storage resources and reduce costs. Verify that no workload requires this volume before
          proceeding.
        </Typography>
      </Box>

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

export default UnusedPVCEvidence;
