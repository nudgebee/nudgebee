import { Box, Typography, Chip } from '@mui/material';
import { colors } from 'src/utils/colors';
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
          p: '12px',
          backgroundColor: '#FEF3C7',
          borderRadius: '8px',
          border: '1px solid #FDE68A',
          mb: '12px',
        }}
      >
        <WarningAmberIcon sx={{ fontSize: '18px', color: '#92400E', mt: '1px' }} />
        <Box>
          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: '#92400E', mb: '4px' }}>Unbound Volume Detected</Typography>
          <Typography sx={{ fontSize: '12px', color: '#78350F', lineHeight: 1.5 }}>
            This PVC is not bound to any running pod. It continues to consume storage resources and incur costs.
            {idleDays != null && idleDays > 0 && ` It has been idle for approximately ${idleDays} days.`}
          </Typography>
        </Box>
      </Box>

      {/* Volume details */}
      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '12px',
          border: `1px solid ${colors.border.secondaryLight}`,
          mb: '12px',
        }}
      >
        {name && <MetricRow label='Volume Name' value={name} />}
        {namespace && <MetricRow label='Last Namespace' value={namespace} />}
        {claimName && <MetricRow label='Last Claim' value={claimName} />}
        {storage && <MetricRow label='Size' value={storage} />}
        {createdAt && <MetricRow label='Created' value={new Date(createdAt).toLocaleDateString()} />}
        {idleDays != null && (
          <MetricRow
            label='Idle Duration'
            value={
              <Chip
                label={`${idleDays} days`}
                size='small'
                sx={{
                  fontSize: '11px',
                  height: '20px',
                  backgroundColor: idleDays > 30 ? colors.background.accordionSummay : '#FEF3C7',
                  color: idleDays > 30 ? '#991B1B' : '#92400E',
                }}
              />
            }
          />
        )}
      </Box>

      {/* Recommendation */}
      <Box sx={{ backgroundColor: colors.background.costBlock, borderRadius: '8px', p: '12px', border: '1px solid #BBF7D0' }}>
        <Typography sx={{ fontSize: '12px', fontWeight: 600, color: '#166534', mb: '4px' }}>Recommendation</Typography>
        <Typography sx={{ fontSize: '12px', color: '#15803D', lineHeight: 1.5 }}>
          Consider deleting this unused PVC to free up storage resources and reduce costs. Verify that no workload requires this volume before
          proceeding.
        </Typography>
      </Box>

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

export default UnusedPVCEvidence;
