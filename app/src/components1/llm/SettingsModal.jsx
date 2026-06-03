import { Box } from '@mui/material';
import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Modal } from '@components1/ds/Modal';
import CustomTabs from '@common-new/CustomTabs';
import ListAgents from '@components1/llm/ListAgents';
import ListTools from '@components1/llm/ListTools';
import ListFunctions from '@components1/llm/ListFunctions';
import LLMConsumptionTab from '@components1/llm/LLMConsumptionTab';
import LLMModelConfigurationTab from '@components1/llm/LLMModelConfigurationTab';
import GlobalContextTab from '@components1/llm/GlobalContextTab';
import KnowledgeBaseTab from '@components1/llm/KnowledgeBaseTab';
import MemoryTab from '@components1/llm/MemoryTab';
import RCAFormatTab from '@components1/llm/RCAFormatTab';
import { hasFeatureAccess } from '@lib/auth';
import { AgentIcon, ToolsIcon, LLMFunctionIcon, LLMConsumptionIcon, InMemoryIcon, DataBaseDark, DocumentationIcon, FileOutlineIcon } from '@assets';
import { ds } from '@utils/colors';

const SettingsModal = ({ open, onClose, accountId, allAgents, refreshAgentListing, loadingAgents }) => {
  const [tabsConfig, setTabsConfig] = useState([]);
  const [typeSelected, setTypeSelected] = useState('agents');

  useEffect(() => {
    const initializeTabs = async () => {
      const baseTabsConfig = [
        { id: 'agents', icon: AgentIcon, label: 'Agents', alt: 'agent', size: 16 },
        { id: 'tools', icon: ToolsIcon, label: 'Tools', alt: 'tools', size: 16 },
      ];

      try {
        const hasAccess = await hasFeatureAccess('LLM_FUNCTION');
        if (hasAccess) {
          baseTabsConfig.push({ id: 'functions', icon: LLMFunctionIcon, label: 'Functions', alt: 'functions', size: 18 });
        }
      } catch (error) {
        if (process.env.NODE_ENV === 'development') {
          console.error('Error checking feature access:', error);
        }
      }

      baseTabsConfig.push({ id: 'consumption', icon: LLMConsumptionIcon, label: 'Usage & Limits', alt: 'consumption', size: 16 });
      baseTabsConfig.push({ id: 'llm-configuration', icon: LLMConsumptionIcon, label: 'Configurations', alt: 'llm-configuration', size: 16 });
      baseTabsConfig.push({ id: 'global-context', icon: DocumentationIcon, label: 'Global Context', alt: 'global-context', size: 16 });
      baseTabsConfig.push({ id: 'knowledge-base', icon: DataBaseDark, label: 'Knowledge Base', alt: 'knowledge-base', size: 16 });
      baseTabsConfig.push({ id: 'memory', icon: InMemoryIcon, label: 'Memory', alt: 'memory', size: 16 });
      baseTabsConfig.push({ id: 'rca-format', icon: FileOutlineIcon, label: 'RCA Format', alt: 'rca-format', size: 16 });

      setTabsConfig(baseTabsConfig);
    };
    if (open) {
      initializeTabs();
    }
  }, [open]);

  const handleClose = () => {
    onClose();
  };

  const customTabs = tabsConfig.map((t) => ({
    value: t.id,
    text: t.label,
    icon: t.icon,
    iconSize: t.size,
  }));

  return (
    <Modal
      width='lg'
      title={'Settings'}
      open={open}
      handleClose={handleClose}
      onClose={handleClose}
      maxHeight='90vh'
      contentStyles={{
        overflowY: 'auto',
        overflowX: 'hidden',
        padding: '0px',
      }}
    >
      <Box
        sx={{
          position: 'sticky',
          top: 0,
          zIndex: 10,
          backgroundColor: 'var(--ds-background-100)',
          mb: ds.space[4],
          padding: `${ds.space[2]} ${ds.space[5]} 0px ${ds.space[5]}`,
        }}
      >
        {customTabs.length > 0 && (
          <CustomTabs
            options={{ tabOptions: customTabs }}
            value={typeSelected}
            onChange={(next) => setTypeSelected(next)}
            smallSize
            behavior='filter'
            variant='secondary'
            ariaLabel='Settings'
          />
        )}
      </Box>
      <Box sx={{ padding: `0px ${ds.space[5]}` }}>
        {typeSelected == 'agents' ? (
          <ListAgents accountId={accountId} allAgents={allAgents} refreshAgentListing={refreshAgentListing} loadingAgents={loadingAgents} />
        ) : typeSelected == 'tools' ? (
          <ListTools accountId={accountId} />
        ) : typeSelected == 'consumption' ? (
          <LLMConsumptionTab accountId={accountId} />
        ) : typeSelected == 'llm-configuration' ? (
          <LLMModelConfigurationTab />
        ) : typeSelected == 'global-context' ? (
          <GlobalContextTab accountId={accountId} />
        ) : typeSelected == 'knowledge-base' ? (
          <KnowledgeBaseTab accountId={accountId} />
        ) : typeSelected == 'memory' ? (
          <MemoryTab accountId={accountId} />
        ) : typeSelected == 'rca-format' ? (
          <RCAFormatTab accountId={accountId} />
        ) : (
          <ListFunctions accountId={accountId} />
        )}
      </Box>
    </Modal>
  );
};

SettingsModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  accountId: PropTypes.string.isRequired,
  allAgents: PropTypes.array.isRequired,
  refreshAgentListing: PropTypes.func.isRequired,
  loadingAgents: PropTypes.bool.isRequired,
};

export default SettingsModal;
