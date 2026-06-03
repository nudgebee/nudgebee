import { Box, Typography, Link } from '@mui/material';
import { ds } from 'src/utils/colors';
import { Label } from '@components1/ds/Label';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import ShieldIcon from '@mui/icons-material/Shield';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface CISSecurityEvidenceProps {
  recommendation: any;
  ruleName: string;
  estimatedSavings?: number;
}

const CISSecurityEvidence = ({ recommendation, ruleName: _ruleName, estimatedSavings }: CISSecurityEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec) return null;

  const ruleId = rec.rule_id || '';
  const ruleNameStr = rec.rule_name || '';
  const ruleDescription = rec.rule_description || '';
  const severity = rec.severity || '';
  const failureCount = rec.count;

  // Nested misconfiguration details
  const target = rec.Target || '';
  const misconfigurations = rec.Misconfigurations || rec.misconfigurations || [];

  // Severity tone
  const severityTone = (sev: string): 'critical' | 'warning' | 'info' | 'neutral' => {
    const s = (sev || '').toLowerCase();
    if (s === 'critical') return 'critical';
    if (s === 'high') return 'critical';
    if (s === 'medium') return 'warning';
    if (s === 'low') return 'info';
    return 'neutral';
  };

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='CIS Security Benchmark' muiIcon={<ShieldIcon sx={{ fontSize: '16px' }} />} />

      {/* Rule header */}
      {(ruleId || ruleNameStr) && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: '10px',
            p: ds.space[3],
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.gray[200]}`,
            mb: ds.space[3],
          }}
        >
          {ruleId && (
            <Label size='sm' tone='info'>
              {ruleId}
            </Label>
          )}
          <Box sx={{ flex: 1 }}>
            {ruleNameStr && (
              <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[1] }}>
                {ruleNameStr}
              </Typography>
            )}
            {ruleDescription && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.5 }}>{ruleDescription}</Typography>}
          </Box>
        </Box>
      )}

      {/* Severity + failure count */}
      <Box sx={{ display: 'flex', gap: ds.space[2], mb: ds.space[3] }}>
        {severity && (
          <Label size='sm' tone={severityTone(severity)}>
            {severity.toUpperCase()}
          </Label>
        )}
        {failureCount != null && (
          <Label size='sm' tone={failureCount > 0 ? 'critical' : 'success'}>
            {`${failureCount} failure${failureCount !== 1 ? 's' : ''}`}
          </Label>
        )}
      </Box>

      {/* Target resource */}
      {target && (
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: ds.space[3],
            border: `1px solid ${ds.gray[200]}`,
            mb: ds.space[3],
          }}
        >
          <MetricRow label='Target' value={target} />
        </Box>
      )}

      {/* Misconfigurations detail */}
      {Array.isArray(misconfigurations) && misconfigurations.length > 0 && (
        <>
          <SectionTitle title={`Issues (${misconfigurations.length})`} muiIcon={<WarningAmberIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              borderRadius: ds.radius.lg,
              border: `1px solid ${ds.gray[200]}`,
              maxHeight: '200px',
              overflow: 'auto',
            }}
          >
            {misconfigurations.map((item: any, idx: number) => (
              <Box
                key={item.ID || item.Message?.substring(0, 40) || `misconfig-${idx}`}
                sx={{
                  p: '10px 12px',
                  backgroundColor: idx % 2 === 0 ? ds.background[100] : ds.gray[100],
                  borderBottom: idx < misconfigurations.length - 1 ? `1px solid ${ds.gray[200]}` : 'none',
                }}
              >
                {item.Message && (
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.5, mb: ds.space[1] }}>{item.Message}</Typography>
                )}
                {item.Resolution && (
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.green[600], lineHeight: 1.4 }}>Fix: {item.Resolution}</Typography>
                )}
                {item.References && Array.isArray(item.References) && item.References.length > 0 && (
                  <Box sx={{ display: 'flex', gap: '6px', mt: ds.space[1], flexWrap: 'wrap' }}>
                    {item.References.slice(0, 3).map((ref: string, refIdx: number) => (
                      <Link key={ref} href={ref} target='_blank' rel='noopener noreferrer' sx={{ fontSize: ds.text.caption, color: ds.blue[600] }}>
                        Reference {refIdx + 1}
                      </Link>
                    ))}
                  </Box>
                )}
              </Box>
            ))}
          </Box>
        </>
      )}

      {/* CIS benchmark link */}
      {ruleId && (
        <Box sx={{ mt: ds.space[3] }}>
          <Link
            href={`https://www.cisecurity.org/benchmark/kubernetes`}
            target='_blank'
            rel='noopener noreferrer'
            sx={{ fontSize: ds.text.small, color: ds.blue[600] }}
          >
            View CIS Kubernetes Benchmark →
          </Link>
        </Box>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

export default CISSecurityEvidence;
