import { useState } from 'react';
import { Box } from '@mui/material';
import GlobalContextTab from '@components/llm/GlobalContextTab';
import AdminAccountFilter from './AdminAccountFilter';
import { ds } from '@utils/colors';

/**
 * Admin → AI & Tools → Account Context. Only shown when this tenant doesn't
 * have b-Cortex (bcortexEnabled === false) — when it does, this same
 * component is b-Cortex → Knowledge → Account Context instead (Settings'
 * old "Global Context" tab used the identical condition to decide between
 * the two). Optional, tenant-wide-default account filter, same pattern as
 * Agents/Tools/Functions.
 */
const AccountContextAdminTab = () => {
  const [accountId, setAccountId] = useState('');

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: ds.space[3] }}>
        <AdminAccountFilter value={accountId} onChange={setAccountId} />
      </Box>
      <GlobalContextTab accountId={accountId} />
    </Box>
  );
};

export default AccountContextAdminTab;
