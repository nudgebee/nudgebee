import { Chip, Tooltip } from '@mui/material';

const NewIssueChip = ({ firstSeenAt }) => {
  return (
    <Tooltip title={`First seen: ${firstSeenAt ? new Date(firstSeenAt).toLocaleString() : 'within 7 days'}`}>
      <Chip
        label='NEW'
        size='small'
        sx={{
          height: '18px',
          fontSize: '10px',
          fontWeight: 600,
          backgroundColor: '#E8F5E9',
          color: '#2E7D32',
          '& .MuiChip-label': { px: '6px' },
        }}
      />
    </Tooltip>
  );
};

export default NewIssueChip;
