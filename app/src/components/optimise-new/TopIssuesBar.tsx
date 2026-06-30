import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
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
    <Box
      sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)', mb: 'var(--ds-space-3)', flexWrap: 'wrap' }}
      data-testid='top-issues-bar'
    >
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-1)' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: ds.gray[600],
            fontWeight: 'var(--ds-font-weight-semibold)',
            letterSpacing: '0.5px',
            textTransform: 'uppercase',
          }}
        >
          Top Issues
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], fontWeight: 'var(--ds-font-weight-regular)' }}>
          ({severityLabel})
        </Typography>
      </Box>

      {/* All chip */}
      <Box
        onClick={onToggleTopIssues}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          px: 'var(--ds-space-3)',
          py: 'var(--ds-space-1)',
          borderRadius: 'var(--ds-radius-xl)',
          backgroundColor: isAllSelected ? ds.blue[100] : ds.background[200],
          color: isAllSelected ? ds.gray[700] : ds.gray[600],
          border: `1px solid ${isAllSelected ? ds.blue[500] : 'var(--ds-gray-300)'}`,
          fontWeight: isAllSelected ? 600 : 400,
          cursor: 'pointer',
          fontSize: 'var(--ds-text-small)',
          transition: 'all 0.15s ease',
          '&:hover': {
            borderColor: 'var(--ds-gray-500)',
            color: isAllSelected ? ds.background[100] : ds.gray[700],
          },
        }}
      >
        <span>All</span>
        <span style={{ fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-caption)' }}>{totalCount.toLocaleString()}</span>
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
              gap: 'var(--ds-space-1)',
              px: 'var(--ds-space-3)',
              py: 'var(--ds-space-1)',
              borderRadius: 'var(--ds-radius-xl)',
              backgroundColor: isActive ? ds.blue[100] : ds.background[200],
              color: isActive ? ds.gray[700] : ds.gray[600],
              border: `1px solid ${isActive ? ds.blue[500] : 'var(--ds-gray-300)'}`,
              fontWeight: isActive ? 600 : 400,
              cursor: 'pointer',
              fontSize: 'var(--ds-text-caption)',
              transition: 'all 0.15s ease',
              '&:hover': {
                borderColor: isActive ? ds.blue[500] : 'var(--ds-gray-500)',
                color: isActive ? ds.gray[700] : 'var(--ds-brand-600)',
              },
            }}
          >
            <span>{formatRuleName(item.rule_name)}</span>
            <span style={{ fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-caption)' }}>{item.count.toLocaleString()}</span>
          </Box>
        );
      })}
    </Box>
  );
};

export default TopIssuesBar;
