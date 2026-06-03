import { Box, Typography, Link } from '@mui/material';
import { ds } from 'src/utils/colors';
import { Label } from '@components1/ds/Label';
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
    if (s === 'critical') return ds.red[700];
    if (s === 'high') return ds.red[600];
    if (s === 'medium') return ds.amber[500];
    if (s === 'low') return ds.blue[500];
    return ds.gray[500];
  };

  const severityTone = (sev: string): 'critical' | 'warning' | 'info' | 'neutral' => {
    const s = sev.toLowerCase();
    if (s === 'critical') return 'critical';
    if (s === 'high') return 'critical';
    if (s === 'medium') return 'warning';
    if (s === 'low') return 'info';
    return 'neutral';
  };

  const severityBg = (sev: string): string => {
    const s = sev.toLowerCase();
    if (s === 'critical') return ds.red[100];
    if (s === 'high') return ds.red[100];
    if (s === 'medium') return ds.amber[100];
    if (s === 'low') return ds.blue[100];
    return ds.gray[100];
  };

  const severityBorder = (sev: string): string => {
    const s = sev.toLowerCase();
    if (s === 'critical' || s === 'high') return ds.red[200];
    if (s === 'medium') return ds.amber[200];
    if (s === 'low') return ds.blue[200];
    return ds.gray[200];
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
            p: ds.space[3],
            backgroundColor: severityBg(severity),
            borderRadius: ds.radius.lg,
            border: `1px solid ${severityBorder(severity)}`,
            mb: ds.space[3],
          }}
        >
          {vulnerabilityId && (
            <Link
              href={`https://nvd.nist.gov/vuln/detail/${vulnerabilityId}`}
              target='_blank'
              rel='noopener noreferrer'
              sx={{
                fontSize: ds.text.bodyLg,
                fontWeight: ds.weight.semibold,
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
            <Label size='sm' tone={severityTone(severity)}>
              {severity.toUpperCase()}
            </Label>
          )}
        </Box>
      )}

      {/* Description */}
      {description && (
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: ds.space[3],
            border: `1px solid ${ds.gray[200]}`,
            mb: ds.space[3],
          }}
        >
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6 }}>{description.replace(/\[b\]|\[\/b\]/g, '')}</Typography>
        </Box>
      )}

      {/* Severity counts (grouped view) */}
      {hasCounts && (
        <>
          <SectionTitle title='Vulnerability Counts' muiIcon={<BarChartIcon sx={{ fontSize: '16px' }} />} />
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr 1fr', gap: '6px', mb: ds.space[3] }}>
            <SeverityCountBox label='Critical' count={criticalCount || 0} variant='critical' />
            <SeverityCountBox label='High' count={highCount || 0} variant='high' />
            <SeverityCountBox label='Medium' count={mediumCount || 0} variant='medium' />
            <SeverityCountBox label='Low' count={lowCount || 0} variant='low' />
          </Box>
        </>
      )}

      {/* Vulnerability details */}
      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
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
        <Box
          sx={{ backgroundColor: ds.green[100], borderRadius: ds.radius.lg, p: ds.space[3], border: `1px solid ${ds.green[200]}`, mb: ds.space[3] }}
        >
          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.green[700], mb: ds.space[1] }}>
            Fix Available
          </Typography>
          <Typography sx={{ fontSize: ds.text.small, color: ds.green[700], lineHeight: 1.5 }}>
            Update package <strong>{packageId}</strong> to version <strong>{fixVersion}</strong> or later to resolve this vulnerability.
          </Typography>
        </Box>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

type SeverityCountVariant = 'critical' | 'high' | 'medium' | 'low';
const SEVERITY_COUNT_TONE: Record<SeverityCountVariant, { text: string; bg: string; border: string }> = {
  critical: { text: ds.red[700], bg: ds.red[100], border: ds.red[200] },
  high: { text: ds.red[600], bg: ds.red[100], border: ds.red[200] },
  medium: { text: ds.amber[500], bg: ds.amber[100], border: ds.amber[200] },
  low: { text: ds.blue[500], bg: ds.blue[100], border: ds.blue[200] },
};

const SeverityCountBox = ({ label, count, variant }: { label: string; count: number; variant: SeverityCountVariant }) => {
  const tone = SEVERITY_COUNT_TONE[variant];
  const active = count > 0;
  return (
    <Box
      sx={{
        textAlign: 'center',
        p: ds.space[2],
        borderRadius: ds.radius.md,
        backgroundColor: active ? tone.bg : ds.gray[100],
        border: `1px solid ${active ? tone.border : ds.gray[200]}`,
      }}
    >
      <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: active ? tone.text : ds.gray[500] }}>{count}</Typography>
      <Typography sx={{ fontSize: '9px', color: ds.gray[500], textTransform: 'uppercase' }}>{label}</Typography>
    </Box>
  );
};

export default ImageScanEvidence;
