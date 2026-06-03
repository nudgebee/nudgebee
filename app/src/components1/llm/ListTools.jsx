import React from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ListingLayout from '@components1/ds/ListingLayout';
import CustomSearch from '@common-new/CustomSearch';
import DownloadButton from '@common-new/DownloadButton';
import { Button } from '@components1/ds/Button';
import CustomTable from '@common-new/tables/CustomTable2';
import { Label } from '@components1/ds/Label';
import Text from '@common-new/format/Text';
import CreateTool from './CreateTool';
import { Modal } from '@components1/ds/Modal';
import { hasWriteAccess } from '@lib/auth';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { PlusIcon, EditIcon, ErrorIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { snakeToTitleCase } from 'src/utils/common';
import { Box } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import { ds } from 'src/utils/colors';
import { TOOL_CONFIGURATION_WARNING } from '@data/constants';

const ListTools = ({ accountId }) => {
  const { baseTitle } = useTenantBranding();
  const [data, setData] = React.useState([]);
  const [originalData, setOriginalData] = React.useState([]);
  const [loading, setLoading] = React.useState(false);
  const [createToolModal, setCreateToolModal] = React.useState(false);
  const [allTools, setAllTools] = React.useState([]);
  const [editMode, setEditMode] = React.useState(false);
  const [selectedTool, setSelectedTool] = React.useState(null);
  const [searchToolByName, setSearchToolByName] = React.useState('');

  React.useEffect(() => {
    listTools();
  }, [accountId]);

  React.useEffect(() => {
    if (searchToolByName === '') {
      setData(originalData);
    } else {
      const filteredData = originalData.filter((item) => {
        const originalToolName = item[0].rawData?.name || '';
        const formattedToolName = snakeToTitleCase(originalToolName);
        const searchLower = searchToolByName.toLowerCase();

        // Search in both original name (snake_case) and formatted name (Title Case)
        return originalToolName.toLowerCase().includes(searchLower) || formattedToolName.toLowerCase().includes(searchLower);
      });
      setData(filteredData);
    }
  }, [searchToolByName, originalData]);

  const handleSearchEnter = () => {
    listTools();
  };

  const handleEditTool = (tool) => {
    setSelectedTool(tool);
    setEditMode(true);
    setCreateToolModal(true);
  };

  const listTools = () => {
    setLoading(true);
    apiAskNudgebee
      .listTools({ accountId })
      .then((res) => {
        const listToolsResponse = res?.data?.data?.ai_list_tools?.data ?? [];
        const allTools = listToolsResponse.map((tool) => tool);
        setAllTools(allTools);
        if (listToolsResponse.length > 0) {
          const tools = listToolsResponse.map((tool) => {
            return [
              {
                component: (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
                    <Box sx={{ fontWeight: ds.weight.medium, display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                      {snakeToTitleCase(tool.name)}
                      {tool.needs_config && !tool.is_configured && (
                        <Tooltip title={TOOL_CONFIGURATION_WARNING}>
                          <SafeIcon src={ErrorIcon} alt='warning' height={18} width={18} />
                        </Tooltip>
                      )}
                    </Box>
                    <Box
                      sx={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        backgroundColor: tool.type === 'system' ? ds.blue[100] : ds.gray[100],
                        color: tool.type === 'system' ? ds.blue[700] : ds.gray[600],
                        fontSize: ds.text.caption,
                        fontWeight: ds.weight.semibold,
                        padding: '2px 6px',
                        borderRadius: ds.radius.pill,
                        border: `1px solid ${tool.type === 'system' ? ds.blue[200] : ds.gray[200]}`,
                        textTransform: 'uppercase',
                        letterSpacing: '0.5px',
                        width: 'fit-content',
                      }}
                    >
                      {tool.type === 'system' ? `${baseTitle} System` : 'User Created'}
                    </Box>
                  </Box>
                ),
                rawData: { name: tool.name },
              },
              {
                component: <Text value={tool.description || '-'} showAutoEllipsis requiredToolTip lineClamp={2} />,
              },
              {
                component: <Label text={tool.status} />,
              },
              {
                text: <Label text={snakeToTitleCase(tool.nb_tool_type)} />,
              },
              {
                component:
                  tool.type === 'custom' && tool.nb_tool_type == 'tool' && hasWriteAccess(accountId) ? (
                    <Button
                      tone='secondary'
                      size='xs'
                      composition='icon-only'
                      icon={<SafeIcon src={EditIcon} alt='edit' height={20} width={20} />}
                      aria-label='Edit tool'
                      onClick={() => handleEditTool(tool)}
                    />
                  ) : null,
              },
            ];
          });
          setData(tools);
          setOriginalData(tools);
        } else {
          setData([]);
          setOriginalData([]);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  };

  return (
    <>
      <Modal
        width={'md'}
        open={createToolModal}
        handleClose={() => {
          setCreateToolModal(false);
          setEditMode(false);
          setSelectedTool(null);
        }}
        title={editMode ? 'Edit Tool' : 'Add Tool'}
      >
        <CreateTool
          accountId={accountId}
          handleClose={(value) => {
            if (value == 'success') {
              listTools();
            }
            setCreateToolModal(false);
            setEditMode(false);
            setSelectedTool(null);
          }}
          allTools={allTools}
          editMode={editMode}
          toolData={selectedTool}
        />
      </Modal>
      <ListingLayout id='all-tools'>
        <ListingLayout.Toolbar
          actions={
            <>
              <DownloadButton onClick={() => ({ tableId: 'tools' })} size='sm' />
              <Button
                tone='secondary'
                size='sm'
                id='integration'
                onClick={() => {
                  window.open('/user-management#integrations', '_blank', 'noopener,noreferrer');
                }}
              >
                Integration
              </Button>
              {hasWriteAccess(accountId) && (
                <Button
                  tone='primary'
                  size='sm'
                  id='create-tool'
                  onClick={() => {
                    setEditMode(false);
                    setSelectedTool(null);
                    setCreateToolModal(true);
                  }}
                >
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
                    Create Tool
                  </Box>
                </Button>
              )}
            </>
          }
        >
          <CustomSearch
            id='tool-search'
            label='Search Tool'
            value={searchToolByName}
            onChange={(value) => setSearchToolByName(value)}
            onEnterPress={handleSearchEnter}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            headers={[
              { name: 'Name', width: '20%' },
              { name: 'Description', width: '40%' },
              { name: 'Status', width: '15%' },
              { name: 'NB Tool Type', width: '15%' },
              { name: 'Actions', width: '10%' },
            ]}
            tableData={data}
            rowsPerPage={data.length}
            totalRows={data.length}
            loading={loading}
            id='tools'
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

ListTools.propTypes = {
  accountId: PropTypes.string,
};

export default ListTools;
