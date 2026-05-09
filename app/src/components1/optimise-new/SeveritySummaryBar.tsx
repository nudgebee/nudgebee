import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { type SeverityLevel } from './SeverityBadge';

export interface SeveritySummaryData {
  severity: SeverityLevel;
  count: number;
  savings: number;
}

interface SeveritySummaryBarProps {
  data: SeveritySummaryData[];
  loading: boolean;
  onSeverityClick?: (severity: SeverityLevel | null) => void;
  activeSeverity?: SeverityLevel | null;
}

const severityChipConfig: Record<SeverityLevel, { dot: string; selectedBg: string; selectedBorder: string }> = {
  Critical: { dot: colors.error, selectedBg: colors.background.activeTab, selectedBorder: colors.error },
  High: { dot: colors.text.orangeLabel, selectedBg: colors.background.orangeLabel, selectedBorder: colors.text.orangeLabel },
  Medium: { dot: colors.yellow, selectedBg: colors.background.yellowLabel, selectedBorder: colors.yellow },
  Low: { dot: colors.primary, selectedBg: colors.background.blueChip, selectedBorder: colors.primary },
  Info: { dot: colors.tertiary, selectedBg: colors.background.tertiaryLightest, selectedBorder: colors.tertiary },
};

const ShimmerChips = () => (
  <>
    {[1, 2, 3, 4, 5].map((i) => (
      <Box
        key={i}
        sx={{
          width: '100px',
          height: '26px',
          borderRadius: '16px',
          backgroundColor: colors.background.shimmerBase,
          animation: 'pulse 1.5s ease-in-out infinite',
          '@keyframes pulse': { '0%, 100%': { opacity: 1 }, '50%': { opacity: 0.5 } },
        }}
      />
    ))}
  </>
);

const SeveritySummaryBar = ({ data, loading, onSeverityClick, activeSeverity }: SeveritySummaryBarProps) => {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: '12px', mb: '10px', mt: '12px' }} data-testid='severity-summary-bar'>
      <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontWeight: 600, letterSpacing: '0.5px', textTransform: 'uppercase' }}>
        Severity
      </Typography>
      {loading ? (
        <ShimmerChips />
      ) : (
        data.map((item) => {
          const config = severityChipConfig[item.severity];
          const isActive = activeSeverity === item.severity;
          return (
            <Box
              key={item.severity}
              onClick={() => onSeverityClick?.(isActive ? null : item.severity)}
              data-testid={`severity-chip-${item.severity.toLowerCase()}`}
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '4px',
                px: '12px',
                py: '4px',
                borderRadius: '16px',
                backgroundColor: isActive ? config.selectedBg : colors.background.tertiaryLightestest,
                border: `1px solid ${isActive ? config.selectedBorder : colors.border.secondaryLightest}`,
                cursor: 'pointer',
                fontSize: '11px',
                fontWeight: isActive ? 700 : 400,
                color: isActive ? colors.text.secondary : colors.text.tertiary,
                transition: 'all 0.15s ease',
                '&:hover': {
                  borderColor: isActive ? config.selectedBorder : colors.border.chipHover,
                  color: isActive ? colors.text.secondary : colors.text.dark,
                },
              }}
            >
              <Box sx={{ width: '6px', height: '6px', borderRadius: '50%', backgroundColor: config.dot, flexShrink: 0 }} />
              <span>{item.severity}</span>
              <span style={{ fontWeight: 500, fontSize: '10px' }}>{item.count.toLocaleString()}</span>
            </Box>
          );
        })
      )}
    </Box>
  );
};

export default SeveritySummaryBar;
