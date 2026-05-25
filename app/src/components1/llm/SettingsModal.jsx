import { Box, Tabs, Tab } from '@mui/material';
import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Modal } from '@components1/common/modal';
import ListAgents from '@components1/llm/ListAgents';
import ListTools from '@components1/llm/ListTools';
import ListFunctions from '@components1/llm/ListFunctions';
import LLMConsumptionTab from '@components1/llm/LLMConsumptionTab';
import GlobalContextTab from '@components1/llm/GlobalContextTab';
import KnowledgeBaseTab from '@components1/llm/KnowledgeBaseTab';
import MemoryTab from '@components1/llm/MemoryTab';
import RCAFormatTab from '@components1/llm/RCAFormatTab';
import { colors } from 'src/utils/colors';
import { hasFeatureAccess } from '@lib/auth';
import { AgentIcon, ToolsIcon, LLMFunctionIcon, LLMConsumptionIcon, InMemoryIcon, DataBaseDark, DocumentationIcon, FileOutlineIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

const SettingsModal = ({ open, onClose, accountId, allAgents, refreshAgentListing, loadingAgents }) => {
  const [tabsConfig, setTabsConfig] = useState([]);
  const [typeSelected, setTypeSelected] = useState('agents');

  // Initialize tabs with feature access check
  useEffect(() => {
    const initializeTabs = async () => {
      const baseTabsConfig = [
        {
          key: 'agents',
          value: 'agents',
          icon: AgentIcon,
          label: 'Agents',
          alt: 'agent',
        },
        {
          key: 'tools',
          value: 'tools',
          icon: ToolsIcon,
          label: 'Tools',
          alt: 'tools',
        },
      ];

      try {
        const hasAccess = await hasFeatureAccess('LLM_FUNCTION');
        if (hasAccess) {
          baseTabsConfig.push({
            key: 'functions',
            value: 'functions',
            icon: LLMFunctionIcon,
            label: 'Functions',
            alt: 'functions',
            width: 20,
            height: 20,
          });
        }
      } catch (error) {
        // Log error appropriately - in production this should use proper logging
        if (process.env.NODE_ENV === 'development') {
          console.error('Error checking feature access:', error);
        }
      }

      baseTabsConfig.push({
        key: 'consumption',
        value: 'consumption',
        icon: LLMConsumptionIcon,
        label: 'Usage & Limits',
        alt: 'consumption',
        width: 16,
        height: 16,
      });

      baseTabsConfig.push({
        key: 'global-context',
        value: 'global-context',
        icon: DocumentationIcon,
        label: 'Global Context',
        alt: 'global-context',
        width: 16,
        height: 16,
      });

      baseTabsConfig.push({
        key: 'knowledge-base',
        value: 'knowledge-base',
        icon: DataBaseDark,
        label: 'Knowledge Base',
        alt: 'knowledge-base',
        width: 16,
        height: 16,
      });

      baseTabsConfig.push({
        key: 'memory',
        value: 'memory',
        icon: InMemoryIcon,
        label: 'Memory',
        alt: 'memory',
        width: 16,
        height: 16,
      });

      baseTabsConfig.push({
        key: 'rca-format',
        value: 'rca-format',
        icon: FileOutlineIcon,
        label: 'RCA Format',
        alt: 'rca-format',
        width: 16,
        height: 16,
      });

      setTabsConfig(baseTabsConfig);
    };
    if (open) {
      initializeTabs();
    }
  }, [open]);

  const getTabs = () => {
    return tabsConfig.map((tabConfig) => (
      <Tab
        key={tabConfig.key}
        value={tabConfig.value}
        id={`settings-tab-${tabConfig.value}`}
        label={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <SafeIcon src={tabConfig.icon} alt={tabConfig.alt} width={tabConfig.width} height={tabConfig.height} />
            {tabConfig.label}
          </Box>
        }
        centered
      />
    ));
  };

  const handleClose = () => {
    onClose();
  };

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
        display='flex'
        flexDirection={'column'}
        alignItems={'flex-start'}
        my={0}
        sx={{
          position: 'sticky',
          top: 0,
          zIndex: 10,
          backgroundColor: colors.background.white,
          borderBottom: `1px solid ${colors.border.secondary}`,
          pb: '0px',
          mb: '16px',
          padding: '0px 24px',
        }}
      >
        <Tabs
          value={typeSelected}
          onChange={(_e, v) => {
            setTypeSelected(v);
          }}
          aria-label='Platform'
          sx={{
            '& .MuiTabs-indicator': {
              backgroundColor: colors.text.primary,
            },
            '& .MuiTab-root': {
              minWidth: '80px',
              height: '36px',
              padding: '0px 16px',
              textTransform: 'inherit',
              color: colors.text.secondary,
              fontFamily: 'Roboto',
              fontSize: '13px',
              fontWeight: 500,
              '&.Mui-selected': {
                color: colors.text.primary,
              },
              '& img': {
                filter: 'brightness(0) saturate(100%) invert(23%) sepia(21%) saturate(699%) hue-rotate(178deg) brightness(87%) contrast(85%)',
              },
              '&.Mui-selected img': {
                filter: 'brightness(0) saturate(100%) invert(45%) sepia(23%) saturate(3237%) hue-rotate(195deg) brightness(98%) contrast(98%)',
              },
            },
          }}
        >
          {getTabs()}
        </Tabs>
      </Box>
      <Box sx={{ padding: '0px 24px' }}>
        {typeSelected == 'agents' ? (
          <ListAgents accountId={accountId} allAgents={allAgents} refreshAgentListing={refreshAgentListing} loadingAgents={loadingAgents} />
        ) : typeSelected == 'tools' ? (
          <ListTools accountId={accountId} />
        ) : typeSelected == 'consumption' ? (
          <LLMConsumptionTab accountId={accountId} />
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
