import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import FilterAltOutlinedIcon from '@mui/icons-material/FilterAltOutlined';
import AccountSelect from '@components/ownership/AccountSelect';
import { ds } from '@utils/colors';

/**
 * Inline "narrow to one account" filter for AI & Tools sub-tabs that have a
 * tenant-wide default (Agents, Tools & MCP, Functions) — same pattern as
 * b-Cortex's own header selector (BCortexModal.jsx, Decision S): a real
 * "All accounts" option via AccountSelect's `includeAllOption`, not a
 * required precondition. `required` drops the "All accounts" entry
 * entirely for the one sub-tab (RCA Format) that has no tenant-wide view.
 */
const AdminAccountFilter = ({ value, onChange, required }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], width: ds.space.mul(0, 120), flexShrink: 0 }}>
    <FilterAltOutlinedIcon sx={{ fontSize: 18, color: 'var(--ds-gray-500)', flexShrink: 0 }} />
    <Box sx={{ flex: 1, minWidth: 0 }}>
      {required ? (
        <AccountSelect value={value} onChange={onChange} placeholder='Select an account' />
      ) : (
        <AccountSelect value={value} onChange={onChange} includeAllOption allOptionLabel='All accounts' />
      )}
    </Box>
  </Box>
);

AdminAccountFilter.propTypes = {
  value: PropTypes.string,
  onChange: PropTypes.func.isRequired,
  required: PropTypes.bool,
};

export default AdminAccountFilter;
