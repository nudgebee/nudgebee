import React, { useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes1';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { Modal } from '@components1/common/modal';
import KubernetesCreateAlert from '@components1/k8s/details/KubernetesCreateAlert';
import { hasWriteAccess } from '@lib/auth';
import NDialog from '@components1/common/modal/NDialog';
import { titleCase } from '@lib/formatter';
import { action } from 'src/utils/actionStyles';
import { Text } from '@components1/common';
import apiUser from '@api1/user';
import CustomTable from '@components1/common/tables/CustomTable2';
import { useRouter } from 'next/router';
import { snackbar } from '@components1/common/snackbarService';
import { isValidSeverity, snakeToTitleCase } from 'src/utils/common';
import CustomButton from '@components1/common/NewCustomButton';

interface CloudAccountAlertManagerProps {
  accountId: string;
}

const CloudAccountAlertManager: React.FC<CloudAccountAlertManagerProps> = ({ accountId }) => {
  const router = useRouter();
  const cloudAlertManager = 'cloudMgr';

  const [data, setData] = useState<any[][]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [openCreateNewAlertModal, setOpenCreateNewAlertModal] = useState<boolean>(false);
  const [isCreateAlert, setIsCreateAlert] = useState<boolean>(true);
  const [alertManagerObject, setAlertManagerObject] = useState<any | null>({});
  const [categoryList, setCategoryList] = useState<string[]>([]);
  const [selectedCategory, setSelectedCategory] = useState<string>('');
  const [sourceList, setSourceList] = useState<string[]>([]);
  const [selectedSource, setSelectedSource] = useState<string>('');
  const [severityList, setSeverityList] = useState<string[]>([]);
  const [selectedSeverity, setSelectedSeverity] = useState<string>('');
  const [selectedStatus, setSelectedStatus] = useState<string>('');
  const [disableAlert, setDisableAlert] = useState(false);
  const [searchByName, setSearchByName] = useState(router.query?.name || '');
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [alertNames, setAlertNames] = useState<string[]>([]);
  const [agentPlaybookOnEvents, setAgentPlaybookOnEvents] = useState<any[]>([]);

  const getMenuItems = (item: any) => {
    const menus = hasWriteAccess(accountId)
      ? [
          {
            label: item?.enabled ? 'Disable' : 'Enable',
            id: 0,
          },
        ]
      : [];
    if (item?.enabled) {
      menus.push({
        label: 'Edit',
        id: 1,
      });
    }
    return menus;
  };

  const onMenuClick = (menuItem: any, data: any) => {
    if (menuItem.id === 0) {
      setDisableAlert(true);
      setAlertManagerObject(data);
    }
    if (menuItem.id === 1) {
      setIsCreateAlert(false);
      setOpenCreateNewAlertModal(true);
      setAlertManagerObject(data);
    }
  };

  useEffect(() => {
    if (!accountId || accountId === 'undefined') return;
    k8sApi
      .getDistinctData(accountId)
      .then((res: any) => {
        if (res?.data?.distinct_category) {
          setCategoryList(res?.data?.distinct_category.map((c: any) => c.category));
        }
        if (res?.data?.distinct_source) {
          setSourceList(
            res?.data?.distinct_source.map((c: any) => ({
              label: titleCase(c.source),
              value: c.source,
            }))
          );
        }
        if (res?.data?.distinct_severity) {
          setSeverityList(
            res?.data?.distinct_severity.map((c: any) => ({
              label: titleCase(c.severity),
              value: c.severity,
            }))
          );
        }
      })
      .catch((error: any) => {
        console.error(error);
      });
  }, [accountId]);

  useEffect(() => {
    listAlertManager();
  }, [currentPage, selectedCategory, selectedSeverity, selectedSource, selectedStatus, accountId, rowsPerPage]);

  useEffect(() => {
    if (!alertNames.length) {
      return;
    }
    k8sApi
      .getAgentPlaybookOfEvent({
        accountId,
        alertName: alertNames,
      })
      .then((res) => {
        const agentPlaybooks = res?.data?.data?.agent_playbook || [];
        if (agentPlaybooks) {
          setAgentPlaybookOnEvents(agentPlaybooks);
          const updatedData = (data as any[]).map((itemData) => {
            const item = agentPlaybooks.find((g: any) => g.alert_name == itemData[0].drilldownQuery.name);
            const updatedItemData = [...itemData];
            if (item && Array.isArray(item.action_params) && item.action_params.length > 0) {
              updatedItemData[5] = {
                component: item.action_params
                  .map((obj: any) => {
                    const dynamicKey = Object.keys(obj)[0];
                    return obj[dynamicKey]?.title || snakeToTitleCase(dynamicKey);
                  })
                  .join(', '),
              };
            } else {
              updatedItemData[5] = { text: '-' };
            }
            return updatedItemData;
          });
          setData(updatedData);
        }
      });
  }, [alertNames]);

  const listAlertManager = () => {
    if (!accountId || accountId === 'undefined') return;
    setLoading(true);
    setData([]);
    setAlertNames([]);
    k8sApi
      .getEventRules(
        {
          accountId: accountId,
          category: selectedCategory,
          severity: selectedSeverity,
          source: selectedSource,
          status: selectedStatus,
          searchByName: searchByName,
        },
        rowsPerPage,
        currentPage * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        setTotalCount(res?.data?.event_rules_aggregate?.aggregate?.count);
        const alertNames = [] as string[];
        const data = res?.data?.event_rules.map((item: any) => {
          alertNames.push(item.alert);
          const tooltipText = item?.enabled ? 'Enabled' : 'Disabled';
          return [
            { component: <Text value={item.alert} />, drilldownQuery: { name: item.alert } },
            { component: <Text value={item.category} /> },
            { component: <Text value={item?.source || '-'} /> },
            { component: <CustomLabels margin='auto' text={item?.severity} /> },
            {
              component: <CustomLabels margin='auto' text={tooltipText} />,
            },
            {
              text: '--',
            },
            {
              component: <ThreeDotsMenu sx={{ ...action.primary }} onMenuClick={onMenuClick} data={item} menuItems={getMenuItems(item)} />,
            },
          ];
        });
        setData(data);
        setAlertNames(alertNames);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  const onSubmit = (message: string, severity: string) => {
    if (severity === 'success') {
      listAlertManager();
    }
    if (severity && isValidSeverity(severity)) {
      snackbar[severity](message);
    }
  };

  const handleCloseCreateNewAlertModal = () => {
    setOpenCreateNewAlertModal(false);
    setAlertManagerObject(null);
    setIsCreateAlert(true);
  };

  const handleCloseAlertPopUp = () => {
    setDisableAlert(false);
    setAlertManagerObject(null);
  };

  const handleSubmit = () => {
    const request: any = {
      accountId: accountId,
      alert: alertManagerObject.alert,
      enable: !alertManagerObject?.enabled,
      id: alertManagerObject?.id,
      namespace: alertManagerObject?.namespace || '',
      group: alertManagerObject?.group || '',
    };
    k8sApi
      .disableAlertManager(request)
      .then((res: any) => {
        if ((res?.data.errors && res?.data.errors?.length > 0) || res?.data?.data?.errors) {
          snackbar.error(`Failed to ${alertManagerObject.enabled ? 'Disable' : 'Enable'} Alert Rule`);
        } else {
          snackbar.success(`Rule ${alertManagerObject.alert} ${!alertManagerObject.enabled ? 'Enabled' : 'Disabled'} Successful`);
          handleCloseAlertPopUp();
          listAlertManager();
        }
      })
      .catch(() => {
        snackbar.error(`Failed to ${alertManagerObject.enabled ? 'Disable' : 'Enable'} Alert Rule`);
      });
  };

  const onClickLoader = (loaderStatus: boolean) => {
    setLoading(loaderStatus);
  };

  const onEnterPress = () => {
    if (currentPage === 0) {
      listAlertManager();
    } else {
      setCurrentPage(0);
    }
  };

  const clearSearchName = () => {
    setSearchByName('');
  };

  return (
    <div>
      <NDialog
        buttonText='Confirm'
        handleClose={handleCloseAlertPopUp}
        dialogTitle={`${alertManagerObject?.enabled ? 'Disable' : 'Enable'} the alert "${alertManagerObject?.alert}"`}
        handleSubmit={handleSubmit}
        open={disableAlert}
        dialogContent={''}
        additionalComponent={undefined}
      />
      <Modal
        width='md'
        open={openCreateNewAlertModal}
        handleClose={handleCloseCreateNewAlertModal}
        title={isCreateAlert ? 'Create New Alert' : 'Update Alert'}
        contentStyles={{ padding: '0px' }}
        maxHeight='80vh'
        rightComponentOnTitle={undefined}
        loader={loading}
      >
        <KubernetesCreateAlert
          alertManagerObject={alertManagerObject}
          onSubmit={onSubmit}
          accountId={accountId}
          handleCloseCreateNewAlertModal={handleCloseCreateNewAlertModal}
          isCreateAlert={isCreateAlert}
          onClickLoader={onClickLoader}
          agentPlaybookOnEvent={agentPlaybookOnEvents?.filter((f) => f.alert_name == alertManagerObject?.alert) || []}
        />
      </Modal>
      <BoxLayout2
        id='cloud-alert-manager-list-box'
        heading=''
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: cloudAlertManager,
              };
            },
          },
        }}
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: categoryList,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedCategory(e?.target?.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Category',
            value: selectedCategory,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: severityList,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedSeverity(e?.target?.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Severity',
            value: selectedSeverity,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: sourceList,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedSource(e?.target?.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Source',
            value: selectedSource,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: ['Enabled', 'Disabled'],
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedStatus(e?.target?.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Status',
            value: selectedStatus,
          },
          {
            type: 'search',
            enabled: true,
            onSelect: (e: any) => {
              setSearchByName(e.target.value);
            },
            minWidth: '150px',
            label: 'Search By Name',
            onEnter: onEnterPress,
            onClear: clearSearchName,
            value: searchByName,
          },
        ]}
        extraOptions={[
          hasWriteAccess(accountId) && (
            <CustomButton
              variant='primary'
              text={'Create New Alert'}
              onClick={() => setOpenCreateNewAlertModal(true)}
              disabled
              sx={{ width: '150px' }}
            />
          ),
        ]}
      >
        <CustomTable
          id={cloudAlertManager}
          totalRows={totalCount}
          tableData={data}
          headers={['Name', 'Category', 'Source', 'Severity', 'Status', 'Configured Actions', '']}
          rowsPerPage={rowsPerPage}
          showExpandable={false}
          loading={loading}
          onPageChange={onPageChange}
          onSortChange={undefined}
          pageNumber={currentPage + 1}
          tableHeadingCenter={['Severity', 'Status']}
        />
      </BoxLayout2>
    </div>
  );
};

export default CloudAccountAlertManager;
