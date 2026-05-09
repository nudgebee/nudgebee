import { Box, Typography, Chip, Link } from '@mui/material';
import { colors } from 'src/utils/colors';
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

  // Severity styling
  const severityColor = (sev: string): string => {
    const s = (sev || '').toLowerCase();
    if (s === 'critical') return '#991B1B';
    if (s === 'high') return '#DC2626';
    if (s === 'medium') return '#F59E0B';
    if (s === 'low') return '#3B82F6';
    return colors.text.tertiary;
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
            p: '12px',
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
          }}
        >
          {ruleId && (
            <Chip
              label={ruleId}
              size='small'
              sx={{
                fontFamily: 'Roboto Mono, monospace',
                fontSize: '11px',
                fontWeight: 700,
                color: colors.primary,
                backgroundColor: colors.background.primaryLightest,
                border: '1px solid #BFDBFE',
                flexShrink: 0,
              }}
            />
          )}
          <Box sx={{ flex: 1 }}>
            {ruleNameStr && (
              <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: '4px' }}>{ruleNameStr}</Typography>
            )}
            {ruleDescription && <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5 }}>{ruleDescription}</Typography>}
          </Box>
        </Box>
      )}

      {/* Severity + failure count */}
      <Box sx={{ display: 'flex', gap: '8px', mb: '12px' }}>
        {severity && (
          <Chip
            label={severity.toUpperCase()}
            size='small'
            sx={{
              fontSize: '10px',
              fontWeight: 700,
              height: '22px',
              color: severityColor(severity),
              backgroundColor: `${severityColor(severity)}15`,
              border: `1px solid ${severityColor(severity)}30`,
            }}
          />
        )}
        {failureCount != null && (
          <Chip
            label={`${failureCount} failure${failureCount !== 1 ? 's' : ''}`}
            size='small'
            sx={{
              fontSize: '10px',
              fontWeight: 600,
              height: '22px',
              color: failureCount > 0 ? '#DC2626' : '#16A34A',
              backgroundColor: failureCount > 0 ? colors.background.accordionSummay : colors.background.costBlock,
              border: `1px solid ${failureCount > 0 ? '#FECACA' : colors.lowestLight}`,
            }}
          />
        )}
      </Box>

      {/* Target resource */}
      {target && (
        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
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
              borderRadius: '8px',
              border: `1px solid ${colors.border.secondaryLight}`,
              maxHeight: '200px',
              overflow: 'auto',
            }}
          >
            {misconfigurations.map((item: any, idx: number) => (
              <Box
                key={item.ID || item.Message?.substring(0, 40) || `misconfig-${idx}`}
                sx={{
                  p: '10px 12px',
                  backgroundColor: idx % 2 === 0 ? '#FFFFFF' : colors.background.tertiaryLightestestest,
                  borderBottom: idx < misconfigurations.length - 1 ? `1px solid ${colors.border.secondaryLight}` : 'none',
                }}
              >
                {item.Message && (
                  <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5, mb: '4px' }}>{item.Message}</Typography>
                )}
                {item.Resolution && <Typography sx={{ fontSize: '11px', color: '#16A34A', lineHeight: 1.4 }}>Fix: {item.Resolution}</Typography>}
                {item.References && Array.isArray(item.References) && item.References.length > 0 && (
                  <Box sx={{ display: 'flex', gap: '6px', mt: '4px', flexWrap: 'wrap' }}>
                    {item.References.slice(0, 3).map((ref: string, refIdx: number) => (
                      <Link key={ref} href={ref} target='_blank' rel='noopener noreferrer' sx={{ fontSize: '10px', color: colors.primary }}>
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
        <Box sx={{ mt: '12px' }}>
          <Link
            href={`https://www.cisecurity.org/benchmark/kubernetes`}
            target='_blank'
            rel='noopener noreferrer'
            sx={{ fontSize: '12px', color: colors.primary }}
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
