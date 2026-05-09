import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { formatRuleName } from './utils';

interface TopIssueItem {
  rule_name: string;
  count: number;
}

interface TopIssuesBarProps {
  items: TopIssueItem[];
  totalCount: number;
  severityLabel: string;
  activeRuleName: string | null;
  topIssuesActive: boolean;
  onRuleClick: (ruleName: string | null) => void;
  onToggleTopIssues: () => void;
  loading: boolean;
}

const TopIssuesBar = ({
  items,
  totalCount,
  severityLabel,
  activeRuleName,
  topIssuesActive,
  onRuleClick,
  onToggleTopIssues,
  loading,
}: TopIssuesBarProps) => {
  if (loading || items.length === 0) return null;

  const isAllSelected = topIssuesActive && !activeRuleName;

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: '12px', mb: '12px', flexWrap: 'wrap' }} data-testid='top-issues-bar'>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '6px' }}>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontWeight: 600, letterSpacing: '0.5px', textTransform: 'uppercase' }}>
          Top Issues
        </Typography>
        <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, fontWeight: 400 }}>({severityLabel})</Typography>
      </Box>

      {/* All chip */}
      <Box
        onClick={onToggleTopIssues}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: '6px',
          px: '12px',
          py: '4px',
          borderRadius: '16px',
          backgroundColor: isAllSelected ? colors.background.primaryLightest : colors.background.tertiaryLightestest,
          color: isAllSelected ? colors.text.secondary : colors.background.tertiary,
          border: `1px solid ${isAllSelected ? colors.border.primary : '#DDDDDD'}`,
          fontWeight: isAllSelected ? 600 : 400,
          cursor: 'pointer',
          fontSize: '12px',
          transition: 'all 0.15s ease',
          '&:hover': {
            borderColor: '#999999',
            color: isAllSelected ? '#FFFFFF' : '#333333',
          },
        }}
      >
        <span>All</span>
        <span style={{ fontWeight: 500, fontSize: '10px' }}>{totalCount.toLocaleString()}</span>
      </Box>

      {items.map((item) => {
        const isActive = topIssuesActive && activeRuleName === item.rule_name;
        return (
          <Box
            key={item.rule_name}
            onClick={() => onRuleClick(item.rule_name)}
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '4px',
              px: '12px',
              py: '4px',
              borderRadius: '16px',
              backgroundColor: isActive ? colors.background.primaryLightest : colors.background.tertiaryLightestest,
              color: isActive ? colors.text.secondary : colors.background.tertiary,
              border: `1px solid ${isActive ? colors.border.primary : '#DDDDDD'}`,
              fontWeight: isActive ? 600 : 400,
              cursor: 'pointer',
              fontSize: '11px',
              transition: 'all 0.15s ease',
              '&:hover': {
                borderColor: isActive ? colors.border.primary : '#999999',
                color: isActive ? colors.text.secondary : '#333333',
              },
            }}
          >
            <span>{formatRuleName(item.rule_name)}</span>
            <span style={{ fontWeight: 500, fontSize: '10px' }}>{item.count.toLocaleString()}</span>
          </Box>
        );
      })}
    </Box>
  );
};

export default TopIssuesBar;
