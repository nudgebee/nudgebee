import { Box, Typography } from '@mui/material';
import React, { useEffect, useRef, useState } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import Datetime from '@components1/common/format/Datetime';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import NotificationRuleModal from './NotificationRuleModal';
import apiNotifications from '@api1/notification';
import apiDashboard from '@api1/home';
import apiKubernetes from '@api1/kubernetes';
import CustomIconButton from '@components1/CustomIconButton';
import SafeIcon from '@components1/common/SafeIcon';
import { DeleteIconRed as deleteIcon, writeIconLight } from '@assets';
import { hasWriteAccess } from '@lib/auth';
import { action } from 'src/utils/actionStyles';
import DeleteNotificationRuleModal from './DeleteNotificationRuleModal';
import { Text } from '@components1/common';
import apiUser from '@api1/user';
import { colors } from 'src/utils/colors';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import { PlatformChannelBadge } from '@components1/common/IconTextBadge';
import StatusBadge from '@components1/common/StatusBadge';

const Notifications = () => {
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const notificationId = 'Notifications';

  const [totalRows, setTotalRows] = useState(0);
  const [loading, setLoading] = useState(false);
  const [currentPage, setCurrentPage] = useState<number>(0);
  const [allNameSpaces, setAllNameSpaces] = useState([]);
  const [namespaceOption, setNamespaceOption] = useState([]);
  const [notificationTableData, setNotificationTableData] = useState([]);
  const [openNotificationRule, setOpenNotificationRule] = useState(false);
  const [clusterOption, setClusterOption] = useState<any[]>([]);
  const [selectedCluster, setSelectedCluster] = useState<string>('');
  const [selectedClusterId, setSelectedClusterId] = useState<string>('');
  const [selectedNamespace, setSelectedNamespace] = useState<string>('');
  const [nsWorkloadOptions, setNSWorkloadOptions] = useState<any>([]);
  const [selectedWorkload, setSelectedWorkload] = useState<string>('');
  const [activeEditRuleData, setActiveEditRuleData] = useState<any>({});
  const [deleteRuleModalVisible, setDeleteRuleModalVisible] = useState<boolean>(false);
  const [editingSource, setEditingSource] = useState('');
  const [clusterMap, setClusterMap] = useState<Map<string, string>>(new Map());

  const [isFetchCluster, setIsFetchCluster] = useState(false);
  const rawRulesRef = useRef<any[]>([]);

  const getClustersData = async () => {
    try {
      const response = await apiDashboard.getCloudAccounts();
      if (response && response.length > 0) {
        const clusters = response.map((item: any) => ({
          id: item.id,
          label: item.account_name,
          value: item.account_name,
        }));
        const map = new Map<string, string>();
        response.forEach((item: any) => {
          map.set(item.id, item.account_name);
        });
        setClusterMap(map);
        setClusterOption(clusters);
      } else {
        setClusterOption([]);
        setClusterMap(new Map());
      }
      setIsFetchCluster(true);
    } catch (error) {
      console.error(error);
    }
  };

  useEffect(() => {
    getClustersData();
    getAllNameSpacesData();
  }, []);

  const getAllNameSpacesData = async () => {
    try {
      const response: any = await apiKubernetes.getAllK8sNamespaces();
      setAllNameSpaces(response?.data);
    } catch (error) {
      console.error(error);
    }
  };

  const getAllWorkloadsData = async () => {
    try {
      if (selectedNamespace) {
        const query: any = {};
        query['namespace'] = selectedNamespace;
        query['kind'] = 'Deployment';
        const response: any = await apiKubernetes.getAllK8sWorkload(query);
        const workloadNameArray: string[] = response?.data.map((item: any) => item.name);
        const uniqueWorkloadNames = Array.from(new Set(workloadNameArray));
        setNSWorkloadOptions(uniqueWorkloadNames);
      }
    } catch (error) {
      console.error(error);
    }
  };

  useEffect(() => {
    getAllWorkloadsData();
  }, [selectedNamespace]);

  const BEST_PRACTICES_HEADER = [
    { name: 'Name', width: '15%' },
    { name: 'Source', width: '10%' },
    { name: 'Cluster', width: '12%' },
    { name: 'Application', width: '18%' },
    { name: 'Channels', width: '18%' },
    { name: 'Status', width: '8%' },
    { name: 'Created By', width: '10%' },
    { name: 'Created At', width: '12%' },
    '',
  ];

  const handleEditRuleModal = (e: any, item: any) => {
    // Look up fresh data from ref to avoid stale closure data
    const freshItem = rawRulesRef.current.find((r: any) => r.id === item.id) || item;
    setActiveEditRuleData(freshItem);
    setEditingSource(freshItem.source.toLowerCase());
    setOpenNotificationRule(true);
  };

  const handleDeleteRuleModal = (e: any, item: any) => {
    setActiveEditRuleData(item);
    setDeleteRuleModalVisible(true);
  };

  const filterNamespaces = (allNameSpaces: any, clusterName: string) => {
    const clusterId: any = clusterOption.filter((item: any) => {
      if (item.label == clusterName) {
        return item;
      }
    });
    const accountSpecificNamespaces = allNameSpaces
      ?.filter((e: any) => e.cloud_account_id == clusterId[0]?.id)
      .map((item: any) => {
        return {
          value: item.namespace_name,
          label: item.namespace_name,
        };
      });
    setNamespaceOption(accountSpecificNamespaces);
  };

  useEffect(() => {
    filterNamespaces(allNameSpaces, selectedCluster);
  }, [selectedCluster, allNameSpaces]);

  useEffect(() => {
    if (isFetchCluster) {
      listNotificationRules();
    }
  }, [currentPage, rowsPerPage, selectedCluster, selectedNamespace, selectedWorkload, isFetchCluster]);

  const listNotificationRules = () => {
    const limit = rowsPerPage;
    const offset = rowsPerPage * currentPage;
    setLoading(true);
    const query: any = {};
    if (selectedClusterId) {
      query.accountId = selectedClusterId;
    }
    if (selectedNamespace) {
      query.namespace = selectedNamespace;
    }
    if (selectedWorkload) {
      query.workload = selectedWorkload;
    }

    apiNotifications.getNotificationRules(query, limit, offset).then((res: any) => {
      const notificationRulesList: any = [];
      const rawRows = res?.data?.admin_get_notification_rules_v2?.rows || [];
      rawRows.forEach((item: any) => {
        item.notification_rule_mappings = safeJSONParse(item?.notification_rule_mappings) || [];
        const data = [];
        data.push({
          component: <Text value={item.name} />,
        });
        data.push({ component: <Text value={snakeToTitleCase(item.source)} /> });
        data.push({
          component: <Text value={clusterMap.get(item?.account_id) || '-'} />,
        });
        data.push({
          component: (
            <Box>
              <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>{item.workload || '-'}</Typography>
              {item.namespace && <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>ns: {item.namespace}</Typography>}
            </Box>
          ),
        });
        data.push({
          component: (() => {
            if (!item.notification_rule_mappings || item.notification_rule_mappings.length === 0) {
              return <Text value='-' />;
            }
            return (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                {item.notification_rule_mappings.map((mapping: any, index: number) => {
                  let channelName = '-';
                  if (mapping.platform === 'slack') {
                    channelName = mapping.channels?.name || '-';
                  } else if (mapping.platform === 'ms_teams') {
                    channelName = mapping.channels?.channels?.[0]?.name || mapping.channels?.name || '-';
                  } else if (mapping.platform === 'email') {
                    if (mapping.channels?.emails?.[0]) {
                      channelName = mapping.channels.emails[0];
                    } else if (Array.isArray(mapping.channels) && mapping.channels[0]) {
                      channelName = mapping.channels[0];
                    } else if (mapping.channels?.exclusion_emails?.length > 0) {
                      const count = mapping.channels.exclusion_emails.length;
                      channelName = `${count} user${count > 1 ? 's' : ''} excluded`;
                    } else {
                      return null;
                    }
                  } else {
                    channelName = mapping.channels?.name || '-';
                  }
                  return (
                    <PlatformChannelBadge key={`${mapping.platform}-${index}`} platform={mapping.platform} channelName={channelName} size='medium' />
                  );
                })}
              </Box>
            );
          })(),
        });
        data.push({
          component: (
            <StatusBadge label={item.is_suppressed ? 'Suppressed' : 'Active'} variant={item.is_suppressed ? 'grey' : 'success'} size='medium' />
          ),
        });
        data.push({ text: <Text value={item.created_by_display_name || '-'} /> });
        data.push({ component: <Datetime value={item.created_at} baseDate={new Date()} /> });
        data.push({
          component: (
            <Box display={'flex'} flexDirection={'row'} alignItems={'center'} justifyContent={'flex-end'}>
              {hasWriteAccess() ? (
                <>
                  <Box sx={{ mr: '8px' }}>
                    <CustomIconButton
                      sx={{ ...action.delete }}
                      onClick={(e) => {
                        handleDeleteRuleModal(e, item);
                      }}
                      size={''}
                      variant={''}
                      id={`${item.name}-delete`}
                    >
                      <SafeIcon src={deleteIcon} alt='delete' />
                    </CustomIconButton>
                  </Box>
                  <CustomIconButton
                    sx={{ ...action.primary }}
                    onClick={(e) => {
                      handleEditRuleModal(e, item);
                    }}
                    size={''}
                    variant={''}
                  >
                    <SafeIcon src={writeIconLight} alt='edit' />
                  </CustomIconButton>
                </>
              ) : (
                <></>
              )}
            </Box>
          ),
        });
        notificationRulesList.push(data);
      });
      rawRulesRef.current = rawRows;
      setNotificationTableData(notificationRulesList);
      setTotalRows(res?.data?.admin_get_notification_rules_grouping_v2?.rows?.[0]?.count || 0);
      setLoading(false);
    });
  };

  const handleButtonAction = () => {
    setOpenNotificationRule(true);
  };

  return (
    <Box position={'relative'}>
      <NotificationRuleModal
        open={openNotificationRule}
        handleClose={() => {
          setOpenNotificationRule(false);
          setActiveEditRuleData({});
          setEditingSource('');
        }}
        listNotificationRules={listNotificationRules}
        notificationRuleObject={activeEditRuleData}
        editingSource={editingSource}
      />
      <DeleteNotificationRuleModal
        open={deleteRuleModalVisible}
        handleClose={() => {
          setDeleteRuleModalVisible(false);
          setActiveEditRuleData({});
        }}
        ruleData={activeEditRuleData}
        listNotificationRules={listNotificationRules}
      />
      <BoxLayout2
        sx={
          {
            padding: '16px 14px 20px 14px',
            alignSelf: 'stretch',
            backgroundColor: colors.background.white,
            borderRadius: '12px',
            boxShadow: '0px 4px 4px 0px #00000026',
            '@media (max-width: 1350px)': {
              padding: '16px 8px 20px 8px',
            } as React.CSSProperties,
          } as React.CSSProperties
        }
        id='notification-container'
        modalButton={{
          enabled: hasWriteAccess(),
          text: 'Create Rule',
          onClick: () => handleButtonAction(),
          id: 'notification-rule',
        }}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: notificationId,
              };
            },
          },
          sharing: { enabled: false, onClick: null },
        }}
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: clusterOption,
            onSelect: (e: any) => {
              setSelectedCluster(e.target.value || '');
              setSelectedClusterId(clusterOption.find((option) => option.value === e.target.value)?.id || '');
              setSelectedNamespace('');
              setSelectedWorkload('');
            },
            minWidth: '150px',
            label: 'Cluster',
            value: selectedCluster,
          },
          {
            type: 'dropdown',
            enabled: selectedCluster?.length > 0 || false,
            options: namespaceOption || [],
            onSelect: (e: any) => {
              setSelectedNamespace(e.target.value);
              setSelectedWorkload('');
            },
            minWidth: '150px',
            label: 'Namespace',
            value: selectedNamespace,
          },
          {
            type: 'dropdown',
            enabled: selectedNamespace?.length > 0 || false,
            options: nsWorkloadOptions || [],
            onSelect: (e: any) => {
              setSelectedWorkload(e.target.value);
            },
            minWidth: '150px',
            label: 'Application',
            value: selectedWorkload,
          },
        ]}
      >
        <CustomTable2
          headers={BEST_PRACTICES_HEADER}
          tableData={notificationTableData as any}
          rowsPerPage={rowsPerPage}
          totalRows={totalRows}
          onPageChange={(page: number, limit: number) => {
            setCurrentPage(page - 1);
            setRowsPerPage(limit);
          }}
          id={notificationId}
          loading={loading}
          showExpandable={false}
          upperHeaders={[]}
          onSortChange={() => undefined}
          stickyColumnIndex='10'
          pageNumber={currentPage + 1}
        />
      </BoxLayout2>
    </Box>
  );
};

export default Notifications;
