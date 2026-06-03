/* eslint-disable prefer-const */
import { Box } from '@mui/material';
import { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import dayjs from 'dayjs';
import apiResources from '@api1/resources';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
// TODO(ds-migration): using legacy CustomDateTimeRangePicker because ds/DateRangePicker has known bugs.
// Revisit once ds/DateRangePicker stabilises — same `{ startTime, endTime }` shape so swap is a 1-line change.
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import CloudAccountTable from './CloudAccountTable';
import Currency from '@common-new/format/Currency';
import HelpBeeModal from '@components1/helpbee';
import { ECSInstances } from './ecs';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { action } from 'src/utils/actionStyles';
import type { ICustomTable2Row } from './ec2/Instances';
import { MENU_ITEMS, DataBlock } from './common';
import Text from '@common-new/format/Text';
import TotalCostChart from '@components1/cloudaccount/CostChart';
import { getLast30Days } from '@lib/datetime';
import { usePagination } from '@hooks/usePagination';
import ServiceRecommendations from '@components1/cloudaccount/ServiceRecommendations';
// TODO(ds-migration): Loader is a leaf-utility — replace with `ds/Skeleton` blocks for content-shaped loading.
import Loader from '@components1/common/Loader';
// TODO(ds-migration): CopyableText is a leaf-utility (text + copy button) — no DS equivalent yet.
import CopyableText from '@components1/common/CopyableText';
import TagsCell from './TagsCell';
import apiCloudAccount from '@api1/cloud-account';
import Datetime from '@common-new/format/Datetime';
import { parseJSONSafely } from '@utils/common';
import Trend from '@components1/ds/Trend';
import Chip from '@components1/ds/Chip';
import { ds } from '@utils/colors';

// Define an interface for the items in resource_groupings
interface IResourceGrouping {
  tenant_id: string;
  account_id: string;
  resource_service_name: string;
  count_resource?: number | null;
  sum_spend_amount?: number | null;
  sum_recommendation_estimated_savings?: number | null;
  count_recommendation?: number | null;
  // Add any other fields that 'item' might have and are used or passed to child components
}

const CloudAccountServices = (props: {
  accountId: string | undefined;
  heading: string | undefined;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
  provider?: string;
}) => {
  const router = useRouter();
  const [rawServices, setRawServices] = useState<IResourceGrouping[]>([]);
  const [servicesCount, setServicesCount] = useState(0);
  const [_ticketData, setTicketData] = useState({} as any);
  const [isHelpBeeOpen, setHelpBeeOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const currencySymbol = useCurrencySymbol(props?.accountId);
  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const servicesTable = 'servicesTable';
  const [selectedDateRange, setSelectedDateRange] = useState<any>({
    startTime: getLast30Days().getTime(),
    endTime: new Date().getTime(),
  });

  // Previous-period costs for trend calculation
  const [prevPeriodCosts, setPrevPeriodCosts] = useState<Record<string, number>>({});

  // Service name filter
  const [selectedServiceName, setSelectedServiceName] = useState<string | null>(null);
  const [availableServiceNames, setAvailableServiceNames] = useState<{ label: string; value: string }[]>([]);

  // Wait for router to be ready, then apply serviceName query param as initial filter (once)
  const [routerReady, setRouterReady] = useState(false);
  useEffect(() => {
    if (!router.isReady || routerReady) return;
    setRouterReady(true);
    const queryServiceName = router.query.serviceName as string | undefined;
    if (queryServiceName && /^[\w\s./:-]+$/.test(queryServiceName)) {
      setSelectedServiceName(queryServiceName);
    }
  }, [router.isReady]);

  // Region filter
  const [selectedRegion, setSelectedRegion] = useState<string | null>(null);
  const [availableRegions, setAvailableRegions] = useState<{ label: string; value: string }[]>([]);

  // Tag key filter (top-level, filters all service aggregations)
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);

  // Sorting state
  const [sortObject, setSortObject] = useState<{ name: string; order: string }>({
    name: 'Total Cost',
    order: 'desc',
  });

  const sortEventChange = (e: any) => {
    setSortObject(e);
  };

  // Typed onMenuClick parameters for better type safety
  type MenuItemFromList = (typeof MENU_ITEMS)[number];
  const onMenuClick = (menuItem: MenuItemFromList, clickedData: IResourceGrouping) => {
    // menuItem.id is still valid as MENU_ITEMS have an id field
    if (menuItem.id === 0) {
      setTicketData(clickedData);
    }
    if (menuItem.id === 1) {
      setHelpBeeOpen(true);
    }
  };

  useEffect(() => {
    if (!props?.accountId) {
      return;
    }
    apiResources
      .getResourceGroupings(
        1000,
        0,
        {
          account_id: props?.accountId,
          spend_end_date: selectedDateRange.endTime ? new Date(selectedDateRange.endTime) : undefined,
          spend_start_date: selectedDateRange.startTime ? new Date(selectedDateRange.startTime) : undefined,
        },
        ['resource_service_name'],
        ['resource_service_name'],
        { name: 'resource_service_name', order: 'asc' }
      )
      .then((res: any) => {
        const serviceNames = (res.data?.resource_groupings || [])
          .map((item: any) => ({
            label: item.resource_service_name,
            value: item.resource_service_name,
          }))
          .filter((item: any) => item.value);
        setAvailableServiceNames(serviceNames);
      })
      .catch((error) => {
        console.error(error);
        setAvailableServiceNames([]);
      });
  }, [props?.accountId, selectedDateRange.startTime, selectedDateRange.endTime]);

  useEffect(() => {
    if (!props?.accountId) return;
    apiResources
      .getResourceGroupings(
        1000,
        0,
        { account_id: props.accountId, resource_service_name: selectedServiceName || undefined },
        ['resource_region'],
        ['resource_region'],
        { name: 'resource_region', order: 'asc' }
      )
      .then((res: any) => {
        const regions = (res.data?.resource_groupings || [])
          .map((item: any) => ({ label: item.resource_region, value: item.resource_region }))
          .filter((item: any) => item.value);
        setAvailableRegions(regions);
      })
      .catch(() => setAvailableRegions([]));
  }, [props?.accountId, selectedServiceName]);

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, selectedServiceName || undefined).then(setAvailableTagKeys);
    }
  }, [props?.accountId, selectedServiceName]);

  // Fetch previous-period costs for trend calculation
  useEffect(() => {
    if (!props?.accountId || !selectedDateRange.startTime || !selectedDateRange.endTime) {
      return;
    }
    const duration = selectedDateRange.endTime - selectedDateRange.startTime;
    const prevStart = new Date(selectedDateRange.startTime - duration);
    const prevEnd = new Date(selectedDateRange.startTime);

    const prevFilters: any = {
      account_id: props.accountId,
      spend_start_date: prevStart,
      spend_end_date: prevEnd,
    };
    if (selectedServiceName) {
      prevFilters.resource_service_name = selectedServiceName;
    }
    if (selectedRegion) {
      prevFilters.resource_region = selectedRegion;
    }
    if (selectedTagKey) {
      prevFilters.resource_tag_key = selectedTagKey;
    }

    apiResources
      .getResourceGroupings(1000, 0, prevFilters, ['resource_service_name'], ['resource_service_name', 'sum_spend_amount'], {
        name: 'sum_spend_amount',
        order: 'desc',
      })
      .then((res: any) => {
        const map: Record<string, number> = {};
        (res.data?.resource_groupings || []).forEach((item: any) => {
          if (item.resource_service_name && item.sum_spend_amount != null) {
            map[item.resource_service_name] = item.sum_spend_amount;
          }
        });
        setPrevPeriodCosts(map);
      })
      .catch(() => {
        setPrevPeriodCosts({});
      });
  }, [props?.accountId, selectedDateRange.startTime, selectedDateRange.endTime, selectedServiceName, selectedRegion, selectedTagKey]);

  useEffect(() => {
    if (!props?.accountId || !routerReady) {
      return;
    }
    setLoading(true);

    const filters: any = {
      account_id: props?.accountId,
      spend_end_date: selectedDateRange.endTime ? new Date(selectedDateRange.endTime) : undefined,
      spend_start_date: selectedDateRange.startTime ? new Date(selectedDateRange.startTime) : undefined,
    };

    if (selectedServiceName) {
      filters.resource_service_name = selectedServiceName;
    }

    if (selectedRegion) {
      filters.resource_region = selectedRegion;
    }

    if (selectedTagKey) {
      filters.resource_tag_key = selectedTagKey;
    }

    let sortField = 'sum_spend_amount';
    if (sortObject.name === 'Estimated Savings') {
      sortField = 'sum_recommendation_estimated_savings';
    } else if (sortObject.name === 'Recommendations') {
      sortField = 'count_recommendation';
    }

    apiResources
      .getResourceGroupings(
        rowsPerPage,
        page * rowsPerPage,
        filters,
        ['resource_service_name', 'account_id', 'tenant_id'],
        [
          'tenant_id',
          'account_id',
          'resource_service_name',
          'count_resource',
          'sum_spend_amount',
          'sum_recommendation_estimated_savings',
          'count_recommendation',
        ],
        {
          name: sortField,
          order: sortObject.order || 'desc',
        }
      )
      .then((res: any) => {
        setLoading(false);
        setRawServices(res.data?.resource_groupings || []);
        setServicesCount(res.data?.resource_groupings_aggregate?.aggregate?.count ?? 0);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [
    props?.accountId,
    page,
    rowsPerPage,
    selectedDateRange.startTime,
    selectedDateRange.endTime,
    selectedServiceName,
    selectedRegion,
    selectedTagKey,
    sortObject,
    routerReady,
  ]);

  const services = useMemo<ICustomTable2Row[][]>(() => {
    return rawServices.map((item: IResourceGrouping) => {
      const data: ICustomTable2Row[] = [];

      data.push({
        component: <Text showAutoEllipsis value={item.resource_service_name} sx={{ marginRight: ds.space[1] }} />,
        drilldownQuery: { event: item },
      });
      data.push({ component: <Text value={item.count_resource ?? '-'} /> });
      data.push({ component: <Text value={item.count_recommendation ?? '-'} /> });
      data.push({ component: <Currency prefix={currencySymbol} value={item.sum_spend_amount?.toFixed(2) ?? '-'} /> });

      const currentCost = item.sum_spend_amount;
      const prevCost = prevPeriodCosts[item.resource_service_name];
      const costChange = currentCost != null && prevCost != null && prevCost > 0 ? ((currentCost - prevCost) * 100) / prevCost : null;
      data.push({
        component:
          costChange != null && Math.abs(costChange) < 1000 ? (
            <Trend sign={costChange > 0 ? -1 : 1} value={Math.abs(costChange)} width='auto' />
          ) : (
            <Text value='-' />
          ),
      });

      data.push({ component: <Currency prefix={currencySymbol} value={item.sum_recommendation_estimated_savings?.toFixed(2) ?? '-'} /> });
      data.push({
        component: (
          <Box display={'flex'} justifyContent={'flex-end'} alignItems={'center'}>
            <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
          </Box>
        ),
      });

      return data;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rawServices, prevPeriodCosts, currencySymbol]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startTime: passedSelectedDateTime.startTime,
      endTime: passedSelectedDateTime.endTime,
    });
    setPage(0);
  };

  let last6mon = new Date();
  last6mon.setMonth(last6mon.getMonth() - 6);
  last6mon.setDate(1);
  last6mon.setHours(0, 0, 0, 0);

  const servicesHeader = useMemo(() => {
    const duration = selectedDateRange.endTime - selectedDateRange.startTime;
    const prevStart = selectedDateRange.startTime - duration;
    const prevEnd = selectedDateRange.startTime;
    const prevPeriodLabel = `${dayjs(prevStart).format('MMM D, YYYY')} – ${dayjs(prevEnd).format('MMM D, YYYY')}`;
    return [
      { name: 'Service Name', width: '28%' },
      { name: 'Resources', width: '10%' },
      { name: 'Recommendations', width: '14%', sortEnabled: true },
      { name: 'Total Cost', width: '14%', sortEnabled: true },
      {
        name: '% Change',
        width: '12%',
        info: `Percentage change in cost compared to previous period (${prevPeriodLabel}).`,
      },
      { name: 'Estimated Savings', width: '14%', sortEnabled: true },
      { name: '', width: '3%' },
    ];
  }, [selectedDateRange.startTime, selectedDateRange.endTime]);

  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setHelpBeeOpen(false)} />
      <ListingLayout id='right-sizing'>
        <ListingLayout.Toolbar
          title={props.heading === undefined ? '' : props.heading}
          actions={
            <>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: selectedDateRange.startTime,
                  endTime: selectedDateRange.endTime,
                  shortcutClickTime: 0,
                }}
                onChange={(result: any) => {
                  const val = result?.selection ?? result;
                  if (val) handleDateRangeChange(val);
                }}
                minDate={dayjs(last6mon)}
              />
              <DownloadButton id={`${servicesTable}-download`} onClick={() => ({ tableId: servicesTable })} />
            </>
          }
        >
          <FilterDropdown
            id='services-filter-service-name'
            label='Service Name'
            value={selectedServiceName}
            options={availableServiceNames}
            onSelect={(e: any) => {
              setSelectedServiceName(e?.target?.value || null);
              setPage(0);
            }}
          />
          <FilterDropdown
            id='services-filter-region'
            label='Region'
            value={selectedRegion}
            options={availableRegions}
            onSelect={(e: any) => {
              setSelectedRegion(e?.target?.value || null);
              setPage(0);
            }}
          />
          <FilterDropdown
            id='services-filter-tag-key'
            label='Tag Key'
            value={selectedTagKey}
            options={availableTagKeys}
            onSelect={(e: any) => {
              setSelectedTagKey(e?.target?.value || null);
              setPage(0);
            }}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CloudAccountTable
            id={servicesTable}
            headers={servicesHeader}
            data={services}
            rowsPerPage={rowsPerPage}
            onPageChange={changePage}
            totalRows={servicesCount}
            loading={loading}
            showExpandable={true}
            pageNumber={page + 1}
            tableHeadingCenter={props.tableHeadingCenter}
            showUpdatedTable
            stickyColumnIndex={props.stickyColumnIndex}
            sort={sortObject}
            onSortChange={sortEventChange}
            expandable={{
              tabs: [
                {
                  componentFn: createServiceResourcesComponentFn(
                    props.accountId ?? '',
                    currencySymbol,
                    selectedDateRange,
                    selectedRegion,
                    selectedTagKey
                  ),
                  text: 'Resources',
                },
                {
                  componentFn: createCostTrendComponentFn(props.accountId ?? ''),
                  text: 'Cost Trend',
                },
                {
                  componentFn: createServiceRecommendationsComponentFn(props.accountId ?? '', props.provider ?? 'AWS'),
                  text: 'Recommendations',
                },
              ],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

const RESOURCES_HEADER = [
  { name: 'Resource Name', width: '25%' },
  { name: 'Type', width: '12%' },
  { name: 'Region', width: '12%' },
  { name: 'Tags', width: '16%' },
  { name: 'Recommendations', width: '12%', sortEnabled: true },
  { name: 'Total Cost', width: '10%', sortEnabled: true },
  { name: 'Estimated Savings', width: '13%', sortEnabled: true },
] as never[];

// Define an interface for the items in resource_groupings for CloudAccountResources
interface IResourceDetail {
  tenant_id: string;
  account_id: string;
  resource_id: string;
  resource_name: string;
  resource_type: string;
  resource_region: string;
  sum_spend_amount?: number | null;
  sum_recommendation_estimated_savings?: number | null;
  count_recommendation?: number | null;
  meta?: any;
  tags?: any;
  resource_tags?: any;
  arn?: string;
  status?: string;
  resourse_created_on?: string;
  first_seen?: string;
  last_seen?: string;
  service_name?: string;
  // Ensure all fields accessed from 'item' below are included here
}

// Component to display detailed resource information
const ResourceDetails = (props: { resourceData: IResourceDetail }) => {
  const [detailedResource, setDetailedResource] = useState<any>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!props.resourceData?.resource_id) {
      setLoading(false);
      return;
    }

    setLoading(true);
    apiResources
      .getResourceDetils(props.resourceData.resource_id, {})
      .then((res: any) => {
        const data = res.data;
        if (data) {
          data.meta = parseJSONSafely(data.meta) as any;
          data.tags = parseJSONSafely(data.tags) as any;
        }
        setDetailedResource(data);
        setLoading(false);
      })
      .catch((error) => {
        console.error('Error fetching resource details:', error);
        setLoading(false);
      });
  }, [props.resourceData?.resource_id]);

  if (loading) {
    return <Loader style={{ height: '200px', width: '100%' }} />;
  }

  if (!detailedResource) {
    return <Box sx={{ padding: ds.space[5] }}>No detailed information available for this resource.</Box>;
  }

  return (
    <>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
          columnGap: ds.space[4],
          rowGap: ds.space[5],
          mb: ds.space[5],
          backgroundColor: ds.background[100],
          padding: ds.space[5],
          borderRadius: ds.radius.md,
        }}
      >
        {detailedResource.arn && (
          <Box>
            <Box sx={{ color: ds.gray[500], fontSize: ds.text.small, fontWeight: ds.weight.regular, mb: ds.space[1] }}>ARN</Box>
            <Box sx={{ fontSize: ds.text.body }}>
              <CopyableText copyableText={detailedResource.arn}>
                <Text showAutoEllipsis value={detailedResource.arn} />
              </CopyableText>
            </Box>
          </Box>
        )}
        {detailedResource.status && <DataBlock title={'Status'} data={detailedResource.status} isCopyable={false} />}
        {detailedResource.resourse_created_on && (
          <Box>
            <Box sx={{ color: ds.gray[500], fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1] }}>Created On</Box>
            <Box sx={{ width: 'fit-content' }}>
              <Datetime value={detailedResource.resourse_created_on} />
            </Box>
          </Box>
        )}
        {detailedResource.first_seen && (
          <Box>
            <Box sx={{ color: ds.gray[500], fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1] }}>First Seen</Box>
            <Box sx={{ width: 'fit-content' }}>
              <Datetime value={detailedResource.first_seen} />
            </Box>
          </Box>
        )}
        {detailedResource.last_seen && (
          <Box>
            <Box sx={{ color: ds.gray[500], fontSize: ds.text.small, fontWeight: ds.weight.semibold, mb: ds.space[1] }}>Last Seen</Box>
            <Box sx={{ width: 'fit-content' }}>
              <Datetime value={detailedResource.last_seen} />
            </Box>
          </Box>
        )}
        {(detailedResource.recommendations_aggregate?.aggregate?.count || 0) > 0 && (
          <DataBlock title={'Open Recommendations'} data={detailedResource.recommendations_aggregate.aggregate.count.toString()} isCopyable={false} />
        )}
        {(detailedResource.recommendations_aggregate?.aggregate?.sum?.estimated_savings || 0) > 0 && (
          <DataBlock
            title={'Potential Savings'}
            data={`$${detailedResource.recommendations_aggregate.aggregate.sum.estimated_savings.toFixed(2)}`}
            isCopyable={false}
          />
        )}
        {(detailedResource.critical_recommendations_aggregate?.aggregate?.count || 0) > 0 && (
          <DataBlock
            title={'Critical Recommendations'}
            data={detailedResource.critical_recommendations_aggregate.aggregate.count.toString()}
            isCopyable={false}
          />
        )}
      </Box>

      {/* Display meta configuration if available */}
      {detailedResource.meta && Object.keys(detailedResource.meta).length > 0 && (
        <Box sx={{ mb: ds.space[5] }}>
          <Box
            sx={{
              fontSize: ds.text.bodyLg,
              fontWeight: ds.weight.medium,
              color: ds.gray[700],
              mb: ds.space[2],
            }}
          >
            Resource Configuration
          </Box>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '1.5fr 1.5fr 1.5fr',
              columnGap: ds.space[4],
              rowGap: ds.space[5],
              padding: ds.space[5],
              backgroundColor: ds.background[100],
              borderRadius: ds.radius.md,
            }}
          >
            {Object.entries(detailedResource.meta)
              .filter(([key, value]) => {
                if (key === 'Tags') {
                  return false;
                }
                // Only show simple primitive values (not objects or arrays)
                return value !== null && value !== undefined && typeof value !== 'object';
              })
              .map(([key, value]: [string, any]) => {
                // Format the key to be more readable
                const formattedKey = key.replace(/([A-Z])/g, ' $1').trim();

                // Check if it's a date/timestamp field
                const isDateField = key.toLowerCase().includes('time') || key.toLowerCase().includes('date');
                const displayValue = isDateField && typeof value === 'string' ? new Date(value).toLocaleString() : String(value);

                return (
                  <Box key={key} sx={{ minWidth: 0, overflow: 'hidden' }}>
                    <Box sx={{ color: ds.gray[500], fontSize: ds.text.small, fontWeight: ds.weight.regular, mb: ds.space[1] }}>{formattedKey}</Box>
                    <Box sx={{ fontSize: ds.text.body }}>
                      {typeof value === 'string' && value.length > 10 ? (
                        <CopyableText copyableText={displayValue}>
                          <Text showAutoEllipsis value={displayValue} />
                        </CopyableText>
                      ) : (
                        <Text showAutoEllipsis value={displayValue} />
                      )}
                    </Box>
                  </Box>
                );
              })}
          </Box>
        </Box>
      )}

      {/* Display tags if available */}
      {detailedResource.tags && Object.keys(detailedResource.tags).length > 0 && (
        <Box sx={{ mb: ds.space[5] }}>
          <Box
            sx={{
              fontSize: ds.text.bodyLg,
              fontWeight: ds.weight.medium,
              color: ds.gray[700],
              mb: ds.space[2],
            }}
          >
            Tags
          </Box>
          <Box
            sx={{
              display: 'flex',
              flexWrap: 'wrap',
              gap: ds.space[2],
              padding: ds.space[3],
              backgroundColor: ds.background[100],
              borderRadius: ds.radius.md,
            }}
          >
            {Object.entries(detailedResource.tags).map(([key, value]: [string, any]) => (
              <Chip key={key} variant='tag' size='sm' tone='neutral'>
                <Box component='span' sx={{ fontWeight: ds.weight.semibold }}>
                  {key}
                </Box>
                <Box component='span' sx={{ mx: ds.space[1], color: ds.gray[400] }}>
                  =
                </Box>
                <Box component='span'>{Array.isArray(value) ? value.join(', ') : String(value)}</Box>
              </Chip>
            ))}
          </Box>
        </Box>
      )}
    </>
  );
};

const createResourceDetailsComponentFn = (_accountId: string | undefined) => (_opt: any, drilldownQuery: any) =>
  <ResourceDetails resourceData={drilldownQuery.event} />;

const createServiceResourcesComponentFn =
  (
    accountId: string,
    currencySymbol: string | undefined,
    selectedDateRange: { startTime: number; endTime: number },
    region: string | null,
    tagKey: string | null
  ) =>
  (_opt: any, drilldownQuery: any) =>
    (
      <div>
        <CloudAccountResources
          accountId={accountId}
          serviceName={drilldownQuery.event.resource_service_name}
          currencySymbol={currencySymbol}
          selectedDateRange={selectedDateRange}
          region={region}
          tagKey={tagKey}
        />
      </div>
    );

const createCostTrendComponentFn = (accountId: string) => (_opt: any, drilldownQuery: any) =>
  (
    <div>
      <TotalCostChart accountId={accountId} resourceServiceName={drilldownQuery.event.resource_service_name} />
    </div>
  );

const createServiceRecommendationsComponentFn = (accountId: string, provider: string) => (_opt: any, drilldownQuery: any) =>
  (
    <div>
      <ServiceRecommendations accountId={accountId} serviceName={drilldownQuery.event.resource_service_name} provider={provider} />
    </div>
  );

const CloudAccountResources = (props: {
  accountId: string;
  serviceName: string | undefined;
  currencySymbol: string | undefined;
  selectedDateRange: { startTime: number; endTime: number };
  region: string | null;
  tagKey: string | null;
}) => {
  const [loading, setLoading] = useState(false);
  const [resources, setResources] = useState<ICustomTable2Row[][]>([]);
  const [resourcesCount, setResourcesCount] = useState(0);
  const { page, rowsPerPage, changePage, setPage } = usePagination(10);

  const [selectedType, setSelectedType] = useState<string | null>(null);
  const [selectedRegion, setSelectedRegion] = useState<string | null>(props.region);
  const [availableTypes, setAvailableTypes] = useState<{ label: string; value: string }[]>([]);
  const [availableRegions, setAvailableRegions] = useState<{ label: string; value: string }[]>([]);
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(props.tagKey);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);

  const [sortObject, setSortObject] = useState<{ name: string; order: string }>({
    name: 'Total Cost',
    order: 'desc',
  });

  const sortEventChange = (e: any) => {
    setSortObject(e);
  };

  const isECSService = props.serviceName === 'ecs' || props.serviceName === 'AmazonECS' || props.serviceName === 'Amazon Elastic Container Service';

  useEffect(() => {
    setSelectedRegion(props.region);
  }, [props.region]);

  useEffect(() => {
    setSelectedTagKey(props.tagKey);
  }, [props.tagKey]);

  useEffect(() => {
    setSelectedType(null);
    setSelectedRegion(props.region);
    setAvailableTypes([]);
    setAvailableRegions([]);
    setSelectedTagKey(props.tagKey);
    setAvailableTagKeys([]);
  }, [props?.serviceName]);

  useEffect(() => {
    if (props?.accountId && props?.serviceName) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, props.serviceName).then(setAvailableTagKeys);
    }
  }, [props?.accountId, props?.serviceName]);

  useEffect(() => {
    if (!props?.accountId || isECSService || !props?.serviceName) {
      return;
    }
    apiResources
      .getResourceGroupings(
        1000,
        0,
        {
          account_id: props?.accountId,
          resource_service_name: props?.serviceName,
        },
        ['resource_type', 'resource_region'],
        ['resource_type', 'resource_region'],
        { name: 'resource_type', order: 'asc' }
      )
      .then((res: any) => {
        const data = res.data?.resource_groupings || [];

        const uniqueTypes = new Set();
        const uniqueRegions = new Set();

        data.forEach((item: any) => {
          if (item.resource_type) {
            uniqueTypes.add(item.resource_type);
          }
          if (item.resource_region) {
            uniqueRegions.add(item.resource_region);
          }
        });
        const types = Array.from(uniqueTypes)
          .sort((a: any, b: any) => String(a).localeCompare(String(b)))
          .map((type: any) => ({
            label: type,
            value: type,
          }));

        const regions = Array.from(uniqueRegions)
          .sort((a: any, b: any) => String(a).localeCompare(String(b)))
          .map((region: any) => ({
            label: region,
            value: region,
          }));

        setAvailableTypes(types);
        setAvailableRegions(regions);
      })
      .catch((error) => {
        console.error(error);
        setAvailableTypes([]);
        setAvailableRegions([]);
      });
  }, [props?.accountId, props?.serviceName, isECSService]);

  useEffect(() => {
    if (!props?.accountId || isECSService || props.currencySymbol === undefined) {
      setResources([]); // Clear any previous generic resources
      setResourcesCount(0);
      return;
    }
    setLoading(true);

    // Build filter object with selected type, region, and date range
    const filters: any = {
      account_id: props?.accountId,
      resource_service_name: props?.serviceName,
      spend_end_date: props.selectedDateRange.endTime ? new Date(props.selectedDateRange.endTime) : undefined,
      spend_start_date: props.selectedDateRange.startTime ? new Date(props.selectedDateRange.startTime) : undefined,
    };

    if (selectedType) {
      filters.resource_type = selectedType;
    }

    if (selectedRegion) {
      filters.resource_region = selectedRegion;
    }

    if (selectedTagKey) {
      filters.resource_tag_key = selectedTagKey;
    }

    let sortField = 'sum_spend_amount';
    if (sortObject.name === 'Estimated Savings') {
      sortField = 'sum_recommendation_estimated_savings';
    } else if (sortObject.name === 'Recommendations') {
      sortField = 'count_recommendation';
    }

    apiResources
      .getResourceGroupings(
        rowsPerPage,
        page * rowsPerPage,
        filters,
        ['resource_id', 'resource_name', 'resource_type', 'resource_region', 'resource_tags', 'account_id', 'tenant_id'],
        [
          'tenant_id',
          'account_id',
          'resource_id',
          'resource_name',
          'resource_type',
          'resource_region',
          'resource_tags',
          'sum_spend_amount',
          'sum_recommendation_estimated_savings',
          'count_recommendation',
        ],
        {
          name: sortField,
          order: sortObject.order || 'desc',
        }
      )
      .then((res: any) => {
        setLoading(false);
        const genericResourceData = (res.data?.resource_groupings || []).map((item: IResourceDetail) => {
          let data: ICustomTable2Row[] = [];

          data.push({
            component: <Text showAutoEllipsis value={item.resource_name} sx={{ marginRight: ds.space[1] }} />,
            drilldownQuery: {
              event: item,
            },
          });
          data.push({
            component: <Text showAutoEllipsis value={item.resource_type} sx={{ marginRight: ds.space[1] }} />,
          });
          data.push({
            component: <Text showAutoEllipsis value={item.resource_region} sx={{ marginRight: ds.space[1] }} />,
          });
          data.push({
            component: <TagsCell tags={typeof item.resource_tags === 'string' ? JSON.parse(item.resource_tags) : item.resource_tags} />,
          });
          data.push({
            component: <Text value={item.count_recommendation ?? '-'} />,
          });
          data.push({
            component: <Currency prefix={props.currencySymbol} value={item.sum_spend_amount?.toFixed(2) ?? '-'} />,
          });
          data.push({
            component: (
              <Box sx={{ textAlign: 'center', width: '100%' }}>
                <Currency prefix={props.currencySymbol} value={item.sum_recommendation_estimated_savings?.toFixed(2) ?? '-'} />
              </Box>
            ),
          });
          return data;
        });
        setResources(genericResourceData);
        setResourcesCount(res.data?.resource_groupings_aggregate?.aggregate?.count || 0);
      })
      .catch(() => {
        setLoading(false);
        setResources([]);
        setResourcesCount(0);
      });
  }, [
    props?.accountId,
    page,
    rowsPerPage,
    props?.serviceName,
    isECSService,
    props.currencySymbol,
    selectedType,
    selectedRegion,
    selectedTagKey,
    sortObject,
    props.selectedDateRange.startTime,
    props.selectedDateRange.endTime,
  ]);

  if (isECSService) {
    return <ECSInstances accountId={props.accountId} heading='ECS Resources' />;
  }

  return (
    <ListingLayout id='service-resource-listing'>
      <ListingLayout.Toolbar
        actions={<DownloadButton id='service-resource-listing-table-download' onClick={() => ({ tableId: 'service-resource-listing-table' })} />}
      >
        <FilterDropdown
          id='resources-filter-type'
          label='Type'
          value={selectedType}
          options={availableTypes}
          onSelect={(e: any) => {
            setSelectedType(e?.target?.value || null);
            setPage(0);
          }}
        />
        <FilterDropdown
          id='resources-filter-region'
          label='Region'
          value={selectedRegion}
          options={availableRegions}
          onSelect={(e: any) => {
            setSelectedRegion(e?.target?.value || null);
            setPage(0);
          }}
        />
        <FilterDropdown
          id='resources-filter-tag-key'
          label='Tag Key'
          value={selectedTagKey}
          options={availableTagKeys}
          onSelect={(e: any) => {
            setSelectedTagKey(e?.target?.value || null);
            setPage(0);
          }}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <CloudAccountTable
          id={'service-resource-listing-table'}
          headers={RESOURCES_HEADER}
          data={resources}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={resourcesCount}
          loading={loading}
          pageNumber={page + 1}
          showUpdatedTable
          tableHeadingCenter={['Estimated Savings']}
          sort={sortObject}
          onSortChange={sortEventChange}
          showExpandable={true}
          expandable={{
            tabs: [
              {
                componentFn: createResourceDetailsComponentFn(props.accountId),
                text: 'Details',
                key: 'resource-details',
              },
            ],
          }}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default CloudAccountServices;
