import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Switch } from '@ui/Switch';
import Tooltip from '@ui/Tooltip';
import { toast as snackbar } from '@ui/Toast';
import { hasWriteAccess } from '@lib/auth';

/**
 * One account's own on/off for an organisation agent or tool (#41073), like the
 * per-account switch of an organisation knowledge base: turning it off removes
 * it from this account only. Optimistic; `setOverride(accountId, id, enabled)`
 * returns { errors }. A selected-accounts item is changed by tenant admins only.
 */
const OrgAccountSwitch = ({ item, name, noun, accountId, tenantAdmin, setOverride, onChanged, testId }) => {
  const [on, setOn] = useState(!item.switched_off);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setOn(!item.switched_off);
  }, [item.switched_off]);

  const handleChange = async (_event, nextOn) => {
    setSaving(true);
    setOn(nextOn);
    try {
      const response = await setOverride(accountId, item.id, nextOn);
      if (response?.errors?.length > 0) {
        setOn(!nextOn);
        snackbar.error(response.errors[0]?.message || `Failed to update ${noun}`);
        return;
      }
      snackbar.success(nextOn ? `"${name}" is used in this account again` : `"${name}" is off for this account; other accounts keep it`);
      onChanged?.();
    } catch (error) {
      setOn(!nextOn);
      console.error(`Error updating ${noun} account switch:`, error);
      snackbar.error(`An error occurred while updating the ${noun}`);
    } finally {
      setSaving(false);
    }
  };

  const selectedOnly = item.visibility === 'selected_accounts' && !tenantAdmin;
  return (
    <Tooltip
      title={
        selectedOnly
          ? 'Shared with selected accounts by your organisation. Only a tenant admin can change which accounts use it.'
          : 'Shared by your organisation. Turning this off removes it from this account only; other accounts keep it.'
      }
      placement='top'
    >
      <Box component='span' sx={{ display: 'inline-flex' }}>
        <Switch
          size='sm'
          checked={on}
          loading={saving}
          disabled={!hasWriteAccess(accountId) || selectedOnly}
          onChange={handleChange}
          aria-label={`${on ? 'Stop using' : 'Use'} ${noun} ${name} in this account`}
          data-testid={testId}
        />
      </Box>
    </Tooltip>
  );
};

OrgAccountSwitch.propTypes = {
  item: PropTypes.shape({ id: PropTypes.string, visibility: PropTypes.string, switched_off: PropTypes.bool }).isRequired,
  name: PropTypes.string.isRequired,
  noun: PropTypes.string.isRequired,
  accountId: PropTypes.string.isRequired,
  tenantAdmin: PropTypes.bool,
  setOverride: PropTypes.func.isRequired,
  onChanged: PropTypes.func,
  testId: PropTypes.string,
};

export default OrgAccountSwitch;
