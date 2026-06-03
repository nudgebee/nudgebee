import { Chip } from '@mui/material';
import { colors } from 'src/utils/colors';

export type SeverityLevel = 'Critical' | 'High' | 'Medium' | 'Low' | 'Info';

const severityConfig: Record<SeverityLevel, { bg: string; color: string; border: string }> = {
  Critical: { bg: colors.background.activeTab, color: colors.critical, border: colors.border.errorLight },
  High: { bg: colors.background.medium, color: colors.high, border: colors.border.paperOutline },
  Medium: { bg: colors.background.warningLightest, color: colors.text.warning, border: colors.background.warningButtonHover },
  Low: { bg: colors.background.anchorActiveTab, color: colors.text.infoDark, border: colors.border.primaryLight },
  Info: { bg: colors.background.tertiaryLight, color: colors.secondary.default, border: colors.border.secondaryLightest },
};

interface SeverityBadgeProps {
  severity: SeverityLevel;
  size?: 'small' | 'medium';
}

const SeverityBadge = ({ severity, size = 'small' }: SeverityBadgeProps) => {
  const config = severityConfig[severity] || severityConfig.Info;

  return (
    <Chip
      label={severity}
      size={size}
      data-testid={`severity-badge-${severity.toLowerCase()}`}
      sx={{
        backgroundColor: config.bg,
        color: config.color,
        border: `1px solid ${config.border}`,
        fontWeight: 600,
        fontSize: size === 'small' ? '11px' : '12px',
        height: size === 'small' ? '22px' : '28px',
        letterSpacing: '0.02em',
        '& .MuiChip-label': {
          px: size === 'small' ? '8px' : '10px',
        },
      }}
    />
  );
};

export default SeverityBadge;
