import PropTypes from 'prop-types';
import { Tooltip } from '@mui/material';
import Chip from '@components1/ds/Chip';

const NewIssueChip = ({ firstSeenAt }) => {
  const title = `First seen: ${firstSeenAt ? new Date(firstSeenAt).toLocaleString() : 'within 7 days'}`;
  return (
    <Tooltip title={title}>
      <span>
        <Chip variant='tag' tone='success' size='xs' data-testid='new-issue-chip'>
          NEW
        </Chip>
      </span>
    </Tooltip>
  );
};

NewIssueChip.propTypes = {
  firstSeenAt: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
};

export default NewIssueChip;
