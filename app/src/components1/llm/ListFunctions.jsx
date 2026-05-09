import React from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { BoxLayout2, Text } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import ExpandableText from '@components1/common/ExpandableText';
import CreateFunction from './CreateFunction';
import { Modal } from '@components1/common/modal';
import { hasWriteAccess } from '@lib/auth';
import CustomButton from '@components1/common/NewCustomButton';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { PlusIcon, DeleteIconRed as deleteIcon, EditIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import { Box, Typography, Chip } from '@mui/material';
import DateTime from '@components1/common/format/Datetime';

const ListFunctions = ({ accountId }) => {
  const [data, setData] = React.useState([]);
  const [originalData, setOriginalData] = React.useState([]);
  const [loading, setLoading] = React.useState(false);
  const [createFunctionModal, setCreateFunctionModal] = React.useState(false);
  const [searchFunctionByName, setSearchFunctionByName] = React.useState('');
  const [deleteModal, setDeleteModal] = React.useState(false);
  const [functionToDelete, setFunctionToDelete] = React.useState(null);
  const [triggerSubmit, setTriggerSubmit] = React.useState(false);
  const [editMode, setEditMode] = React.useState(false);
  const [selectedFunction, setSelectedFunction] = React.useState(null);
  const [functionsData, setFunctionsData] = React.useState([]);
  React.useEffect(() => {
    listFunctions();
  }, [accountId]);

  React.useEffect(() => {
    if (searchFunctionByName === '') {
      setData(originalData);
    } else {
      const filteredData = originalData.filter((item) => {
        const functionName = item[0]?.drillDownQuery?.name?.toLowerCase();
        return functionName?.includes(searchFunctionByName?.toLowerCase());
      });
      setData(filteredData);
    }
  }, [searchFunctionByName, originalData]);

  const handleSearchChange = (e) => {
    setSearchFunctionByName(e.target.value);
  };

  const handleSearchEnter = () => {
    listFunctions();
  };

  const handleDeleteFunction = (func) => {
    setFunctionToDelete(func);
    setDeleteModal(true);
  };

  const handleEditFunction = (func) => {
    setSelectedFunction(func);
    setEditMode(true);
    setCreateFunctionModal(true);
  };

  const confirmDeleteFunction = async () => {
    if (!functionToDelete) {
      return;
    }

    try {
      const response = await apiAskNudgebee.deleteLLMFunction({ id: functionToDelete.id, accountId: accountId });
      if (response.data.success) {
        snackbar.success(`Function "${functionToDelete.name}" deleted successfully`);
        setDeleteModal(false);
        setFunctionToDelete(null);
        listFunctions();
      } else {
        snackbar.error(response.errors?.[0] || 'Failed to delete function');
      }
    } catch (error) {
      console.error('Error deleting function:', error);
      snackbar.error('Failed to delete function');
    }
  };

  const handleMenuAction = (action, func) => {
    switch (action.id) {
      case 'edit':
        handleEditFunction(func);
        break;
      case 'delete':
        handleDeleteFunction(func);
        break;
      default:
        break;
    }
  };

  const getMenuItems = () => {
    const menuItems = [];

    // Edit (only for users with write access)
    if (hasWriteAccess(accountId)) {
      menuItems.push({
        id: 'edit',
        label: 'Edit Function',
        icon: EditIcon,
      });
    }

    // Delete (only for users with write access)
    if (hasWriteAccess(accountId)) {
      menuItems.push({
        id: 'delete',
        label: 'Delete Function',
        icon: deleteIcon,
      });
    }

    return menuItems;
  };

  const listFunctions = () => {
    setLoading(true);
    apiAskNudgebee
      .listFunctions({ accountId })
      .then((res) => {
        const listFunctionsResponse = res?.res?.llm_functions || [];
        setFunctionsData(listFunctionsResponse);
        if (listFunctionsResponse.length > 0) {
          const functions = listFunctionsResponse.map((func) => {
            const getVariableCount = (variables) => {
              if (!variables) {
                return 0;
              }
              try {
                // Try parsing as JSON first
                const parsed = typeof variables === 'string' ? JSON.parse(variables) : variables;
                return Array.isArray(parsed) ? parsed.length : Object.keys(parsed || {}).length;
              } catch {
                // If JSON parsing fails, treat as comma-separated string
                if (typeof variables === 'string') {
                  return variables
                    .split(',')
                    .map((v) => v.trim())
                    .filter((v) => v.length > 0).length;
                }
                return 0;
              }
            };

            const variableCount = getVariableCount(func.variables);

            return [
              {
                component: (
                  <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: '8px', minWidth: 0, maxWidth: '100%' }}>
                    <Text value={func.name} sx={{ fontSize: '14px' }} />
                  </Box>
                ),
                drillDownQuery: {
                  name: func.name,
                },
              },
              {
                component: <ExpandableText text={func.description || '-'} maxLength={80} />,
              },
              {
                component: <CustomLabels text={func.status || 'active'} />,
              },
              {
                component: func.prompt ? (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', flexWrap: 'wrap' }}>
                    <ExpandableText text={func.prompt} maxLength={120} sx={{ fontSize: '12px', color: '#333' }} />
                    {variableCount > 0 && (
                      <Chip
                        label={`${variableCount} var${variableCount > 1 ? 's' : ''}`}
                        size='small'
                        sx={{
                          height: '18px',
                          fontSize: '8px',
                          fontWeight: 600,
                          backgroundColor: colors.background.lightBox,
                          color: colors.success,
                          border: `1px solid ${colors.border.success}`,
                          textTransform: 'uppercase',
                          letterSpacing: '0.5px',
                          '& .MuiChip-label': { px: '6px' },
                        }}
                      />
                    )}
                  </Box>
                ) : (
                  <Typography variant='caption' sx={{ color: '#999', fontSize: '12px', fontStyle: 'italic' }}>
                    No prompt configured
                  </Typography>
                ),
              },
              {
                component: (
                  <DateTime value={func.created_at} showTooltip={true} maxLevel={2} sx={{ fontSize: '12px' }} sxSuffix={{ fontSize: '11px' }} />
                ),
              },
              {
                component: <ThreeDotsMenu menuItems={getMenuItems()} onMenuClick={handleMenuAction} data={func} sx={{ padding: '4px' }} />,
              },
            ];
          });
          setData(functions);
          setOriginalData(functions);
        } else {
          setData([]);
          setOriginalData([]);
          setFunctionsData(listFunctionsResponse);
        }
      })
      .catch((error) => {
        console.error('Error fetching functions:', error);
        snackbar.error('Failed to load functions');
        setData([]);
        setOriginalData([]);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  return (
    <>
      {/* Create/Edit Function Modal */}
      <Modal
        width={'lg'}
        open={createFunctionModal}
        contentStyles={{
          overflow: 'hidden',
        }}
        handleClose={() => {
          setCreateFunctionModal(false);
          setEditMode(false);
          setSelectedFunction(null);
        }}
        title={editMode ? 'Edit LLM Function' : 'Create New LLM Function'}
        subtitle={
          editMode
            ? 'Edit function details and configuration.'
            : 'Create custom prompt-based functions with dynamic variables and integrate existing agents.'
        }
        backgroundColor={colors.background.primaryLightest}
        actionButtons={
          <Box display='flex' alignItems='center' justifyContent='flex-end' gap='12px' p='0px' sx={{ '& button': { minWidth: '140px' } }}>
            <CustomButton
              text='Cancel'
              variant='secondary'
              size='Medium'
              onClick={() => {
                setCreateFunctionModal(false);
                setEditMode(false);
              }}
            />
            <CustomButton
              text={editMode ? 'Update Function' : 'Save Function'}
              size='Medium'
              onClick={() => {
                setTriggerSubmit(!triggerSubmit);
              }}
            />
          </Box>
        }
      >
        <CreateFunction
          accountId={accountId}
          _handleClose={(value) => {
            if (value === 'success') {
              listFunctions();
            }
            setCreateFunctionModal(false);
            setEditMode(false);
            setSelectedFunction(null);
          }}
          editMode={editMode}
          functionData={editMode ? selectedFunction : null}
          triggerSubmit={triggerSubmit}
          onSubmitStart={() => {
            // Called when submit starts
          }}
          onSubmitEnd={() => {
            // Called when submit ends (success or error)
            setTriggerSubmit(false);
          }}
          isModal={true}
          functionList={functionsData}
        />
      </Modal>

      {/* Delete Confirmation Modal */}
      <Modal
        handleClose={() => {
          setDeleteModal(false);
          setFunctionToDelete(null);
        }}
        buttonText='Delete'
        title={`Delete Function: ${functionToDelete?.name}`}
        open={deleteModal}
        handleSubmit={confirmDeleteFunction}
      >
        <Typography variant='body1' sx={{ mt: 2, mb: 1 }}>
          Are you sure you want to delete the function &quot;<strong>{functionToDelete?.name}</strong>&quot;?
          <br />
          <br />
          This action cannot be undone. The function will be permanently removed.
        </Typography>
        <Box sx={{ p: 1, mb: '8px', display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: '16px' }}>
          <CustomButton
            text='Cancel'
            variant='secondary'
            onClick={() => {
              setDeleteModal(false);
              setFunctionToDelete(null);
            }}
          />
          <CustomButton text='Delete' variant='primary' onClick={confirmDeleteFunction} />
        </Box>
      </Modal>

      <BoxLayout2
        id='all-functions'
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: 'functions',
              };
            },
          },
          sharing: { enabled: false },
        }}
        filterOptions={[
          {
            type: 'search',
            enabled: true,
            onSelect: handleSearchChange,
            minWidth: '150px',
            label: 'Search Function',
            onEnter: handleSearchEnter,
            value: searchFunctionByName,
          },
        ]}
        modalButton={{
          enabled: hasWriteAccess(accountId),
          text: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', fontFamily: 'Roboto', fontSize: '12px', fontWeight: 500 }}>
              <SafeIcon src={PlusIcon} alt='plus' />
              Create Function
            </Box>
          ),
          onClick: () => {
            setCreateFunctionModal(true);
          },
          id: 'create-function',
        }}
      >
        <CustomTable
          headers={[
            { name: 'Name', width: '25%' },
            { name: 'Description', width: '30%' },
            { name: 'Status', width: '10%' },
            { name: 'Prompt', width: '25%' },
            { name: 'Created', width: '5%' },
            { name: 'Actions', width: '5%' },
          ]}
          rowProps={{
            sx: {
              '&:hover': {
                backgroundColor: '#f8f9fa',
                transition: 'background-color 0.2s ease',
              },
            },
          }}
          tableData={data}
          rowsPerPage={data.length}
          totalRows={data.length}
          loading={loading}
          id='functions'
        />
      </BoxLayout2>
    </>
  );
};

ListFunctions.propTypes = {
  accountId: PropTypes.string,
};

export default ListFunctions;
