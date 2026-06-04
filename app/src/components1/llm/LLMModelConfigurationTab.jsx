import React, { useState } from 'react';
import { Box } from '@mui/material';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import LLMConfigList from '@components1/llm/LLMConfigList';
import MCPConfigList from '@components1/llm/MCPConfigList';

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

  return (
    <Box sx={{ py: 2 }}>
      <Box sx={{ mb: 2 }}>
        <ToggleGroup
          selection='single'
          options={[
            { value: 'models', label: 'LLM Providers' },
            { value: 'mcp', label: 'MCP Servers' },
          ]}
          value={activeSubTab}
          onChange={(next) => setActiveSubTab(next)}
          size='sm'
          ariaLabel='LLM Configuration'
        />
      </Box>

      {activeSubTab === 'models' && <LLMConfigList />}
      {activeSubTab === 'mcp' && <MCPConfigList />}
    </Box>
  );
};

export default LLMModelConfigurationTab;
