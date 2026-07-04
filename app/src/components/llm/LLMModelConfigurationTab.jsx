import React, { useState } from 'react';
import { Box } from '@mui/material';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Banner } from '@ui/Banner';
import LLMConfigList from '@components/llm/LLMConfigList';
import MCPConfigList from '@components/llm/MCPConfigList';
/**
 * LLM Configuration tab inside the Nubi Settings modal.
 *
 * Both sub-tabs are read-only listings — full management lives on
 * Admin → Integrations (LLM uses AddLLMConfigModal, MCP uses
 * IntegrationDynamicFormModal). Sub-tabs need no accountId because
 * they neither open a modal nor fetch per-account data (listIntegrations
 * derives the tenant from the session).
 */
const LLMModelConfigurationTab = () => {
  const [activeSubTab, setActiveSubTab] = useState('models');
  const bannerConfig = {
    models: {
      message: (
        <>
          This view is read-only. Manage <strong>LLM Providers</strong> from <strong>Admin → Integrations → LLM</strong>.
        </>
      ),
      href: '/accounts/account-form?cloudProvider=llm',
    },
    mcp: {
      message: (
        <>
          This view is read-only. Manage <strong>MCP Servers</strong> from <strong>Admin → Integrations → MCP</strong>.
        </>
      ),
      href: '/accounts/account-form?cloudProvider=mcp',
    },
  };

  const activeBanner = bannerConfig[activeSubTab];

  return (
    <Box sx={{ py: 0 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 1, flexWrap: 'wrap', justifyContent: 'space-between' }}>
        <ToggleGroup
          selection='single'
          options={[
            { value: 'models', label: 'LLM Providers' },
            { value: 'mcp', label: 'MCP Servers' },
          ]}
          value={activeSubTab}
          onChange={(next) => setActiveSubTab(next)}
          size='md'
          ariaLabel='LLM Configuration'
        />
        {activeBanner && (
          <Box sx={{ width: '50%', minWidth: 0 }}>
            <Banner
              tone='info'
              surface='section'
              actionsPlacement='inline'
              message={activeBanner.message}
              actions={[{ label: 'Manage in Admin', onClick: () => window.open(activeBanner.href, '_blank') }]}
            />
          </Box>
        )}
      </Box>

      {activeSubTab === 'models' && <LLMConfigList stickyTable />}
      {activeSubTab === 'mcp' && <MCPConfigList stickyTable />}
    </Box>
  );
};

export default LLMModelConfigurationTab;
