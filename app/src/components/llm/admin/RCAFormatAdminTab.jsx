import { useState } from 'react';
import dynamic from 'next/dynamic';
import { Box } from '@mui/material';
import { EmptyState } from '@ui/EmptyState';
import AdminAccountFilter from './AdminAccountFilter';
import Loader from '@shared/Loader';
import { ds } from '@utils/colors';

// RCAFormatTab pulls in CodeMirror — same reasoning Settings' own dynamic
// import of it documented. Deferred so opening AI & Tools on its default
// Agents tab doesn't pay for this bundle.
const RCAFormatTab = dynamic(() => import('@components/llm/RCAFormatTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});

/**
 * Admin → AI & Tools → RCA Format. Unlike every other AI & Tools sub-tab,
 * RCA Format has no tenant-wide view — it's stored 1-per-account with no
 * tenant rollup (RCAFormatTab.propTypes marks accountId required, and
 * Settings hides the tab entirely without one). Admin has no natural
 * accountId in scope, so the filter here is a required picker, not an
 * optional "All accounts" narrowing like Agents/Tools/Functions.
 */
const RCAFormatAdminTab = () => {
  const [accountId, setAccountId] = useState('');

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: ds.space[3] }}>
        <AdminAccountFilter value={accountId} onChange={setAccountId} required />
      </Box>
      {accountId ? (
        <RCAFormatTab accountId={accountId} />
      ) : (
        <EmptyState
          surface
          size='section'
          illustration='no-results'
          title='Select an account'
          description='RCA Format is configured per account — pick one above to view or edit its template.'
        />
      )}
    </Box>
  );
};

export default RCAFormatAdminTab;
