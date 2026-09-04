import { useCallback, useEffect, useState } from 'react';
import { Box } from '@mui/material';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ListAgents from '@components/llm/ListAgents';
import AdminAccountFilter from './AdminAccountFilter';
import { ds } from '@utils/colors';

/**
 * Admin → AI & Tools → Agents. ListAgents itself is a pure list — its
 * `allAgents` prop IS its data source (see `listAgentResponse = allAgents ??
 * []` in ListAgents.jsx), not something it fetches on its own — so this
 * wrapper owns the fetch, mirroring NubiBrainNav.jsx's own internal
 * self-fetch fallback (`fetchAgents`, used when no external `agents` prop is
 * supplied) rather than duplicating a second implementation of it.
 * Tenant-wide by default (accountId=''), same as the Settings mount opened
 * from the global sidebar — narrows via the header filter.
 */
const AgentsAdminTab = () => {
  const [accountId, setAccountId] = useState('');
  const [agents, setAgents] = useState([]);
  const [loading, setLoading] = useState(false);

  const fetchAgents = useCallback(() => {
    setLoading(true);
    apiAskNudgebee
      .listAgents({ accountId })
      .then((res) => {
        setAgents(res?.data?.data?.ai_list_agents?.data ?? []);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, [accountId]);

  useEffect(() => {
    fetchAgents();
  }, [fetchAgents]);

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: ds.space[3] }}>
        <AdminAccountFilter value={accountId} onChange={setAccountId} />
      </Box>
      <ListAgents accountId={accountId} allAgents={agents} refreshAgentListing={fetchAgents} loadingAgents={loading} stickyTable />
    </Box>
  );
};

export default AgentsAdminTab;
