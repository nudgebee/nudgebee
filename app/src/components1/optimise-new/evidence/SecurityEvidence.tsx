import { Box, Typography, Link } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import ShieldIcon from '@mui/icons-material/Shield';
import BuildIcon from '@mui/icons-material/Build';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface SecurityEvidenceProps {
  recommendation: any;
  ruleName: string;
  estimatedSavings?: number;
}

const SecurityEvidence = ({ recommendation, ruleName: _ruleName, estimatedSavings }: SecurityEvidenceProps) => {
  const rec = safeParseJSON(recommendation);

  // ─── AWS SecurityHub format ───
  if (rec.Title || rec.Description || rec.Remediation) {
    const remediationText = rec.Remediation?.Recommendation?.Text;
    const remediationUrl = rec.Remediation?.Recommendation?.Url;

    return (
      <Box sx={{ p: '14px' }}>
        <SectionTitle title='Security Finding' muiIcon={<ShieldIcon sx={{ fontSize: '16px' }} />} />

        {rec.Title && <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: '8px' }}>{rec.Title}</Typography>}

        {rec.Description && (
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '12px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '12px',
            }}
          >
            <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6 }}>{rec.Description}</Typography>
          </Box>
        )}

        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '10px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
          }}
        >
          {rec.ServiceName && <MetricRow label='Service' value={rec.ServiceName} />}
          {rec.Severity?.Label && <MetricRow label='Severity' value={rec.Severity.Label} />}
          {rec.Compliance?.Status && <MetricRow label='Compliance' value={rec.Compliance.Status} />}
          {rec.ProductName && <MetricRow label='Product' value={rec.ProductName} />}
          {rec.GeneratorId && <MetricRow label='Generator' value={rec.GeneratorId} />}
        </Box>

        {/* Remediation */}
        {(remediationText || remediationUrl) && (
          <>
            <SectionTitle title='Remediation' muiIcon={<BuildIcon sx={{ fontSize: '16px' }} />} />
            <Box sx={{ backgroundColor: colors.background.costBlock, borderRadius: '8px', p: '12px', border: '1px solid #BBF7D0' }}>
              {remediationText && (
                <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6, mb: remediationUrl ? '8px' : 0 }}>
                  {remediationText}
                </Typography>
              )}
              {remediationUrl && (
                <Link
                  href={remediationUrl}
                  target='_blank'
                  rel='noopener noreferrer'
                  sx={{ fontSize: '12px', color: colors.primary, display: 'block' }}
                >
                  View remediation guide →
                </Link>
              )}
            </Box>
          </>
        )}

        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  // ─── Generic security format ───
  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Security Details' muiIcon={<ShieldIcon sx={{ fontSize: '16px' }} />} />

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
        {rec.image && <MetricRow label='Image' value={rec.image} />}
        {rec.package_id && <MetricRow label='Package' value={rec.package_id} />}
        {rec.vulnerability_id && <MetricRow label='Vulnerability' value={rec.vulnerability_id} />}
        {rec.severity && <MetricRow label='Severity' value={rec.severity} />}
        {rec.fix_version && <MetricRow label='Fix Version' value={rec.fix_version} highlight />}
        {rec.description && (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5, mt: '8px' }}>
            {rec.description.replace(/\[b\]|\[\/b\]/g, '')}
          </Typography>
        )}
      </Box>

      {/* Render remaining key-value pairs */}
      {renderRemainingFields(rec)}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const KNOWN_SECURITY_FIELDS = new Set([
  'reason',
  'service_name',
  'description',
  'image',
  'package_id',
  'vulnerability_id',
  'severity',
  'fix_version',
  'Title',
  'Description',
  'ServiceName',
  'Remediation',
  'Severity',
  'Compliance',
  'ProductName',
  'GeneratorId',
]);

function renderRemainingFields(rec: any) {
  const remaining = Object.entries(rec).filter(([key, value]) => !KNOWN_SECURITY_FIELDS.has(key) && value != null && typeof value !== 'object');
  if (remaining.length === 0) return null;

  return (
    <Box sx={{ mt: '8px' }}>
      {remaining.slice(0, 6).map(([key, value]) => (
        <MetricRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </Box>
  );
}

export default SecurityEvidence;
