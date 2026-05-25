import { Box, Typography, Chip, Link } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import SearchIcon from '@mui/icons-material/Search';
import BarChartIcon from '@mui/icons-material/BarChart';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface ImageScanEvidenceProps {
  recommendation: any;
  ruleName: string;
  estimatedSavings?: number;
}

const ImageScanEvidence = ({ recommendation, ruleName: _ruleName, estimatedSavings }: ImageScanEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec) return null;

  const image = rec.image || '';
  const packageId = rec.package_id || '';
  const vulnerabilityId = rec.vulnerability_id || '';
  const severity = rec.severity || '';
  const fixVersion = rec.fix_version || '';
  const description = rec.description || '';

  // Severity counts (from grouped view)
  const criticalCount = rec.count_severity_critical;
  const highCount = rec.count_severity_high;
  const mediumCount = rec.count_severity_medium;
  const lowCount = rec.count_severity_low;
  const hasCounts = criticalCount != null || highCount != null;

  // Severity color mapping
  const severityColor = (sev: string): string => {
    const s = sev.toLowerCase();
    if (s === 'critical') return '#991B1B';
    if (s === 'high') return '#DC2626';
    if (s === 'medium') return '#F59E0B';
    if (s === 'low') return '#3B82F6';
    return colors.text.tertiary;
  };

  const severityBg = (sev: string): string => {
    const s = sev.toLowerCase();
    if (s === 'critical') return colors.background.accordionSummay;
    if (s === 'high') return colors.background.accordionSummay;
    if (s === 'medium') return '#FEF3C7';
    if (s === 'low') return colors.background.primaryLightest;
    return colors.background.tertiaryLightestestest;
  };

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Container Image Vulnerability' muiIcon={<SearchIcon sx={{ fontSize: '16px' }} />} />

      {/* Vulnerability ID + Severity */}
      {(vulnerabilityId || severity) && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '10px',
            p: '12px',
            backgroundColor: severityBg(severity),
            borderRadius: '8px',
            border: `1px solid ${severityColor(severity)}30`,
            mb: '12px',
          }}
        >
          {vulnerabilityId && (
            <Link
              href={`https://nvd.nist.gov/vuln/detail/${vulnerabilityId}`}
              target='_blank'
              rel='noopener noreferrer'
              sx={{
                fontSize: '14px',
                fontWeight: 700,
                color: severityColor(severity),
                fontFamily: 'Roboto Mono, monospace',
                textDecoration: 'none',
                '&:hover': { textDecoration: 'underline' },
              }}
            >
              {vulnerabilityId}
            </Link>
          )}
          {severity && (
            <Chip
              label={severity.toUpperCase()}
              size='small'
              sx={{
                fontSize: '10px',
                fontWeight: 700,
                height: '20px',
                color: severityColor(severity),
                backgroundColor: `${severityColor(severity)}15`,
                border: `1px solid ${severityColor(severity)}30`,
              }}
            />
          )}
        </Box>
      )}

      {/* Description */}
      {description && (
        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
          }}
        >
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6 }}>
            {description.replace(/\[b\]|\[\/b\]/g, '')}
          </Typography>
        </Box>
      )}

      {/* Severity counts (grouped view) */}
      {hasCounts && (
        <>
          <SectionTitle title='Vulnerability Counts' muiIcon={<BarChartIcon sx={{ fontSize: '16px' }} />} />
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr 1fr', gap: '6px', mb: '12px' }}>
            <SeverityCountBox label='Critical' count={criticalCount || 0} color='#991B1B' />
            <SeverityCountBox label='High' count={highCount || 0} color='#DC2626' />
            <SeverityCountBox label='Medium' count={mediumCount || 0} color='#F59E0B' />
            <SeverityCountBox label='Low' count={lowCount || 0} color='#3B82F6' />
          </Box>
        </>
      )}

      {/* Vulnerability details */}
      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '12px',
          border: `1px solid ${colors.border.secondaryLight}`,
          mb: '12px',
        }}
      >
        {image && <MetricRow label='Image' value={image} />}
        {packageId && <MetricRow label='Package' value={packageId} />}
        {vulnerabilityId && <MetricRow label='CVE ID' value={vulnerabilityId} />}
        {severity && <MetricRow label='Severity' value={severity.toUpperCase()} />}
        {fixVersion && <MetricRow label='Fix Version' value={fixVersion} highlight />}
      </Box>

      {/* Fix available */}
      {fixVersion && (
        <Box sx={{ backgroundColor: colors.background.costBlock, borderRadius: '8px', p: '12px', border: '1px solid #BBF7D0', mb: '12px' }}>
          <Typography sx={{ fontSize: '12px', fontWeight: 600, color: '#166534', mb: '4px' }}>Fix Available</Typography>
          <Typography sx={{ fontSize: '12px', color: '#15803D', lineHeight: 1.5 }}>
            Update package <strong>{packageId}</strong> to version <strong>{fixVersion}</strong> or later to resolve this vulnerability.
          </Typography>
        </Box>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const SeverityCountBox = ({ label, count, color }: { label: string; count: number; color: string }) => (
  <Box
    sx={{
      textAlign: 'center',
      p: '8px',
      borderRadius: '6px',
      backgroundColor: count > 0 ? color + '10' : colors.background.tertiaryLightestestest,
      border: '1px solid ' + (count > 0 ? color + '30' : colors.border.secondaryLight),
    }}
  >
    <Typography sx={{ fontSize: '18px', fontWeight: 700, color: count > 0 ? color : colors.text.tertiary }}>{count}</Typography>
    <Typography sx={{ fontSize: '9px', color: colors.text.tertiary, textTransform: 'uppercase' }}>{label}</Typography>
  </Box>
);

export default ImageScanEvidence;
