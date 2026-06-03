import React from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiKnowledgeBase from '@api1/knowledge-base';
import ListingLayout from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import DownloadButton from '@common-new/DownloadButton';
import { Button } from '@components1/ds/Button';
import CustomTable from '@common-new/tables/CustomTable2';
import { Label } from '@components1/ds/Label';
import { Modal } from '@components1/ds/Modal';
import CreateAgentNew from './CreateAgentNew';
import CreateAgentExtension from './CreateAgentExtension';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import Text from '@common-new/format/Text';
import { ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import { hasWriteAccess } from '@lib/auth';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { Avatar, Box, Typography, List, ListItem, ListItemText } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import { PlusIcon, EditIcon, DeleteIconRed as deleteIcon, DataBaseDark, PlusIconSecondary } from '@assets';
import { getIcon } from '@components1/llm/common/AgentIcon';
import Loader from '@components1/common/Loader';
import SafeIcon from '@components1/common/SafeIcon';

const ListAgents = ({ accountId, refreshAgentListing, allAgents, loadingAgents }) => {
  const { baseTitle } = useTenantBranding();
  const [data, setData] = React.useState([]);
  const [originalData, setOriginalData] = React.useState([]);
  const [createAgentModal, setCreateAgentModal] = React.useState(false);
  const [allAgentNames, setAllAgentNames] = React.useState([]);
  const [searchAgentByName, setSearchAgentByName] = React.useState('');
  const [selectedAgent, setSelectedAgent] = React.useState(null);
  const [editMode, setEditMode] = React.useState(false);
  const [customizeMode, setCustomizeMode] = React.useState(false);
  const [extensionMode, setExtensionMode] = React.useState(false);
  const [deleteModal, setDeleteModal] = React.useState(false);
  const [agentToDelete, setAgentToDelete] = React.useState(null);
  const [agentTypeFilter, setAgentTypeFilter] = React.useState('all');

  // State for KB counts - { agentName: count }
  const [kbCountsMap, setKbCountsMap] = React.useState({});

  // State for Agent Extensions
  const [extensionsMap, setExtensionsMap] = React.useState({}); // { agentId: extension[] }

  const [isKbDataPopupOpen, setIsKbDataPopupOpen] = React.useState(false);
  const [currentAgentKbData, setCurrentAgentKbData] = React.useState([]);
  const [isLoadingKbData, setIsLoadingKbData] = React.useState(false);

  const [isKbSelectionModalOpen, setIsKbSelectionModalOpen] = React.useState(false);
  const [availableKbs, setAvailableKbs] = React.useState([]);
  const [selectedKbIds, setSelectedKbIds] = React.useState([]);
  const [isLoadingKbs, setIsLoadingKbs] = React.useState(false);
  const [isMappingKb, setIsMappingKb] = React.useState(false);
  const [kbSearchTerm, setKbSearchTerm] = React.useState('');
  const [alreadyMappedKbIds, setAlreadyMappedKbIds] = React.useState([]);

  const [kbToRemove, setKbToRemove] = React.useState(null);
  const [isRemoveKbModalOpen, setIsRemoveKbModalOpen] = React.useState(false);

  const [triggerSubmit, setTriggerSubmit] = React.useState(false);

  const fetchKBCounts = async () => {
    try {
      const response = await apiKnowledgeBase.getAgentsWithKbCounts(accountId);
      if (response?.errors?.length > 0) {
        console.error('Error fetching KB counts:', response.errors);
        return;
      }

      const counts = (response.data || []).reduce((acc, item) => {
        acc[item.agent_id] = item.kb_count || 0;
        return acc;
      }, {});

      setKbCountsMap(counts);
    } catch (error) {
      console.error('Failed to fetch KB counts:', error);
    }
  };

  const fetchAgentKbData = async (agent) => {
    if (!agent) {
      return;
    }
    setIsLoadingKbData(true);
    try {
      const response = await apiKnowledgeBase.getAgentKnowledgeBases(accountId, agent.name);
      if (response?.errors?.length > 0) {
        snackbar.error('Failed to fetch knowledge bases for agent');
        setCurrentAgentKbData([]);
      } else {
        setCurrentAgentKbData(response.data || []);
        // Update the count for this agent
        setKbCountsMap((prev) => ({
          ...prev,
          [agent.name]: response.data?.length || 0,
        }));
      }
    } catch (error) {
      console.error('Error fetching agent KBs:', error);
      snackbar.error('Failed to fetch knowledge bases for agent');
      setCurrentAgentKbData([]);
    } finally {
      setIsLoadingKbData(false);
    }
  };

  const handleOpenKbDataPopup = (agent) => {
    setSelectedAgent(agent);
    setIsKbDataPopupOpen(true);
    fetchAgentKbData(agent);
  };

  const handleCloseKbDataPopup = () => {
    setIsKbDataPopupOpen(false);
    setSelectedAgent(null);
    setCurrentAgentKbData([]);
  };

  const fetchAvailableKbs = async () => {
    setIsLoadingKbs(true);
    try {
      const response = await apiKnowledgeBase.getKnowledgeBases(accountId);
      if (response?.errors?.length > 0) {
        snackbar.error('Failed to fetch knowledge bases');
        setAvailableKbs([]);
      } else {
        setAvailableKbs(response.data || []);
      }
    } catch (error) {
      console.error('Error fetching KBs:', error);
      snackbar.error('Failed to fetch knowledge bases');
      setAvailableKbs([]);
    } finally {
      setIsLoadingKbs(false);
    }
  };

  const fetchAlreadyMappedKbs = async (agent) => {
    try {
      const response = await apiKnowledgeBase.getAgentKnowledgeBases(accountId, agent.name);
      if (!response?.errors?.length) {
        const mappedIds = (response.data || []).map((kb) => kb.id);
        setAlreadyMappedKbIds(mappedIds);
      }
    } catch (error) {
      console.error('Error fetching already mapped KBs:', error);
    }
  };

  const handleOpenKbSelectionModal = (agent) => {
    setSelectedAgent(agent);
    setSelectedKbIds([]);
    setKbSearchTerm('');
    setAlreadyMappedKbIds([]);
    setIsKbSelectionModalOpen(true);
    fetchAvailableKbs();
    fetchAlreadyMappedKbs(agent);
  };

  const handleCloseKbSelectionModal = () => {
    setIsKbSelectionModalOpen(false);
    setSelectedAgent(null);
    setSelectedKbIds([]);
    setKbSearchTerm('');
    setAvailableKbs([]);
    setAlreadyMappedKbIds([]);
  };

  const handleToggleKbSelection = (kbId) => {
    setSelectedKbIds((prev) => {
      if (prev.includes(kbId)) {
        return prev.filter((id) => id !== kbId);
      }
      return [...prev, kbId];
    });
  };

  const handleMapKbToAgent = async () => {
    if (selectedKbIds.length === 0 || !selectedAgent) {
      snackbar.error('Please select at least one knowledge base');
      return;
    }

    setIsMappingKb(true);
    try {
      let successCount = 0;
      let errorCount = 0;

      for (const kbId of selectedKbIds) {
        const response = await apiKnowledgeBase.mapKnowledgeBaseToAgent(accountId, kbId, selectedAgent.name);
        if (response?.errors?.length > 0) {
          errorCount++;
        } else {
          successCount++;
        }
      }

      if (successCount > 0) {
        snackbar.success(`${successCount} knowledge base(s) mapped to agent successfully`);
      }
      if (errorCount > 0) {
        snackbar.error(`Failed to map ${errorCount} knowledge base(s)`);
      }

      handleCloseKbSelectionModal();
      // Refresh the KB count for this agent
      fetchAgentKbData(selectedAgent);
    } catch (error) {
      console.error('Error mapping KBs to agent:', error);
      snackbar.error('Failed to map knowledge bases to agent');
    } finally {
      setIsMappingKb(false);
    }
  };

  const getFilteredKbs = () => {
    if (!kbSearchTerm.trim()) {
      return availableKbs;
    }
    const searchLower = kbSearchTerm.toLowerCase();
    return availableKbs.filter((kb) => kb.name?.toLowerCase().includes(searchLower) || kb.description?.toLowerCase().includes(searchLower));
  };

  const openRemoveKbConfirmation = (kb) => {
    setKbToRemove(kb);
    setIsRemoveKbModalOpen(true);
  };

  const handleUnmapKbFromAgent = async () => {
    if (!selectedAgent || !kbToRemove) {
      return;
    }

    try {
      const response = await apiKnowledgeBase.unmapKnowledgeBaseFromAgent(accountId, kbToRemove.id, selectedAgent.name);
      if (response?.errors?.length > 0) {
        snackbar.error(response.errors[0]?.message || 'Failed to remove knowledge base from agent');
      } else {
        snackbar.success('Knowledge base removed from agent successfully');
        fetchAgentKbData(selectedAgent);
      }
    } catch (error) {
      console.error('Error unmapping KB from agent:', error);
      snackbar.error('Failed to remove knowledge base from agent');
    } finally {
      setIsRemoveKbModalOpen(false);
      setKbToRemove(null);
    }
  };

  const fetchAgentExtensions = async () => {
    try {
      const response = await apiAskNudgebee.listAgentExtensions(accountId);
      if (response?.errors?.length > 0) {
        snackbar.error('Failed to fetch agent extensions');
        return;
      }
      // Create a map for quick lookup
      const extensionsMap = (response.data || []).reduce((acc, extension) => {
        if (!acc[extension.agent_id]) {
          acc[extension.agent_id] = [];
        }
        acc[extension.agent_id].push(extension);
        return acc;
      }, {});
      setExtensionsMap(extensionsMap);
    } catch (error) {
      console.error('Failed to fetch agent extensions:', error);
      snackbar.error('Failed to fetch agent extensions');
    }
  };
  React.useEffect(() => {
    fetchKBCounts();
    fetchAgentExtensions();
  }, [accountId]);

  React.useEffect(() => {
    // Refresh KB counts when allAgents changes (in case new agents are added)
    if (allAgents && allAgents.length > 0) {
      fetchKBCounts();
    }
  }, [allAgents]);

  React.useEffect(() => {
    listAgents();
  }, [accountId, allAgents, kbCountsMap, extensionsMap]); // Added kbCountsMap and extensionsMap as dependency

  React.useEffect(() => {
    let filteredData = originalData;

    // Filter by search text
    if (searchAgentByName !== '') {
      filteredData = filteredData.filter((item) => {
        const agentName = item[0]?.drillDownQuery?.name?.toLowerCase();
        return agentName?.includes(searchAgentByName?.toLowerCase());
      });
    }

    // Filter by agent type
    if (agentTypeFilter !== 'all') {
      filteredData = filteredData.filter((item) => {
        // The agent data is stored in the component, we need to find it from allAgents
        const agentName = item[0]?.drillDownQuery?.name;
        const agent = allAgents?.find((a) => (a.aliases?.[0] ?? a.name) === agentName);

        if (agentTypeFilter === 'nudgebee-system-agent') {
          return agent?.type === 'system';
        } else if (agentTypeFilter === 'user-created-agent') {
          return agent?.type === 'custom';
        }
        return true;
      });
    }

    setData(filteredData);
  }, [searchAgentByName, agentTypeFilter, originalData, allAgents]);

  const handleSearchEnter = () => {
    listAgents();
  };

  const handleEditAgent = (agent) => {
    setSelectedAgent(agent);
    if (agent.type === 'custom') {
      setEditMode(true);
      setCustomizeMode(false);
    } else {
      setEditMode(false);
      setCustomizeMode(true);
    }
    setCreateAgentModal(true);
  };

  const handleExtendAgent = (agent) => {
    setSelectedAgent(agent);
    setExtensionMode(true);
    setCreateAgentModal(true);
  };

  const handleDeleteAgent = (agent) => {
    setAgentToDelete(agent);
    setDeleteModal(true);
  };

  const confirmDeleteAgent = async () => {
    if (!agentToDelete) {
      return;
    }

    try {
      const response = await apiAskNudgebee.deleteAgent(accountId, agentToDelete.name);
      if (!(response?.data?.data?.ai_delete_agent?.data?.status === 'ok')) {
        snackbar.error('Failed to delete agent');
        return;
      }

      snackbar.success(
        agentToDelete.overridden
          ? `Agent "${agentToDelete.aliases?.[0] || agentToDelete.name}" reverted to system agent successfully`
          : `Agent "${agentToDelete.aliases?.[0] || agentToDelete.name}" deleted successfully`
      );
      setDeleteModal(false);
      setAgentToDelete(null);
      refreshAgentListing();
    } catch (error) {
      console.error('Error deleting agent:', error);
      snackbar.error('Failed to delete agent');
    }
  };

  const handleMenuAction = (action, agent) => {
    switch (action.id) {
      case 'add-kb':
        handleOpenKbSelectionModal(agent);
        break;
      case 'edit':
        handleEditAgent(agent);
        break;
      case 'extend':
        handleExtendAgent(agent);
        break;
      case 'delete':
        handleDeleteAgent(agent);
        break;
      default:
        break;
    }
  };

  const getMenuItems = (agent) => {
    const hasExtensions = extensionsMap[agent.name]?.length > 0;
    const menuItems = [];

    // Add KB
    menuItems.push({
      id: 'add-kb',
      label: 'Add KB',
      icon: DataBaseDark,
    });

    // Edit
    menuItems.push({
      id: 'edit',
      label: agent.type === 'custom' ? 'Edit Agent' : 'Override Agent Prompt',
      icon: EditIcon,
    });

    // Extend (only for system agents)
    if (agent.type === 'system') {
      menuItems.push({
        id: 'extend',
        label: hasExtensions ? 'Update Extension' : 'Add Prompt and Tools',
        icon: PlusIconSecondary,
      });
    }

    // Delete/Revert
    if (agent.type === 'custom' && !agent.overridden) {
      menuItems.push({
        id: 'delete',
        label: 'Delete Agent',
        icon: deleteIcon,
      });
    } else if (agent.overridden) {
      menuItems.push({
        id: 'delete',
        label: 'Revert Agent',
        icon: deleteIcon,
      });
    }

    return menuItems;
  };

  const listAgents = () => {
    const listAgentResponse = allAgents ?? [];
    if (listAgentResponse.length > 0) {
      const agents = listAgentResponse.map((agent) => {
        const icon = getIcon(agent?.name?.toLowerCase());
        const currentAgentName = agent.name;
        const kbCount = kbCountsMap[currentAgentName] || 0;
        const hasExtensions = extensionsMap[currentAgentName]?.length > 0;
        return [
          {
            component: (
              <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: ds.space[2], minWidth: 0, maxWidth: '100%' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
                  {icon ? (
                    <SafeIcon src={icon?.default} alt='agent icon' width={18} height={18} />
                  ) : (
                    <Avatar
                      style={{
                        width: '16px',
                        height: '16px',
                        border: `1px solid ${ds.blue[400]}`,
                        color: ds.blue[400],
                        backgroundColor: ds.background[100],
                        fontSize: ds.text.small,
                        fontWeight: ds.weight.medium,
                        borderRadius: ds.radius.sm,
                        padding: '1px 0px 0px',
                      }}
                    >
                      {/* Ensure agent.name exists before trying to access agent.name[0] */}
                      {agent.name ? agent.name[0].toUpperCase() : '?'}
                    </Avatar>
                  )}
                  <Box sx={{ fontWeight: ds.weight.medium }}>{agent.aliases?.[0] ?? agent.name}</Box>
                </Box>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
                  <Box
                    sx={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      backgroundColor: agent.type === 'system' ? ds.blue[100] : ds.gray[100],
                      color: agent.type === 'system' ? ds.blue[700] : ds.gray[600],
                      fontSize: ds.text.caption,
                      fontWeight: ds.weight.semibold,
                      padding: '2px 6px',
                      borderRadius: ds.radius.pill,
                      border: `1px solid ${agent.type === 'system' ? ds.blue[200] : ds.gray[200]}`,
                      textTransform: 'uppercase',
                      letterSpacing: '0.5px',
                    }}
                  >
                    {agent.type === 'system' ? `${baseTitle} System Agent` : 'User Created Agent'}
                  </Box>
                  {agent.overridden && agent.type === 'custom' && (
                    <Box
                      sx={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        backgroundColor: ds.amber[100],
                        color: ds.amber[700],
                        fontSize: ds.text.caption,
                        fontWeight: ds.weight.semibold,
                        padding: '2px 6px',
                        borderRadius: ds.radius.pill,
                        border: `1px solid ${ds.amber[200]}`,
                        textTransform: 'uppercase',
                        letterSpacing: '0.5px',
                      }}
                    >
                      ⚠ USER OVERRIDDEN
                    </Box>
                  )}
                  {hasExtensions && (
                    <Box
                      sx={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        backgroundColor: ds.green[100],
                        color: ds.green[600],
                        fontSize: ds.text.caption,
                        fontWeight: ds.weight.semibold,
                        padding: '2px 6px',
                        borderRadius: ds.radius.pill,
                        border: `1px solid ${ds.green[200]}`,
                        textTransform: 'uppercase',
                        letterSpacing: '0.5px',
                      }}
                    >
                      ✓ ADD-ON CONFIGURED
                    </Box>
                  )}
                </Box>
              </Box>
            ),
            drillDownQuery: {
              name: agent.aliases?.[0] ?? agent.name,
            },
          },
          {
            component: <Text value={agent.description} />,
          },
          {
            component: <Label text={agent.status} />,
          },
          {
            component: <Text value={agent.tools?.join(', ') || '-'} showAutoEllipsis requiredToolTip lineClamp={2} />,
          },
          {
            // KB Count Indicator Column
            component: (
              <Box
                sx={{ display: 'flex', alignItems: 'center', cursor: 'pointer', gap: ds.space[1] }}
                onClick={() => {
                  handleOpenKbDataPopup(agent);
                }}
              >
                <Text value={`${kbCount}`} sx={{ fontWeight: 'medium' }} />
              </Box>
            ),
          },
          {
            component: hasWriteAccess(accountId) ? (
              <ThreeDotsMenu menuItems={getMenuItems(agent)} onMenuClick={handleMenuAction} data={agent} sx={{ padding: ds.space[1] }} />
            ) : (
              <></>
            ),
          },
        ];
      });
      setOriginalData(agents);
      setData(agents);
      setAllAgentNames(listAgentResponse.map((agent) => agent.name));
    } else {
      setData([]);
      setOriginalData([]);
    }
  };

  return (
    <>
      {/* KB Selection Modal */}
      <Modal
        width={'lg'}
        open={isKbSelectionModalOpen}
        handleClose={handleCloseKbSelectionModal}
        title={`Add Knowledge Base to ${selectedAgent?.aliases?.[0] || selectedAgent?.name || 'Agent'}`}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3] }}>
            <Button tone='secondary' size='md' onClick={handleCloseKbSelectionModal}>
              Cancel
            </Button>
            <Button tone='primary' size='md' onClick={handleMapKbToAgent} disabled={selectedKbIds.length === 0 || isMappingKb}>
              {isMappingKb ? 'Adding...' : `Add Selected (${selectedKbIds.length})`}
            </Button>
          </Box>
        }
      >
        <Box sx={{ minHeight: '400px' }}>
          {isLoadingKbs ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '350px' }}>
              <Loader style={{ height: '100%', width: '100%' }} />
            </Box>
          ) : availableKbs.length === 0 ? (
            <Box
              sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', alignItems: 'center', minHeight: '350px', gap: ds.space[4] }}
            >
              <SafeIcon src={DataBaseDark} alt='database' width={48} height={48} style={{ opacity: 0.5 }} />
              <Typography sx={{ textAlign: 'center', color: ds.gray[600], fontSize: ds.text.bodyLg }}>No knowledge bases found.</Typography>
              <Typography sx={{ textAlign: 'center', color: ds.gray[500], fontSize: ds.text.small }}>
                Create a knowledge base in Settings → Knowledge Base first.
              </Typography>
            </Box>
          ) : (
            <Box sx={{ pt: 2 }}>
              <ListingLayout id='kb-selection-list'>
                <ListingLayout.Toolbar>
                  <CustomSearch
                    id='kb-selection-search'
                    label='Search Knowledge Base'
                    value={kbSearchTerm}
                    onChange={(value) => setKbSearchTerm(value)}
                  />
                </ListingLayout.Toolbar>
                <ListingLayout.Body>
                  <CustomTable
                    headers={[
                      { name: '', width: '5%' },
                      { name: 'Name', width: '25%' },
                      { name: 'Description', width: '35%' },
                      { name: 'Status', width: '15%' },
                      { name: 'Created By', width: '20%' },
                    ]}
                    tableData={getFilteredKbs().map((kb) => {
                      const isAlreadyMapped = alreadyMappedKbIds.includes(kb.id);
                      const isSelected = selectedKbIds.includes(kb.id);
                      return [
                        {
                          component: (
                            <Box onClick={(e) => e.stopPropagation()} sx={{ display: 'inline-flex' }}>
                              <Checkbox
                                size='sm'
                                checked={isSelected || isAlreadyMapped}
                                disabled={isAlreadyMapped}
                                onChange={() => !isAlreadyMapped && handleToggleKbSelection(kb.id)}
                                aria-label={`Select ${kb.name || 'knowledge base'}`}
                              />
                            </Box>
                          ),
                        },
                        {
                          component: (
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                              <Text value={kb.name} sx={{ fontWeight: ds.weight.medium }} />
                              {isAlreadyMapped && <Label text='Added' tone='success' />}
                            </Box>
                          ),
                        },
                        {
                          component: (
                            <Text
                              value={kb.description || '-'}
                              sx={{
                                display: '-webkit-box',
                                WebkitLineClamp: 2,
                                WebkitBoxOrient: 'vertical',
                                overflow: 'hidden',
                              }}
                            />
                          ),
                        },
                        {
                          component: <Label text={kb.status || 'active'} />,
                        },
                        {
                          component: <Text value={kb.created_by?.display_name || '-'} />,
                        },
                      ];
                    })}
                    rowsPerPage={10}
                    totalRows={getFilteredKbs().length}
                    loading={false}
                    id='kb-selection-table'
                    onRowClick={(rowData, rowIndex) => {
                      const kb = getFilteredKbs()[rowIndex];
                      if (!kb) {
                        return;
                      }
                      const isAlreadyMapped = alreadyMappedKbIds.includes(kb.id);
                      if (!isAlreadyMapped) {
                        handleToggleKbSelection(kb.id);
                      }
                    }}
                  />
                </ListingLayout.Body>
              </ListingLayout>
            </Box>
          )}
        </Box>
      </Modal>
      {/* Create Agent Modal */}
      <Modal
        width={'lg'}
        open={createAgentModal && !extensionMode}
        contentStyles={{
          overflow: 'hidden',
        }}
        handleClose={(_event, reason) => {
          if (reason === 'backdropClick' || reason === 'escapeKeyDown') {
            return;
          }
          setCreateAgentModal(false);
          setEditMode(false);
          setCustomizeMode(false);
          setSelectedAgent(null);
        }}
        title={editMode ? 'Edit Agent' : customizeMode ? 'Override Agent Prompt' : 'Add Agent'}
        subtitle={editMode ? 'Edit the agent details' : 'Create a specialized AI assistant tailored to your specific needs.'}
        backgroundColor={ds.blue[100]}
        actionButtons={
          <Box display='flex' alignItems='center' justifyContent='flex-end' gap={ds.space[3]} p='0px'>
            <Button
              tone='secondary'
              size='md'
              onClick={() => {
                setCreateAgentModal(false);
                setEditMode(false);
                setCustomizeMode(false);
                setSelectedAgent(null);
              }}
            >
              Cancel
            </Button>
            <Button
              tone='primary'
              size='md'
              onClick={() => {
                setTriggerSubmit(true);
              }}
            >
              {editMode ? 'Update Agent' : customizeMode ? 'Override Agent Prompt' : 'Create Agent'}
            </Button>
          </Box>
        }
      >
        <CreateAgentNew
          accountId={accountId}
          handleClose={(value) => {
            if (value == 'success') {
              refreshAgentListing();
            }
            setCreateAgentModal(false);
            setEditMode(false);
            setCustomizeMode(false);
            setSelectedAgent(null);
          }}
          allAgents={allAgentNames}
          editMode={editMode}
          customizeMode={customizeMode}
          agentData={selectedAgent}
          triggerSubmit={triggerSubmit}
          onSubmitStart={() => {
            // Called when submit starts
          }}
          onSubmitEnd={() => {
            // Called when submit ends (success or error)
            setTriggerSubmit(false);
          }}
        />
      </Modal>

      {/* Create Agent Extension Modal */}
      <Modal
        width={'lg'}
        open={createAgentModal && extensionMode}
        contentStyles={{
          overflow: 'hidden',
        }}
        handleClose={() => {
          setCreateAgentModal(false);
          setExtensionMode(false);
          setSelectedAgent(null);
        }}
        title={selectedAgent && extensionsMap[selectedAgent.name]?.length > 0 ? 'Update Agent Extension' : 'Add Prompt and Tools'}
        subtitle='Add custom prompts and tools to enhance the agent capabilities.'
        backgroundColor={ds.blue[100]}
        actionButtons={
          <Box display='flex' alignItems='center' justifyContent='flex-end' gap={ds.space[3]} p='0px'>
            <Button
              tone='secondary'
              size='md'
              onClick={() => {
                setCreateAgentModal(false);
                setExtensionMode(false);
                setSelectedAgent(null);
              }}
            >
              Cancel
            </Button>
            <Button
              tone='primary'
              size='md'
              onClick={() => {
                setTriggerSubmit(true);
              }}
            >
              {selectedAgent && extensionsMap[selectedAgent.name]?.length > 0 ? 'Update Extension' : 'Create Extension'}
            </Button>
          </Box>
        }
      >
        <CreateAgentExtension
          accountId={accountId}
          handleClose={(value) => {
            if (value == 'success') {
              refreshAgentListing();
              fetchAgentExtensions();
            }
            setCreateAgentModal(false);
            setExtensionMode(false);
            setSelectedAgent(null);
          }}
          agentData={selectedAgent}
          existingExtension={selectedAgent ? extensionsMap[selectedAgent.name]?.[0] : null}
          editMode={selectedAgent ? extensionsMap[selectedAgent.name]?.length > 0 : false}
          triggerSubmit={triggerSubmit}
          onSubmitStart={() => {
            // Called when submit starts
          }}
          onSubmitEnd={() => {
            // Called when submit ends (success or error)
            setTriggerSubmit(false);
          }}
        />
      </Modal>

      <Modal
        width={'lg'}
        open={isKbDataPopupOpen}
        handleClose={handleCloseKbDataPopup}
        title={selectedAgent ? `Knowledge Bases for ${selectedAgent.aliases?.[0] || selectedAgent.name}` : 'Knowledge Bases'}
      >
        <Box sx={{ p: 2, minHeight: '300px' }}>
          {isLoadingKbData ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '300px' }}>
              <Loader style={{ height: '100%', width: '100%' }} />
            </Box>
          ) : currentAgentKbData?.length === 0 ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '300px' }}>
              <Typography variant='subtitle1' sx={{ textAlign: 'center' }}>
                No knowledge bases mapped to this agent.
              </Typography>
            </Box>
          ) : (
            <List>
              {currentAgentKbData.map((kb) => (
                <ListItem
                  key={kb.id}
                  divider
                  secondaryAction={
                    hasWriteAccess(accountId) && (
                      <Button
                        tone='secondary'
                        size='sm'
                        composition='icon-only'
                        icon={<SafeIcon src={deleteIcon} alt='delete' width={20} height={20} />}
                        aria-label='Remove knowledge base'
                        onClick={() => openRemoveKbConfirmation(kb)}
                      />
                    )
                  }
                >
                  <ListItemText
                    primary={kb.name || 'N/A'}
                    secondary={
                      <>
                        {kb.description && (
                          <Typography component='span' variant='body2' color='text.primary' sx={{ display: 'block' }}>
                            {kb.description}
                          </Typography>
                        )}
                        <Typography component='span' variant='caption' color='text.secondary' sx={{ display: 'block' }}>
                          Status: {kb.status || 'N/A'}
                          {kb.created_at && ` • Added: ${new Date(kb.created_at).toLocaleDateString()}`}
                        </Typography>
                      </>
                    }
                  />
                </ListItem>
              ))}
            </List>
          )}
        </Box>
      </Modal>

      <Modal
        handleClose={() => {
          setDeleteModal(false);
          setAgentToDelete(null);
        }}
        buttonText={agentToDelete?.overridden ? 'Revert' : 'Delete'}
        title={
          agentToDelete?.overridden
            ? `Revert Agent: ${agentToDelete?.aliases?.[0] || agentToDelete?.name}`
            : `Delete Agent: ${agentToDelete?.aliases?.[0] || agentToDelete?.name}`
        }
        open={deleteModal}
        handleSubmit={confirmDeleteAgent}
      >
        <Typography variant='body1' sx={{ mt: 2, mb: 1 }}>
          {agentToDelete?.overridden ? (
            <>
              Are you sure you want to revert the agent &quot;<strong>{agentToDelete?.aliases?.[0] || agentToDelete?.name}</strong>&quot; to the
              system agent?
              <br />
              <br />
              This will remove the custom override and restore the original system agent behavior. All custom configurations will be permanently
              removed.
            </>
          ) : (
            <>
              Are you sure you want to delete the agent &quot;<strong>{agentToDelete?.aliases?.[0] || agentToDelete?.name}</strong>&quot;?
              <br />
              <br />
              This action cannot be undone. All associated configurations and data will be permanently removed.
            </>
          )}
        </Typography>
        <Box sx={{ p: 1, mb: ds.space[2], display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: ds.space[4] }}>
          <Button
            tone='secondary'
            size='sm'
            onClick={() => {
              setDeleteModal(false);
              setAgentToDelete(null);
            }}
          >
            Cancel
          </Button>
          <Button tone='primary' size='sm' onClick={confirmDeleteAgent}>
            {agentToDelete?.overridden ? 'Revert' : 'Delete'}
          </Button>
        </Box>
      </Modal>

      {/* KB Removal Confirmation Modal */}
      <Modal title='Remove Knowledge Base' open={isRemoveKbModalOpen} handleSubmit={handleUnmapKbFromAgent}>
        <Typography variant='body1' sx={{ mt: 2, mb: 1 }}>
          Are you sure you want to remove the knowledge base &quot;<strong>{kbToRemove?.name || 'N/A'}</strong>&quot; from agent &quot;
          <strong>{selectedAgent?.aliases?.[0] || selectedAgent?.name}</strong>&quot;?
        </Typography>
        <Box sx={{ p: 1, mb: ds.space[2], display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: ds.space[4] }}>
          <Button
            tone='secondary'
            size='sm'
            onClick={() => {
              setIsRemoveKbModalOpen(false);
              setKbToRemove(null);
            }}
          >
            Cancel
          </Button>
          <Button tone='primary' size='sm' onClick={handleUnmapKbFromAgent}>
            Remove
          </Button>
        </Box>
      </Modal>

      <ListingLayout id='all-agents'>
        <ListingLayout.Toolbar
          actions={
            <>
              <DownloadButton onClick={() => ({ tableId: 'agents' })} size='sm' />
              {hasWriteAccess(accountId) && (
                <Button tone='primary' size='sm' id='create-agent' onClick={() => setCreateAgentModal(true)}>
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: ds.space[2],
                      fontFamily: ds.font.sans,
                      fontSize: ds.text.small,
                      fontWeight: ds.weight.medium,
                    }}
                  >
                    <SafeIcon src={PlusIcon} alt='plus' />
                    Create Custom Agent
                  </Box>
                </Button>
              )}
            </>
          }
        >
          <CustomSearch
            id='agent-search'
            label='Search Agent'
            value={searchAgentByName}
            onChange={(value) => setSearchAgentByName(value)}
            onEnterPress={handleSearchEnter}
          />
          <FilterDropdown
            id='agent-type-filter'
            label='Agent Type'
            options={[
              { value: 'all', label: 'All Agents' },
              { value: 'nudgebee-system-agent', label: `${baseTitle} System Agent` },
              { value: 'user-created-agent', label: 'User Created Agent' },
            ]}
            value={agentTypeFilter}
            onSelect={(e) => setAgentTypeFilter(e?.target?.value || 'all')}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            headers={[
              { name: 'Name', width: '15%' },
              { name: 'Description', width: '35%' },
              { name: 'Status', width: '10%' },
              { name: 'Tools', width: '10%' },
              { name: 'KB', width: '5%', info: 'Knowledge Base count - Click to view or manage knowledge bases mapped to this agent' },
              { name: 'Action', width: '5%' },
            ]}
            tableData={data}
            rowsPerPage={data.length}
            totalRows={data.length}
            loading={loadingAgents}
            id='agents'
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

ListAgents.propTypes = {
  accountId: PropTypes.string,
};

export default ListAgents;
