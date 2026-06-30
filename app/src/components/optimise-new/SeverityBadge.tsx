import { Chip } from '@mui/material';
import { ds } from 'src/utils/colors';

export type SeverityLevel = 'Critical' | 'High' | 'Medium' | 'Low' | 'Info';

const severityConfig: Record<SeverityLevel, { bg: string; color: string; border: string }> = {
  Critical: { bg: ds.red[200], color: ds.red[700], border: ds.red[200] },
  High: { bg: ds.red[100], color: ds.red[500], border: ds.red[300] },
  Medium: { bg: ds.yellow[200], color: ds.amber[700], border: ds.yellow[300] },
  Low: { bg: ds.blue[200], color: ds.blue[700], border: ds.blue[300] },
  Info: { bg: ds.gray[100], color: ds.brand[500], border: ds.gray[300] },
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
        fontWeight: ds.weight.semibold,
        fontSize: size === 'small' ? ds.text.caption : ds.text.small,
        height: size === 'small' ? '22px' : '28px',
        letterSpacing: '0.02em',
        '& .MuiChip-label': {
          px: size === 'small' ? ds.space[2] : '10px',
        },
      }}
    />
  );
};

export default SeverityBadge;
