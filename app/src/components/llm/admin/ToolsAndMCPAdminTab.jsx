import { useState } from 'react';
import { Box } from '@mui/material';
import { ToggleGroup } from '@ui/ToggleGroup';
import ListTools from '@components/llm/ListTools';
import MCPConfigList from '@components/llm/MCPConfigList';
import AdminAccountFilter from './AdminAccountFilter';
import { ds } from '@utils/colors';

/**
 * Admin → AI & Tools → Tools & MCP. Merges ListTools (self-fetches via
 * accountId, tenant-wide when unset) with MCPConfigList (tenant-wide only,
 * no accountId — same read-only listing Settings' old LLMModelConfigurationTab
 * mounted, both now deleted, docs/ia-consolidation-plan.md). The account
 * filter only affects the Tools sub-view.
 */
const ToolsAndMCPAdminTab = () => {
  const [activeSubTab, setActiveSubTab] = useState('tools');
  const [accountId, setAccountId] = useState('');

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3], mb: ds.space[3], flexWrap: 'wrap', justifyContent: 'space-between' }}>
        <ToggleGroup
          selection='single'
          options={[
            { value: 'tools', label: 'Tools' },
            { value: 'mcp', label: 'MCP Servers' },
          ]}
          value={activeSubTab}
          onChange={(next) => setActiveSubTab(next)}
          size='md'
          ariaLabel='Tools & MCP'
        />
        {activeSubTab === 'tools' && <AdminAccountFilter value={accountId} onChange={setAccountId} />}
      </Box>
      {activeSubTab === 'tools' && <ListTools accountId={accountId} stickyTable />}
      {activeSubTab === 'mcp' && <MCPConfigList stickyTable />}
    </Box>
  );
};

export default ToolsAndMCPAdminTab;
