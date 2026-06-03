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
import CreateFunction from './CreateFunction';
import { Modal } from '@components1/ds/Modal';
import { hasWriteAccess } from '@lib/auth';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { PlusIcon, DeleteIconRed as deleteIcon, EditIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import { Box, Typography } from '@mui/material';
import Chip from '@components1/ds/Chip';
import DateTime from '@common-new/format/Datetime';

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
                  <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: ds.space[2], minWidth: 0, maxWidth: '100%' }}>
                    <Text value={func.name} sx={{ fontSize: ds.text.bodyLg }} />
                  </Box>
                ),
                drillDownQuery: {
                  name: func.name,
                },
              },
              {
                component: <Text value={func.description || '-'} showAutoEllipsis requiredToolTip lineClamp={2} />,
              },
              {
                component: <Label text={func.status || 'active'} />,
              },
              {
                component: func.prompt ? (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexWrap: 'wrap' }}>
                    <Text value={func.prompt} showAutoEllipsis requiredToolTip lineClamp={2} sx={{ fontSize: ds.text.small, color: ds.gray[700] }} />
                    {variableCount > 0 && (
                      <Chip tone='success' size='xs' sx={{ textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                        {`${variableCount} var${variableCount > 1 ? 's' : ''}`}
                      </Chip>
                    )}
                  </Box>
                ) : (
                  <Typography variant='caption' sx={{ color: ds.gray[500], fontSize: ds.text.small, fontStyle: 'italic' }}>
                    No prompt configured
                  </Typography>
                ),
              },
              {
                component: (
                  <DateTime
                    value={func.created_at}
                    showTooltip={true}
                    maxLevel={2}
                    sx={{ fontSize: ds.text.small }}
                    sxSuffix={{ fontSize: ds.text.caption }}
                  />
                ),
              },
              {
                component: <ThreeDotsMenu menuItems={getMenuItems()} onMenuClick={handleMenuAction} data={func} sx={{ padding: ds.space[1] }} />,
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
        backgroundColor={ds.blue[100]}
        actionButtons={
          <Box display='flex' alignItems='center' justifyContent='flex-end' gap={ds.space[3]} p='0px'>
            <Button
              tone='secondary'
              size='md'
              onClick={() => {
                setCreateFunctionModal(false);
                setEditMode(false);
              }}
            >
              Cancel
            </Button>
            <Button
              tone='primary'
              size='md'
              onClick={() => {
                setTriggerSubmit(!triggerSubmit);
              }}
            >
              {editMode ? 'Update Function' : 'Save Function'}
            </Button>
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
        title={`Delete Function: ${functionToDelete?.name}`}
        open={deleteModal}
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: ds.space[4], p: `${ds.space[3]} ${ds.space[5]}` }}>
            <Button
              tone='secondary'
              size='sm'
              onClick={() => {
                setDeleteModal(false);
                setFunctionToDelete(null);
              }}
            >
              Cancel
            </Button>
            <Button tone='danger' size='sm' onClick={confirmDeleteFunction}>
              Delete
            </Button>
          </Box>
        }
      >
        <Typography
          sx={{
            mt: 2,
            mb: 2,
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-regular)',
            color: ds.gray[700],
            lineHeight: 1.5,
          }}
        >
          Are you sure you want to delete the function &quot;<strong>{functionToDelete?.name}</strong>&quot;?
          <br />
          <br />
          This action cannot be undone. The function will be permanently removed.
        </Typography>
      </Modal>

      <ListingLayout id='all-functions'>
        <ListingLayout.Toolbar
          actions={
            <>
              <DownloadButton onClick={() => ({ tableId: 'functions' })} size='sm' />
              {hasWriteAccess(accountId) && (
                <Button tone='primary' size='sm' id='create-function' onClick={() => setCreateFunctionModal(true)}>
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
                    Create Function
                  </Box>
                </Button>
              )}
            </>
          }
        >
          <CustomSearch
            id='function-search'
            label='Search Function'
            value={searchFunctionByName}
            onChange={(value) => setSearchFunctionByName(value)}
            onEnterPress={handleSearchEnter}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
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
                  backgroundColor: ds.background[200],
                  transition: `background-color ${ds.motion.micro} ${ds.motion.ease}`,
                },
              },
            }}
            tableData={data}
            rowsPerPage={data.length}
            totalRows={data.length}
            loading={loading}
            id='functions'
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

ListFunctions.propTypes = {
  accountId: PropTypes.string,
};

export default ListFunctions;
