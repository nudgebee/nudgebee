import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import SettingsIcon from '@mui/icons-material/Settings';
import NotificationsIcon from '@mui/icons-material/Notifications';
import BuildIcon from '@mui/icons-material/Build';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface ConfigurationEvidenceProps {
  recommendation: any;
  ruleName: string;
  estimatedSavings?: number;
}

const ConfigurationEvidence = ({ recommendation, ruleName: _ruleName, estimatedSavings }: ConfigurationEvidenceProps) => {
  const rec = safeParseJSON(recommendation);

  // ─── Array-of-issues format (K8s misconfigurations) ───
  if (Array.isArray(rec)) {
    const grouped: Record<string, any[]> = {};
    rec.forEach((item: any) => {
      const cat = item.category || 'Other';
      if (!grouped[cat]) grouped[cat] = [];
      grouped[cat].push(item);
    });

    return (
      <Box sx={{ p: '14px' }}>
        <SectionTitle title={`Configuration Issues (${rec.length})`} muiIcon={<SettingsIcon sx={{ fontSize: '16px' }} />} />
        {Object.entries(grouped).map(([groupName, items]) => (
          <Box key={groupName} sx={{ mb: '12px' }}>
            <Typography
              sx={{
                fontSize: '11px',
                fontWeight: 600,
                color: '#6B7280',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                mb: '6px',
                mt: '8px',
              }}
            >
              {groupName} ({items.length})
            </Typography>
            <Box
              sx={{
                backgroundColor: colors.background.tertiaryLightestestest,
                borderRadius: '8px',
                overflow: 'hidden',
                border: `1px solid ${colors.border.secondaryLight}`,
              }}
            >
              {items.map((item: any, idx: number) => (
                <Box
                  key={item.name || item.message || `config-item-${idx}`}
                  sx={{
                    px: '12px',
                    py: '8px',
                    borderBottom: idx < items.length - 1 ? '1px solid #F3F4F6' : 'none',
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '2px',
                  }}
                >
                  <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5 }}>{item.message || 'Unknown issue'}</Typography>
                  {item.name && (
                    <Typography sx={{ fontSize: '11px', color: '#9CA3AF', fontFamily: 'Roboto Mono, monospace' }}>
                      {item.kind && `${item.kind}/`}
                      {item.name}
                    </Typography>
                  )}
                </Box>
              ))}
            </Box>
          </Box>
        ))}

        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  // ─── Single object format (cloud) ───
  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Configuration Details' muiIcon={<SettingsIcon sx={{ fontSize: '16px' }} />} />
      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '12px',
          border: `1px solid ${colors.border.secondaryLight}`,
        }}
      >
        {rec.reason && <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6, mb: '8px' }}>{rec.reason}</Typography>}
        {rec.service_name && <MetricRow label='Service' value={rec.service_name} />}
        {rec.alarm_type && <MetricRow label='Alarm Type' value={rec.alarm_type} />}
        {rec.threshold != null && <MetricRow label='Threshold' value={rec.threshold} />}
        {rec.load_balancer_name && <MetricRow label='Load Balancer' value={rec.load_balancer_name} />}
        {rec.instance_type && <MetricRow label='Instance Type' value={rec.instance_type} />}
        {rec.region && <MetricRow label='Region' value={rec.region} />}

        {rec.description && (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5, mt: '8px' }}>
            {rec.description.replace(/\[b\]|\[\/b\]/g, '')}
          </Typography>
        )}
        {rec.message && <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5, mt: '8px' }}>{rec.message}</Typography>}
      </Box>

      {/* Key-value grid for any remaining fields */}
      {renderRemainingFields(rec)}

      {/* Alarm Config section */}
      {rec.alarm_config && (
        <>
          <SectionTitle title='Alarm Configuration' muiIcon={<NotificationsIcon sx={{ fontSize: '16px' }} />} />
          <Box sx={{ backgroundColor: colors.background.primaryLightest, borderRadius: '8px', p: '12px', border: '1px solid #BFDBFE' }}>
            <Typography sx={{ fontSize: '12px', color: '#1E40AF', lineHeight: 1.6 }}>
              An alarm configuration is available for this recommendation. Use the action bar below to create a CloudWatch alarm.
            </Typography>
          </Box>
        </>
      )}

      {/* Remediation */}
      {rec.remediation && (
        <>
          <SectionTitle title='Remediation' muiIcon={<BuildIcon sx={{ fontSize: '16px' }} />} />
          <Box sx={{ backgroundColor: colors.background.costBlock, borderRadius: '8px', p: '12px', border: '1px solid #BBF7D0' }}>
            <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6 }}>{rec.remediation}</Typography>
          </Box>
        </>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const KNOWN_FIELDS = new Set([
  'reason',
  'service_name',
  'alarm_type',
  'threshold',
  'load_balancer_name',
  'instance_type',
  'region',
  'description',
  'message',
  'alarm_config',
  'remediation',
]);

function renderRemainingFields(rec: any) {
  const remaining = Object.entries(rec).filter(([key, value]) => !KNOWN_FIELDS.has(key) && value != null && typeof value !== 'object');
  if (remaining.length === 0) return null;

  return (
    <Box sx={{ mt: '8px' }}>
      {remaining.slice(0, 8).map(([key, value]) => (
        <MetricRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </Box>
  );
}

export default ConfigurationEvidence;
