import React, { useEffect, useState } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import DownloadButton from '@common-new/DownloadButton';
import apiAutoPilot from '@api1/autoPilot';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import CustomLabels from '@common-new/widgets/CustomLabels';
import DateTime from '@common-new/format/Datetime';
import { useRouter } from 'next/router';
import NDialog from '@common-new/modal/NDialog';
import { hasWriteAccess } from '@lib/auth';
import { useData } from '@context/DataContext';
import { action } from 'src/utils/actionStyles';
import apiUser from '@api1/user';
import PropTypes from 'prop-types';
import Text from '@common-new/format/Text';
import CustomLink from '@components1/common/CustomLink';
import { validate as isValidUUID } from 'uuid';
import CustomTable from '@common-new/tables/CustomTable2';
import AutoPilotApprovalStatusListingModal from '@components1/autopilot/AutoPilotApprovalStatusListingModal';
import { toast as snackbar } from '@components1/ds/Toast';

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

const STATUS_OPTIONS = [
  { label: 'Active', value: 'Active' },
  { label: 'Disabled', value: 'Disabled' },
  { label: 'Dryrun', value: 'Dryrun' },
  { label: 'Draft', value: 'DRAFT' },
];

const CATEGORY_OPTIONS = [
  { label: 'Vertical RightSizing', value: 'continuous_rightsize' },
  { label: 'Scheduled Vertical RightSizing', value: 'vertical_rightsize' },
  { label: 'Horizontal RightSizing', value: 'horizontal_rightsize' },
  { label: 'PVC RightSizing', value: 'pvc_rightsize' },
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
  const [selectedName, setSelectedName] = useState('');
  const [appliedName, setAppliedName] = useState('');
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
    const nameToUse = appliedName || router.query?.name;
    if (nameToUse) {
      if (isValidUUID(nameToUse)) {
        query['id'] = nameToUse;
      } else {
        query['name'] = nameToUse.length > 2 ? nameToUse : null;
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
  }, [currentPage, recordsPerPage, selectedStatus, router.query?.accountId, router.query?.name, selectedCategory, refresh, appliedName]);

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
      <ListingLayout id='box-layout-auto-pilot'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: autoPilotListId })} />}>
          <FilterDropdown
            id='auto-pilot-filter-status'
            label='Status'
            options={STATUS_OPTIONS}
            value={selectedStatus}
            onSelect={(e) => {
              setSelectedStatus(e?.target?.value);
              setCurrentPage(0);
            }}
          />
          <FilterDropdown
            id='auto-pilot-filter-category'
            label='Category'
            options={CATEGORY_OPTIONS}
            value={selectedCategory}
            onSelect={(e) => {
              setSelectedCategory(e?.target?.value);
              setCurrentPage(0);
            }}
          />
          <CustomSearch
            id='auto-pilot-name-search'
            value={selectedName}
            label='Search by name or id'
            onChange={(next) => {
              setSelectedName((prev) => {
                if (prev.trim() !== '' && next.trim() === '') {
                  setAppliedName('');
                  setCurrentPage(0);
                }
                return next;
              });
            }}
            onEnterPress={() => {
              setAppliedName(selectedName);
              setCurrentPage(0);
            }}
            onClear={() => {
              setSelectedName('');
              setAppliedName('');
              setCurrentPage(0);
            }}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default AutoOptimizeListingTable;

AutoOptimizeListingTable.propTypes = {
  defaultQuery: PropTypes.object,
  handleOpenCreateAutoOptimize: PropTypes.func,
  setAutoOptimizeData: PropTypes.func,
  refresh: PropTypes.bool,
  autoOptimizeData: PropTypes.object,
};
