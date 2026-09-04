import { useState } from 'react';
import dynamic from 'next/dynamic';
import { Box } from '@mui/material';
import AdminAccountFilter from './AdminAccountFilter';
import Loader from '@shared/Loader';
import { ds } from '@utils/colors';

// Only rendered for a tenant without b-Cortex (legacyOnly, see
// aiToolsConfig.js) — same reasoning Settings' own dynamic import of
// MemoryTab documented: deferred so the majority of tenants, who never see
// this tab, don't pay for its bundle.
const MemoryTab = dynamic(() => import('@components/llm/MemoryTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});

/**
 * Admin → AI & Tools → Memory. Only shown when this tenant doesn't have
 * b-Cortex (bcortexEnabled === false — permanently true for OSS, since
 * BCortexModal itself is stripped from the OSS snapshot; temporarily true
 * for an EE tenant that hasn't been migrated to MEMORY_MODULE yet). The
 * legacy pre-b-Cortex memory view, same component and data Settings' old
 * "Memory" tab read (llm_conversation_memory) — b-Cortex's own Memory tabs
 * would just show a disabled placeholder for this population, not this
 * data, so there's no equivalent to redirect to. MemoryTab has its own
 * internal account picker when accountId is empty; the header filter here
 * just keeps it visually consistent with Agents/Tools/Functions/RCA Format.
 */
const MemoryLegacyAdminTab = () => {
  const [accountId, setAccountId] = useState('');

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: ds.space[3] }}>
        <AdminAccountFilter value={accountId} onChange={setAccountId} />
      </Box>
      <MemoryTab accountId={accountId} />
    </Box>
  );
};

export default MemoryLegacyAdminTab;
