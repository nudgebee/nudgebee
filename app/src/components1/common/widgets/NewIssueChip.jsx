import { Chip, Tooltip } from '@mui/material';

const NewIssueChip = ({ firstSeenAt }) => {
  return (
    <Tooltip title={`First seen: ${firstSeenAt ? new Date(firstSeenAt).toLocaleString() : 'within 7 days'}`}>
      <Chip
        label='NEW'
        size='small'
        sx={{
          height: '18px',
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          backgroundColor: 'var(--ds-green-100)',
          color: 'var(--ds-green-600)',
          '& .MuiChip-label': { px: 'var(--ds-space-1)' },
        }}
      />
    </Tooltip>
  );
};

export default NewIssueChip;
