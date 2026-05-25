import { Box, Typography, LinearProgress } from '@mui/material';
import { colors } from 'src/utils/colors';
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
        color: '#DC2626',
        bg: colors.background.accordionSummay,
        border: '#FECACA',
        label: isExpired ? 'EXPIRED' : 'CRITICAL',
        Icon: ErrorOutlineIcon,
      };
    }
    if (isWarning) {
      return { color: '#F59E0B', bg: '#FEF3C7', border: '#FDE68A', label: 'WARNING', Icon: WarningAmberIcon };
    }
    return { color: '#16A34A', bg: colors.background.costBlock, border: colors.lowestLight, label: 'OK', Icon: CheckCircleOutlineIcon };
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
          gap: '12px',
          p: '14px',
          backgroundColor: urgencyBg,
          borderRadius: '8px',
          border: `1px solid ${urgencyBorder}`,
          mb: '12px',
        }}
      >
        <UrgencyIcon sx={{ fontSize: '28px', color: urgencyColor }} />
        <Box sx={{ flex: 1 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', mb: '4px' }}>
            <Typography sx={{ fontSize: '14px', fontWeight: 700, color: urgencyColor }}>{urgencyLabel}</Typography>
          </Box>
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5 }}>
            {isExpired && `Certificate "${certName}" has expired. Immediate renewal is required.`}
            {!isExpired &&
              daysUntilExpiry != null &&
              `Certificate "${certName}" expires in ${daysUntilExpiry} day${daysUntilExpiry !== 1 ? 's' : ''}.`}
            {!isExpired && daysUntilExpiry == null && `Certificate "${certName}" expiry status.`}
          </Typography>
        </Box>
        {daysUntilExpiry != null && !isExpired && (
          <Box sx={{ textAlign: 'center', minWidth: '60px' }}>
            <Typography sx={{ fontSize: '24px', fontWeight: 700, color: urgencyColor }}>{daysUntilExpiry}</Typography>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>days left</Typography>
          </Box>
        )}
      </Box>

      {/* Countdown bar */}
      {countdownValue != null && !isExpired && (
        <Box sx={{ mb: '12px' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: '4px' }}>
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Time Remaining</Typography>
            <Typography sx={{ fontSize: '11px', fontWeight: 600, color: urgencyColor }}>
              {daysUntilExpiry} / {countdownMax} days
            </Typography>
          </Box>
          <LinearProgress
            variant='determinate'
            value={(countdownValue / countdownMax) * 100}
            sx={{
              height: '8px',
              borderRadius: '4px',
              backgroundColor: colors.border.secondaryLightest,
              '& .MuiLinearProgress-bar': {
                borderRadius: '4px',
                backgroundColor: urgencyColor,
              },
            }}
          />
        </Box>
      )}

      {/* Certificate details */}
      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '12px',
          border: `1px solid ${colors.border.secondaryLight}`,
          mb: '12px',
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
      <Box sx={{ backgroundColor: colors.background.costBlock, borderRadius: '8px', p: '12px', border: '1px solid #BBF7D0' }}>
        <Typography sx={{ fontSize: '12px', fontWeight: 600, color: '#166534', mb: '4px' }}>Recommendation</Typography>
        <Typography sx={{ fontSize: '12px', color: '#15803D', lineHeight: 1.5 }}>
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
