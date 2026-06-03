import { Box, Typography, LinearProgress } from '@mui/material';
import { ds } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import LockIcon from '@mui/icons-material/Lock';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface CertificateExpiryEvidenceProps {
  recommendation: any;
  estimatedSavings?: number;
}

const CertificateExpiryEvidence = ({ recommendation, estimatedSavings }: CertificateExpiryEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec) return null;

  const certName = rec.name || '';
  const namespace = rec.namespace || '';
  const expiryDate = rec.expiry_date;
  const daysUntilExpiry = rec.days_until_expiry;

  // Determine urgency
  const computeIsExpired = (): boolean => {
    if (daysUntilExpiry != null) return daysUntilExpiry <= 0;
    if (expiryDate) return new Date(expiryDate) < new Date();
    return false;
  };
  const isExpired = computeIsExpired();
  const isCritical = daysUntilExpiry != null ? daysUntilExpiry <= 7 : false;
  const isWarning = daysUntilExpiry != null ? daysUntilExpiry <= 30 : false;

  const getUrgencyTheme = () => {
    if (isExpired || isCritical) {
      return {
        color: ds.red[600],
        bg: ds.red[100],
        border: ds.red[200],
        label: isExpired ? 'EXPIRED' : 'CRITICAL',
        Icon: ErrorOutlineIcon,
      };
    }
    if (isWarning) {
      return { color: ds.amber[500], bg: ds.amber[100], border: ds.amber[200], label: 'WARNING', Icon: WarningAmberIcon };
    }
    return { color: ds.green[600], bg: ds.green[100], border: ds.green[200], label: 'OK', Icon: CheckCircleOutlineIcon };
  };
  const { color: urgencyColor, bg: urgencyBg, border: urgencyBorder, label: urgencyLabel, Icon: UrgencyIcon } = getUrgencyTheme();

  // Countdown bar (max 90 days)
  const countdownMax = 90;
  const countdownValue = daysUntilExpiry != null ? Math.max(0, Math.min(daysUntilExpiry, countdownMax)) : null;

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Certificate Expiry' muiIcon={<LockIcon sx={{ fontSize: '16px' }} />} />

      {/* Urgency banner */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[3],
          p: '14px',
          backgroundColor: urgencyBg,
          borderRadius: ds.radius.lg,
          border: `1px solid ${urgencyBorder}`,
          mb: ds.space[3],
        }}
      >
        <UrgencyIcon sx={{ fontSize: '28px', color: urgencyColor }} />
        <Box sx={{ flex: 1 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[1] }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: urgencyColor }}>{urgencyLabel}</Typography>
          </Box>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.5 }}>
            {isExpired && `Certificate "${certName}" has expired. Immediate renewal is required.`}
            {!isExpired &&
              daysUntilExpiry != null &&
              `Certificate "${certName}" expires in ${daysUntilExpiry} day${daysUntilExpiry !== 1 ? 's' : ''}.`}
            {!isExpired && daysUntilExpiry == null && `Certificate "${certName}" expiry status.`}
          </Typography>
        </Box>
        {daysUntilExpiry != null && !isExpired && (
          <Box sx={{ textAlign: 'center', minWidth: '60px' }}>
            <Typography sx={{ fontSize: '24px', fontWeight: ds.weight.semibold, color: urgencyColor }}>{daysUntilExpiry}</Typography>
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>days left</Typography>
          </Box>
        )}
      </Box>

      {/* Countdown bar */}
      {countdownValue != null && !isExpired && (
        <Box sx={{ mb: ds.space[3] }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: ds.space[1] }}>
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Time Remaining</Typography>
            <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.semibold, color: urgencyColor }}>
              {daysUntilExpiry} / {countdownMax} days
            </Typography>
          </Box>
          <LinearProgress
            variant='determinate'
            value={(countdownValue / countdownMax) * 100}
            sx={{
              height: '8px',
              borderRadius: ds.radius.sm,
              backgroundColor: ds.gray[200],
              '& .MuiLinearProgress-bar': {
                borderRadius: ds.radius.sm,
                backgroundColor: urgencyColor,
              },
            }}
          />
        </Box>
      )}

      {/* Certificate details */}
      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
        }}
      >
        {certName && <MetricRow label='Certificate Name' value={certName} />}
        {namespace && <MetricRow label='Namespace' value={namespace} />}
        {expiryDate && (
          <MetricRow
            label='Expiry Date'
            value={new Date(expiryDate).toLocaleDateString('en-US', {
              year: 'numeric',
              month: 'long',
              day: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
            })}
          />
        )}
        {daysUntilExpiry != null && (
          <MetricRow
            label='Days Until Expiry'
            value={isExpired ? `Expired ${Math.abs(daysUntilExpiry)} days ago` : `${daysUntilExpiry} days`}
            highlight
          />
        )}
      </Box>

      {/* Recommendation */}
      <Box sx={{ backgroundColor: ds.green[100], borderRadius: ds.radius.lg, p: ds.space[3], border: `1px solid ${ds.green[200]}` }}>
        <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.green[700], mb: ds.space[1] }}>
          Recommendation
        </Typography>
        <Typography sx={{ fontSize: ds.text.small, color: ds.green[700], lineHeight: 1.5 }}>
          {isExpired &&
            'Renew this certificate immediately to restore secure communications. Update the Kubernetes secret with the renewed certificate.'}
          {!isExpired && isCritical && 'Urgent: Renew this certificate within the next few days to avoid service disruption.'}
          {!isExpired && !isCritical && isWarning && 'Plan certificate renewal soon. Consider automating certificate management with cert-manager.'}
          {!isExpired && !isCritical && !isWarning && 'Certificate is valid. Monitor for upcoming expiry.'}
        </Typography>
      </Box>

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

export default CertificateExpiryEvidence;
