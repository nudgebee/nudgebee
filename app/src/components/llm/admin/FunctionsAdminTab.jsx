import { useState } from 'react';
import dynamic from 'next/dynamic';
import { Box } from '@mui/material';
import AdminAccountFilter from './AdminAccountFilter';
import Loader from '@shared/Loader';
import { ds } from '@utils/colors';

// ListFunctions reaches the Kubernetes tooling tree (elkjs, xterm,
// reactflow) — same reasoning Settings' own dynamic import of it documented.
// Deferred so opening AI & Tools on its default Agents tab doesn't pay for
// this bundle.
const ListFunctions = dynamic(() => import('@components/llm/ListFunctions'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});

/**
 * Admin → AI & Tools → Functions. Oversight mirror only — canonical editing
 * moves to b-Cortex → Knowledge → My Functions in PR 4
 * (docs/ia-consolidation-plan.md). ListFunctions self-fetches via accountId
 * (tenant-wide when unset), same as its existing Settings mount.
 */
const FunctionsAdminTab = () => {
  const [accountId, setAccountId] = useState('');

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: ds.space[3] }}>
        <AdminAccountFilter value={accountId} onChange={setAccountId} />
      </Box>
      <ListFunctions accountId={accountId} stickyTable />
    </Box>
  );
};

export default FunctionsAdminTab;
