import { Box, Typography, Link } from '@mui/material';
import { ds } from 'src/utils/colors';
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

        {rec.Title && (
          <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>{rec.Title}</Typography>
        )}

        {rec.Description && (
          <Box
            sx={{
              backgroundColor: ds.gray[100],
              borderRadius: ds.radius.lg,
              p: ds.space[3],
              border: `1px solid ${ds.gray[200]}`,
              mb: ds.space[3],
            }}
          >
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6 }}>{rec.Description}</Typography>
          </Box>
        )}

        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: '10px',
            border: `1px solid ${ds.gray[200]}`,
            mb: ds.space[3],
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
            <Box sx={{ backgroundColor: ds.green[100], borderRadius: ds.radius.lg, p: ds.space[3], border: `1px solid ${ds.green[200]}` }}>
              {remediationText && (
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6, mb: remediationUrl ? ds.space[2] : 0 }}>
                  {remediationText}
                </Typography>
              )}
              {remediationUrl && (
                <Link
                  href={remediationUrl}
                  target='_blank'
                  rel='noopener noreferrer'
                  sx={{ fontSize: ds.text.small, color: ds.blue[600], display: 'block' }}
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
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
        }}
      >
        {rec.reason && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6, mb: ds.space[2] }}>{rec.reason}</Typography>}
        {rec.service_name && <MetricRow label='Service' value={rec.service_name} />}
        {rec.image && <MetricRow label='Image' value={rec.image} />}
        {rec.package_id && <MetricRow label='Package' value={rec.package_id} />}
        {rec.vulnerability_id && <MetricRow label='Vulnerability' value={rec.vulnerability_id} />}
        {rec.severity && <MetricRow label='Severity' value={rec.severity} />}
        {rec.fix_version && <MetricRow label='Fix Version' value={rec.fix_version} highlight />}
        {rec.description && (
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.5, mt: ds.space[2] }}>
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
    <Box sx={{ mt: ds.space[2] }}>
      {remaining.slice(0, 6).map(([key, value]) => (
        <MetricRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </Box>
  );
}

export default SecurityEvidence;
