import React from 'react';
import PropTypes from 'prop-types';
import { useRouter } from 'next/router';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { BoxLayout2 } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import ExpandableText from '@components1/common/ExpandableText';
import CreateToolsV5 from './CreateToolsV5';
import { Modal } from '@components1/common/modal';
import { hasWriteAccess } from '@lib/auth';
import CustomButton from '@components1/common/NewCustomButton';
import ButtonMenu from '@components1/common/ButtonMenu';
import { EditIcon, ErrorIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { Tooltip } from '@mui/material';
import { TOOL_CONFIGURATION_WARNING } from '@data/constants';

const ListTools = ({ accountId }) => {
  const router = useRouter();
  const [data, setData] = React.useState([]);
  const [originalData, setOriginalData] = React.useState([]);
  const [loading, setLoading] = React.useState(false);
  const [createToolModal, setCreateToolModal] = React.useState(false);
  const [allTools, setAllTools] = React.useState([]);
  const [editMode, setEditMode] = React.useState(false);
  const [selectedTool, setSelectedTool] = React.useState(null);
  const [searchToolByName, setSearchToolByName] = React.useState('');
  const [preSelectedToolType, setPreSelectedToolType] = React.useState('');

  React.useEffect(() => {
    listTools();
  }, [accountId]);

  React.useEffect(() => {
    if (searchToolByName === '') {
      setData(originalData);
    } else {
      const filteredData = originalData.filter((item) => {
        const toolName = item[0].text.toLowerCase();
        return toolName.includes(searchToolByName.toLowerCase());
      });
      setData(filteredData);
    }
  }, [searchToolByName, originalData]);

  const handleSearchChange = (e) => {
    setSearchToolByName(e.target.value);
  };

  const handleSearchEnter = () => {
    listTools();
  };

  const handleEditTool = (tool) => {
    setSelectedTool(tool);
    setEditMode(true);
    setPreSelectedToolType('');
    setCreateToolModal(true);
  };

  const handleCreateToolTypeSelection = (type) => {
    setEditMode(false);
    setSelectedTool(null);
    setPreSelectedToolType(type);
    setCreateToolModal(true);
  };

  // ButtonMenu items for tool type selection
  const toolTypeMenuItems = [
    {
      text: 'MCP Integration',
      onClick: () => router.push('/accounts/account-form?cloudProvider=mcp'),
    },
    {
      text: 'Container',
      onClick: () => handleCreateToolTypeSelection('container'),
    },
  ];

  const listTools = () => {
    setLoading(true);
    apiAskNudgebee
      .listTools({ accountId })
      .then((res) => {
        const listToolsResponse = res.data?.data?.ai_list_tools?.data ?? [];
        const allTools = listToolsResponse.map((tool) => tool);
        setAllTools(allTools);
        if (listToolsResponse.length > 0) {
          const tools = listToolsResponse.map((tool) => {
            return [
              {
                component: (
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    {tool.name}
                    {tool.needs_config && !tool.is_configured && (
                      <Tooltip title={TOOL_CONFIGURATION_WARNING}>
                        <SafeIcon src={ErrorIcon} alt='warning' height={18} width={18} />
                      </Tooltip>
                    )}
                  </div>
                ),
                text: tool.name,
              },
              {
                text: tool.description ? <ExpandableText text={tool.description} /> : '-',
              },
              {
                component: <CustomLabels text={tool.status} />,
              },
              {
                text: tool.nb_tool_type,
              },
              {
                text: tool.type,
              },
              {
                component:
                  tool.type === 'custom' && tool.nb_tool_type == 'tool' && hasWriteAccess(accountId) ? (
                    <div style={{ display: 'flex' }}>
                      <CustomButton
                        onClick={() => handleEditTool(tool)}
                        variant='secondary'
                        size='xSmall'
                        text={<SafeIcon src={EditIcon} alt='edit' height={20} width={20} />}
                        sx={{
                          maxHeight: '32px',
                          maxWidth: '50px',
                          minWidth: '50px !important',
                        }}
                      />
                    </div>
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
        width={'lg'}
        open={createToolModal}
        handleClose={() => {
          setCreateToolModal(false);
          setEditMode(false);
          setSelectedTool(null);
        }}
        title={editMode ? 'Edit Tool' : 'Add Tool'}
      >
        <CreateToolsV5
          accountId={accountId}
          handleClose={(value) => {
            if (value == 'success') {
              listTools();
            }
            setCreateToolModal(false);
            setEditMode(false);
            setSelectedTool(null);
            setPreSelectedToolType('');
          }}
          allTools={allTools}
          editMode={editMode}
          toolData={selectedTool}
          preSelectedToolType={preSelectedToolType}
        />
      </Modal>
      <BoxLayout2
        id='all-tools'
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: 'tools',
              };
            },
          },
        }}
        filterOptions={[
          {
            type: 'search',
            enabled: true,
            onSelect: handleSearchChange,
            minWidth: '150px',
            label: 'Search Tool',
            onEnter: handleSearchEnter,
            value: searchToolByName,
          },
        ]}
        customButton={
          hasWriteAccess(accountId) ? <ButtonMenu title='Create Tool' items={toolTypeMenuItems} variant='secondary' size='medium' /> : null
        }
      >
        <CustomTable
          headers={[
            { name: 'Name', width: '10%' },
            { name: 'Description', width: '30%' },
            { name: 'Status', width: '10%' },
            { name: 'NB Tool Type', width: '10%' },
            { name: 'Type', width: '10%' },
            { name: 'Actions', width: '10%' },
          ]}
          tableData={data}
          rowsPerPage={data.length}
          totalRows={data.length}
          loading={loading}
          id='tools'
        />
      </BoxLayout2>
    </>
  );
};

ListTools.propTypes = {
  accountId: PropTypes.string,
};

export default ListTools;
