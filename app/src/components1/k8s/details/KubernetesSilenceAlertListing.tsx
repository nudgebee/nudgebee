import React, { useEffect, useState } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import apiKubernetes from '@api1/kubernetes';
import { List, ListItem, ListItemText, Typography, Box } from '@mui/material';
import { Text } from '@components1/common';
import Datetime from '@components1/common/format/Datetime';
import CustomIconButton from '@components1/CustomIconButton';
import { EditOutlined } from '@mui/icons-material';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import KubernetesAlertSilencer from './KubernetesAlertSilencer';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

const LISTING_HEADER = ['Status', 'Comment', 'Created By', 'Start At', 'Ends At', 'Matchers', ''];

interface KubernetesSilenceAlertListingProps {
  accountId: string;
}

interface SilenceData {
  id: string;
  comment: string;
  startsAt?: string;
  endsAt?: string;
  matchers?: string;
}

const KubernetesSilenceAlertListing: React.FC<KubernetesSilenceAlertListingProps> = ({ accountId }) => {
  const [data, setData] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [editModalOpen, setEditModalOpen] = useState(false);
  const [silenceToDelete, setSilenceToDelete] = useState<SilenceData | null>(null);
  const [silenceToEdit, setSilenceToEdit] = useState<SilenceData | null>(null);
  const [alertData, setAlertData] = useState<Record<string, any>>({});
  const [filterTypes, setFilterTypes] = useState<Record<string, string>>({});
  const [_startDateInMilli, setStartDateInMilli] = useState<number>(Date.now());
  const [_endDateInMilli, setEndDateInMilli] = useState<number>(Date.now() + 24 * 60 * 60 * 1000);
  const tableId = 'silence-alert-list';

  const getMatcherList = (matcher: string) => {
    const matcherList = JSON.parse(matcher);
    return (
      <List sx={{ pl: 2 }}>
        {matcherList.map((item: any) => (
          <ListItem key={item.name} sx={{ display: 'list-item', pl: 1, py: 0 }} disablePadding>
            <ListItemText
              primary={
                <Typography component='span' sx={{ fontSize: '14px' }}>
                  <b>
                    {item.name} {item.isEqual ? '= ' : item.isRegex ? '=~ ' : '!= '}
                  </b>{' '}
                  {item.value}
                </Typography>
              }
            />
          </ListItem>
        ))}
      </List>
    );
  };

  const handleEditClick = (silence: SilenceData) => {
    setSilenceToEdit(silence);

    try {
      const matchers = JSON.parse(silence.matchers || '[]');
      const alertDataObj: Record<string, any> = {};
      const filterTypesObj: Record<string, string> = {};

      matchers.forEach((item: any) => {
        if (item.name) {
          alertDataObj[item.name] = item.value || '';
          filterTypesObj[item.name] = item.isRegex ? 'REGEX' : 'EQUAL';
        }
      });

      setAlertData(alertDataObj);
      setFilterTypes(filterTypesObj);

      if (silence.startsAt) {
        setStartDateInMilli(new Date(silence.startsAt).getTime());
      }

      if (silence.endsAt) {
        setEndDateInMilli(new Date(silence.endsAt).getTime());
      }

      setEditModalOpen(true);
    } catch (error) {
      console.error('Error parsing silence data', error);
      snackbar.error('Failed to edit silence: Invalid data format');
    }
  };

  const handleConfirmDelete = () => {
    if (!silenceToDelete) {
      return;
    }

    setLoading(true);
    const data = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'delete_silence',
        action_params: {
          id: silenceToDelete.id,
        },
      },
      cache: false,
    };

    apiKubernetes
      .relayForwardRequest(data)
      .then((res) => {
        if (res?.data?.success) {
          snackbar.success(`Successfully deleted silence: ${silenceToDelete.comment || silenceToDelete.id}`);
          listAlertSilence();
        } else {
          snackbar.error(`Failed to delete silence: ${res?.data?.message || 'Unknown error'}`);
        }
        setDeleteModalOpen(false);
        setSilenceToDelete(null);
        setLoading(false);
      })
      .catch((error) => {
        snackbar.error(`Failed to delete silence: ${error.message || 'Unknown error'}`);
        setDeleteModalOpen(false);
        setSilenceToDelete(null);
        setLoading(false);
      });
  };

  const handleCloseDeleteModal = () => {
    setDeleteModalOpen(false);
    setSilenceToDelete(null);
  };

  const handleCloseEditModal = () => {
    setEditModalOpen(false);
    setSilenceToEdit(null);
    setAlertData({});
    setFilterTypes({});
  };

  const listAlertSilence = () => {
    setLoading(true);
    const data = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'get_silences',
        action_params: {
          alertmanager_flavor: '',
        },
      },
      cache: false,
    };
    apiKubernetes
      .relayForwardRequest(data)
      .then((res) => {
        if (res?.data?.success) {
          if (res?.data?.findings && res?.data?.findings.length > 0) {
            if (res?.data?.findings[0]?.evidence && res?.data?.findings[0].evidence.length > 0) {
              const evidenceData = JSON.parse(res?.data?.findings[0].evidence[0].data);
              const headers = evidenceData[0].data.headers;
              const convertedJson = {
                rows:
                  evidenceData?.[0]?.data?.rows?.map((row: any) => {
                    const rowData: any = {};
                    if (Array.isArray(headers) && headers.length > 0 && Array.isArray(row)) {
                      headers.forEach((header: string, index: number) => {
                        rowData[header] = row[index] !== undefined ? row[index] : null;
                      });
                    }
                    return rowData;
                  }) || [],
              };
              const convertedJson2 = convertedJson.rows.map((item: any) => {
                const rowId = item.id;
                const rowComment = item.comment;

                const components = Object.entries(item)
                  .slice(1)
                  .map(([key, value]) => {
                    if (key === 'status') {
                      let statusText = '';
                      try {
                        const statusObj = typeof value === 'string' ? JSON.parse(value) : value;
                        statusText = statusObj.state === 'expired' ? 'Expired' : 'Active';
                      } catch {
                        statusText = String(value);
                      }
                      return { text: <Text value={String(statusText)} /> };
                    } else if (key === 'matchers') {
                      // Render bullet points directly
                      return { component: getMatcherList(value as string) };
                    } else if (key === 'startsAt') {
                      return { component: <Datetime value={value as string} /> };
                    } else if (key === 'endsAt') {
                      return { component: <Datetime value={value as string} /> };
                    }
                    return { text: <Text value={value} /> };
                  });
                // Determine if alert is active or expired
                let isExpired = false;
                try {
                  const status = typeof item.status === 'string' ? JSON.parse(item.status) : item.status;
                  isExpired = status.state === 'expired';
                } catch {
                  // Default to assuming it's active if we can't determine
                  isExpired = false;
                }
                components.push({
                  component: (
                    <Box sx={{ display: 'flex', gap: '8px' }}>
                      {isExpired ? (
                        // For expired alerts, show edit button
                        <CustomIconButton
                          variant='secondary'
                          onClick={(e) => {
                            e.stopPropagation();
                            handleEditClick({
                              id: rowId,
                              comment: rowComment,
                              startsAt: item.startsAt,
                              endsAt: item.endsAt,
                              matchers: item.matchers,
                            });
                          }}
                          size='small'
                        >
                          <EditOutlined style={{ color: '#3B82F6' }} />
                        </CustomIconButton>
                      ) : (
                        <CustomIconButton
                          variant='secondary'
                          onClick={(e) => {
                            e.stopPropagation();
                            setSilenceToDelete({ id: rowId, comment: rowComment });
                            setDeleteModalOpen(true);
                          }}
                          size='small'
                        >
                          <SafeIcon src={DeleteIconRed} alt='delete' width={20} height={20} />
                        </CustomIconButton>
                      )}
                    </Box>
                  ),
                });

                return components;
              });
              setData(convertedJson2);
            }
          }
        }
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listAlertSilence();
  }, [accountId]);

  return (
    <>
      {/* Edit Modal */}
      <Modal width='md' open={editModalOpen} handleClose={handleCloseEditModal} title={'Edit Silence Alert'} contentStyles={{ padding: '0px' }}>
        <KubernetesAlertSilencer
          accountId={accountId}
          alertData={alertData}
          handleCloseSilencePopUp={handleCloseEditModal}
          filterTypes={filterTypes}
          onSuccess={listAlertSilence}
          isEdit={true}
          silenceId={silenceToEdit?.id}
          comment={silenceToEdit?.comment}
        />
      </Modal>
      {/* Delete Modal */}
      <Modal width='sm' open={deleteModalOpen} handleClose={handleCloseDeleteModal} title={'Delete Silence Alert'} contentStyles={{ padding: '0px' }}>
        <Box sx={{ p: '24px 40px' }}>
          <Typography sx={{ fontSize: '16px', mb: 2 }}>Are you sure you want to delete this silence?</Typography>
          {silenceToDelete && (
            <Box sx={{ mb: 3 }}>
              <Typography sx={{ fontWeight: '500', color: colors.text.secondary }}>{silenceToDelete.comment || silenceToDelete.id}</Typography>
            </Box>
          )}

          <Box
            sx={{
              display: 'flex',
              justifyContent: 'flex-end',
              gap: '12px',
              button: {
                minWidth: '140px',
              },
            }}
          >
            <CustomButton size='Medium' text={'Cancel'} variant='secondary' onClick={handleCloseDeleteModal} />
            <CustomButton size='Medium' text={'Delete'} onClick={handleConfirmDelete} loading={loading} />
          </Box>
        </Box>
      </Modal>

      <BoxLayout2
        id='silence-alert'
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: tableId,
              };
            },
          },
        }}
        filterOptions={[]}
      >
        <KubernetesTable2
          id={tableId}
          headers={LISTING_HEADER as any}
          rowsPerPage={data.length}
          data={data as any}
          onPageChange={undefined}
          totalRows={data.length}
          loading={loading}
          onSortChange={{}}
          sort={{}}
        />
      </BoxLayout2>
    </>
  );
};

export default KubernetesSilenceAlertListing;
