import { useState } from 'react';
import { Box } from '@mui/material';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Banner } from '@ui/Banner';
import ListTools from '@components/llm/ListTools';
import MCPConfigList from '@components/llm/MCPConfigList';
import AdminAccountFilter from './AdminAccountFilter';
import { ds } from '@utils/colors';

/**
 * Admin → AI & Tools → Tools & MCP. Merges ListTools (self-fetches via
 * accountId, tenant-wide when unset) with MCPConfigList (tenant-wide only,
 * no accountId — same read-only listing Settings' old LLMModelConfigurationTab
 * mounted, both now deleted, docs/ia-consolidation-plan.md). The account
 * filter only affects the Tools sub-view. MCP Servers carries the same
 * read-only banner LLMModelConfigurationTab's own MCP sub-tab did — Providers'
 * equivalent is the LLM one, restored when Providers got its own tab; this one
 * was missed in the initial move and is restored here.
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
        {activeSubTab === 'mcp' && (
          <Box sx={{ width: '50%', minWidth: 0 }}>
            <Banner
              tone='info'
              surface='section'
              actionsPlacement='inline'
              message={
                <>
                  This view is read-only. Manage <strong>MCP Servers</strong> from <strong>Admin → Integrations → MCP</strong>.
                </>
              }
              actions={[{ label: 'Manage in Integrations', onClick: () => window.open('/accounts/account-form?cloudProvider=mcp', '_blank') }]}
            />
          </Box>
        )}
      </Box>
      {activeSubTab === 'tools' && <ListTools accountId={accountId} stickyTable />}
      {activeSubTab === 'mcp' && <MCPConfigList stickyTable />}
    </Box>
  );
};

export default ToolsAndMCPAdminTab;
