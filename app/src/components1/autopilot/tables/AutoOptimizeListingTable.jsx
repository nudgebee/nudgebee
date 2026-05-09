import React, { useState, useEffect } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import apiAutoPilot from '@api1/autoPilot';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import DateTime from '@components1/common/format/Datetime';
import { useRouter } from 'next/router';
import NDialog from '@components1/common/modal/NDialog';
import { hasWriteAccess } from '@lib/auth';
import { useData } from '@context/DataContext';
import { action } from 'src/utils/actionStyles';
import apiUser from '@api1/user';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
import CustomLink from '@components1/common/CustomLink';
import { validate as isValidUUID } from 'uuid';
import CustomTable from '@components1/common/tables/CustomTable2';
import AutoPilotApprovalStatusListingModal from '@components1/autopilot/AutoPilotApprovalStatusListingModal';
import { snackbar } from '@components1/common/snackbarService';

const LISTING_HEADER = [
  'Name',
  'Status',
  { name: 'Category', width: '15%' },
  'Resource',
  'Last Executed Time',
  'Next Scheduled',
  'Created At',
  'Created By',
  'Updated By',
  '',
];
const AutoOptimizeListingTable = ({ defaultQuery = {}, handleOpenCreateAutoOptimize, setAutoOptimizeData, refresh, autoOptimizeData }) => {
  const router = useRouter();
  const autoPilotListId = 'auto-pilot';
  const { setAutoOptimizeNameRequest } = useData();
  const categoryMap = {
    vertical_rightsize: 'Scheduled Vertical RightSizing',
    continuous_rightsize: 'Continuous Vertical RightSizing',
    horizontal_rightsize: 'Horizontal RightSizing',
    pvc_rightsize: 'PVC RightSizing',
  };

  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [selectedStatus, setSelectedStatus] = useState('Active');
  const [selectedName, setSelectedName] = useState(null);
  const [loading, setLoading] = useState(false);
  const [activeToggleMenuOpen, setActiveToggleMenuOpen] = useState(false);
  const [selectedAutopilot, setSelectedAutopilot] = useState({ name: '', status: '', id: '', account_id: '' });
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [selectedCategory, setSelectedCategory] = useState('');
  const [approvalStatusModalOpen, setApprovalStatusModalOpen] = useState(false);

  const getMenuItems = (item) => {
    if (!hasWriteAccess(item?.account_id || router.query?.accountId || router.query?.KubernetesDetails)) {
      return [];
    }

    let menuItems = [];
    menuItems.push({
      label: 'Edit',
      id: 1,
    });
    if (item.status == 'Active') {
      menuItems.push({
        label: 'Disable',
        id: 0,
      });
    } else if (item?.status != 'DRAFT') {
      menuItems.push({
        label: 'Enable',
        id: 0,
      });
    } else if (item?.status == 'DRAFT') {
      menuItems.push({
        label: 'Check Status',
        id: 2,
      });
    }

    return menuItems;
  };

  const listAutoPilot = () => {
    let query = {};
    setData([]);
    setTotalCount(0);

    if (router.query?.accountId) {
      query['accountId'] = router.query?.accountId;
    }
    if (defaultQuery) {
      query = { ...query, ...defaultQuery };
    }
    if (selectedStatus) {
      query['status'] = selectedStatus;
    }
    if (router.query?.name || selectedName) {
      if (isValidUUID(selectedName)) {
        query['id'] = selectedName;
      } else {
        query['name'] = selectedName?.length > 2 ? selectedName : null;
      }
    }
    if (selectedCategory) {
      query['category'] = selectedCategory;
    }
    const setAutoOptimizeName = (item) => {
      setAutoOptimizeNameRequest(item?.name);
    };

    setLoading(true);
    apiAutoPilot
      .listAutoPilot(recordsPerPage, currentPage * recordsPerPage, query)
      .then((res) => {
        let data = res?.data?.auto_pilot_listing?.map((item) => {
          return [
            {
              component: (
                <CustomLink onClick={() => setAutoOptimizeName(item)} href={`/auto-pilot/task/${item?.id}?accountId=${item.account_id}`}>
                  {item?.name}
                </CustomLink>
              ),
              drilldownQuery: {
                rule: item.rule,
              },
            },
            { component: <CustomLabels margin='auto' text={item?.status} /> },
            { component: <Text value={categoryMap[item?.category] ?? item?.category} /> },
            {
              component: (
                <CustomLink
                  href={
                    item.category == 'pvc_rightsize'
                      ? `/kubernetes/details/${item?.account_id}?pvName=${
                          item?.auto_optimize_resource_maps?.[0]?.resource_identifier?.name ?? ''
                        }#kubernetes/pvc`
                      : `/kubernetes/details/${item?.account_id}?namespace=${
                          item?.auto_optimize_resource_maps?.[0]?.resource_identifier?.namespace ?? ''
                        }&workloadName=${item?.auto_optimize_resource_maps?.[0]?.resource_identifier?.name ?? ''}#kubernetes/applications`
                  }
                  passHref={true}
                >
                  <Text
                    showAutoEllipsis
                    sx={{
                      textDecoration: 'underline',
                      textDecorationColor: 'lightgray',
                    }}
                    value={item?.auto_optimize_resource_maps
                      ?.map((m) => {
                        let r = m.resource_identifier?.namespace;
                        if (r == null) {
                          r = '';
                        } else {
                          r = '/' + r;
                        }
                        if (m.resource_identifier?.type) {
                          r += '/' + m.resource_identifier?.type;
                        }
                        if (m.resource_identifier?.name) {
                          r += '/' + m.resource_identifier?.name;
                        }
                        return r;
                      })
                      ?.join(',')}
                  />
                </CustomLink>
              ),
            },
            { component: <DateTime value={item?.last_executed_time ? item?.last_executed_time : null} /> },
            {
              component:
                item?.next_schedule_time && item?.status != 'Disabled' ? <DateTime value={item.next_schedule_time + 'Z'} type='future' /> : '-',
            },
            { component: <DateTime value={item.creation_date} /> },
            { component: <Text value={item?.user?.display_name} /> },
            { component: <Text value={item?.user_updated_by ? item?.user_updated_by?.display_name : '-'} /> },

            {
              component: hasWriteAccess(item.account_id || router.query?.accountId || defaultQuery?.accountId) ? (
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />
              ) : (
                <></>
              ),
            },
          ];
        });

        let totalCount = res?.data?.auto_pilot_aggregate?.aggregate?.count;
        setData(data);
        setTotalCount(totalCount);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listAutoPilot();
  }, [currentPage, recordsPerPage, selectedStatus, router.query?.accountId, selectedCategory, refresh]);

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setActiveToggleMenuOpen(true);
      setSelectedAutopilot(data);
    } else {
      if (
        data?.category === 'pvc_rightsize' ||
        data?.category === 'vertical_rightsize' ||
        data?.category === 'horizontal_rightsize' ||
        data?.category === 'continuous_rightsize'
      ) {
        setAutoOptimizeData(data);
        if (menuItem.id === 1) {
          handleOpenCreateAutoOptimize(data?.category);
        } else if (menuItem.id === 2) {
          setApprovalStatusModalOpen(true);
        }
      }
    }
  };

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const onNameFilterChange = (e) => {
    setSelectedName(e?.target?.value);
  };

  const onEnterPress = () => {
    if (currentPage !== 0) {
      setCurrentPage(0);
    } else {
      listAutoPilot();
    }
  };

  const handleSubmit = () => {
    apiAutoPilot
      .updateAutoPilotStatus(
        selectedAutopilot.id,
        selectedAutopilot?.account_id || router.query?.accountId,
        selectedAutopilot.status == 'Active' ? 'Disabled' : 'Active'
      )
      .then((res) => {
        if (res?.data.errors) {
          snackbar.error(
            `Failed to update ${selectedAutopilot.status == 'Active' ? 'Disabled' : 'Active'} status on autopilot "${selectedAutopilot.name}"`
          );
        } else {
          snackbar.success(`Autopilot "${selectedAutopilot.name}" ${selectedAutopilot.status == 'Active' ? 'disabled' : 'enabled'} Successfully`);
          listAutoPilot();
          setActiveToggleMenuOpen(false);
        }
      })
      .catch(() => {
        snackbar.error(
          `Failed to update ${selectedAutopilot.status == 'Active' ? 'Disabled' : 'Active'} status on autopilot "${selectedAutopilot.name}"`
        );
      });
  };
  return (
    <>
      <AutoPilotApprovalStatusListingModal
        id={autoOptimizeData?.id}
        name={autoOptimizeData?.name}
        open={approvalStatusModalOpen}
        handleClose={() => {
          setApprovalStatusModalOpen(false);
          setAutoOptimizeData();
        }}
      />
      <NDialog
        buttonText='Confirm'
        handleClose={() => {
          setActiveToggleMenuOpen(false);
        }}
        dialogTitle={`${selectedAutopilot.status == 'Active' ? 'Disable' : 'Enable'} Auto Optimize "${selectedAutopilot.name}"`}
        handleSubmit={handleSubmit}
        open={activeToggleMenuOpen}
        dialogContent={''}
        additionalComponent={undefined}
      />
      <BoxLayout2
        id='box-layout-auto-pilot'
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: [
              { label: 'Active', value: 'Active' },
              { label: 'Disabled', value: 'Disabled' },
              { label: 'Dryrun', value: 'Dryrun' },
              { label: 'Draft', value: 'DRAFT' },
            ],
            onSelect: (e) => {
              setSelectedStatus(e.target.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Status',
            value: selectedStatus,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: [
              { label: 'Vertical RightSizing', value: 'continuous_rightsize' },
              { label: 'Scheduled Vertical RightSizing', value: 'vertical_rightsize' },
              { label: 'Horizontal RightSizing', value: 'horizontal_rightsize' },
              { label: 'PVC RightSizing', value: 'pvc_rightsize' },
            ],
            onSelect: (e) => {
              setSelectedCategory(e.target.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Category',
            value: selectedCategory,
          },
          {
            type: 'search',
            enabled: true,
            onSelect: onNameFilterChange,
            minWidth: '150px',
            label: 'type name/id & press enter',
            onEnter: onEnterPress,
          },
        ]}
        sharingOptions={{
          sharing: {
            enabled: true,
            onClick: null,
          },
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: autoPilotListId,
              };
            },
          },
        }}
      >
        <CustomTable
          id={autoPilotListId}
          headers={LISTING_HEADER}
          rowsPerPage={recordsPerPage}
          tableData={data}
          onPageChange={onPageChange}
          totalRows={totalCount}
          loading={loading}
          tableHeadingCenter={['Status']}
          stickyColumnIndex='10'
          pageNumber={currentPage + 1}
        />
      </BoxLayout2>
    </>
  );
};

export default AutoOptimizeListingTable;

AutoOptimizeListingTable.propTypes = {
  defaultQuery: PropTypes.object,
  handleOpenCreateAutoOptimize: PropTypes.func,
  setAutoOptimizeData: PropTypes.func,
  refresh: PropTypes.boolean,
  autoOptimizeData: PropTypes.object,
};
