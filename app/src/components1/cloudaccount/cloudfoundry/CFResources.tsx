import { Box, Typography, Chip } from '@mui/material';
import React, { useEffect, useState } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import BoxLayout2 from '@common/BoxLayout2';
import CloudAccountTable from '../CloudAccountTable';
import Datetime from '@components1/common/format/Datetime';
import { DataBlock } from '../common';
import { usePagination } from '@hooks/usePagination';
import TagsCell from '../TagsCell';
import CloudAccountEvents from '../CloudAccountEvents';
import type { ICustomTable2Row } from '../ec2/Instances';

// --- Headers per resource type ---
const HEADERS: Record<string, string[]> = {
  organizations: ['Name', 'Apps', 'Instances', 'Memory', 'Status', 'Created At', ''],
  spaces: ['Name', 'Organization', 'Apps', 'Memory', 'Status', 'Created At', ''],
  routes: ['URL / Host', 'Space', 'Protocol', 'Destinations', 'Status', 'Created At', ''],
};

const CustomText = (data: { text1: string | null; subtext1?: string | null }) => (
  <>
    <Box sx={{ display: 'flex', flexDirection: 'row' }}>
      {data.text1 && <Typography sx={{ color: '#374151', fontWeight: 400, fontSize: 13 }}>{data.text1}</Typography>}
    </Box>
    {data.subtext1 && <Typography sx={{ color: '#9F9F9F', fontSize: 12 }}>{data.subtext1}</Typography>}
  </>
);

const StatusBadge = ({ status }: { status: string }) => {
  const isActive = status?.toLowerCase() === 'active';
  return (
    <Typography
      sx={{
        color: isActive ? '#059669' : '#DC2626',
        fontWeight: 500,
        fontSize: 12,
        backgroundColor: isActive ? '#D1FAE5' : '#FEE2E2',
        borderRadius: '4px',
        padding: '2px 8px',
        display: 'inline-block',
      }}
    >
      {isActive ? 'Active' : status || '-'}
    </Typography>
  );
};

// --- Parse JSON string fields from cloud_resources_list_v2 ---
const parseResource = (r: any) => ({
  ...r,
  meta: typeof r.meta === 'string' ? JSON.parse(r.meta || '{}') : r.meta || {},
  tags: typeof r.tags === 'string' ? JSON.parse(r.tags || '{}') : r.tags || {},
});

// --- Types for enrichment data ---
type AppEnrichment = {
  orgApps: Record<string, { count: number; instances: number; memoryMB: number }>;
  spaceApps: Record<string, { count: number; memoryMB: number }>;
  appNames: Record<string, string>; // GUID -> app name mapping for route destinations
};

// --- Row mappers per resource type ---
const mapOrganizationRow = (item: any, enrichment?: AppEnrichment): ICustomTable2Row[] => {
  const data: ICustomTable2Row[] = [];
  const orgName = item.name || '-';
  const orgStats = enrichment?.orgApps?.[orgName] || { count: 0, instances: 0, memoryMB: 0 };
  data.push({ component: <CustomText text1={orgName} />, drilldownQuery: item });
  data.push({ component: <CustomText text1={String(orgStats.count)} /> });
  data.push({ component: <CustomText text1={String(orgStats.instances)} /> });
  data.push({ component: <CustomText text1={orgStats.memoryMB > 0 ? `${orgStats.memoryMB} MB` : '-'} /> });
  data.push({ component: <StatusBadge status={item.status} /> });
  data.push({ component: <Datetime value={item?.resourse_created_on} /> });
  data.push({ component: <></> });
  return data;
};

const mapSpaceRow = (item: any, enrichment?: AppEnrichment): ICustomTable2Row[] => {
  const data: ICustomTable2Row[] = [];
  const orgTag = item.tags?.org;
  const org = Array.isArray(orgTag) ? orgTag[0] : orgTag || item.region || '-';
  const spaceName = item.name || '-';
  const spaceStats = enrichment?.spaceApps?.[spaceName] || { count: 0, memoryMB: 0 };
  data.push({ component: <CustomText text1={spaceName} />, drilldownQuery: item });
  data.push({ component: <CustomText text1={org} /> });
  data.push({ component: <CustomText text1={String(spaceStats.count)} /> });
  data.push({ component: <CustomText text1={spaceStats.memoryMB > 0 ? `${spaceStats.memoryMB} MB` : '-'} /> });
  data.push({ component: <StatusBadge status={item.status} /> });
  data.push({ component: <Datetime value={item?.resourse_created_on} /> });
  data.push({ component: <></> });
  return data;
};

const mapRouteRow = (item: any, enrichment?: AppEnrichment): ICustomTable2Row[] => {
  const data: ICustomTable2Row[] = [];
  const url = item.meta?.url || item.name || '-';
  const host = item.meta?.host;
  const spaceTag = item.tags?.space;
  const space = Array.isArray(spaceTag) ? spaceTag[0] : spaceTag || '-';
  const protocol = item.meta?.protocol || '-';
  const destApps: string[] = item.meta?.destination_apps || [];
  data.push({ component: <CustomText text1={url} subtext1={host && host !== url ? host : undefined} />, drilldownQuery: item });
  data.push({ component: <CustomText text1={space} /> });
  data.push({ component: <CustomText text1={protocol} /> });
  data.push({
    component:
      destApps.length > 0 ? (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: '4px' }}>
          {destApps.map((guid: string) => {
            const appName = enrichment?.appNames?.[guid] || guid.substring(0, 8) + '...';
            return <Chip key={guid} label={appName} size='small' variant='outlined' sx={{ fontSize: '10px', height: '20px' }} />;
          })}
        </Box>
      ) : (
        <Typography fontSize='11px' color='#D1D5DB' fontStyle='italic'>
          none
        </Typography>
      ),
  });
  data.push({ component: <StatusBadge status={item.status} /> });
  data.push({ component: <Datetime value={item?.resourse_created_on} /> });
  data.push({ component: <></> });
  return data;
};

const ROW_MAPPERS: Record<string, (item: any, enrichment?: AppEnrichment) => ICustomTable2Row[]> = {
  organizations: mapOrganizationRow,
  spaces: mapSpaceRow,
  routes: mapRouteRow,
};

// --- Expandable Details per resource type ---
const OrgDetails = ({ drilldownQuery }: any) => (
  <Box
    sx={{
      display: 'grid',
      gridTemplateColumns: '1fr 1fr 1fr',
      columnGap: '15px',
      rowGap: '20px',
      mb: '25px',
      backgroundColor: '#fff',
      padding: '20px',
      borderRadius: '8px',
    }}
  >
    {drilldownQuery.name && <DataBlock title={'Name'} data={drilldownQuery.name} />}
    {drilldownQuery.resourse_id && <DataBlock title={'GUID'} data={drilldownQuery.resourse_id} />}
    {drilldownQuery.status && <DataBlock title={'Status'} data={drilldownQuery.status} />}
    {drilldownQuery.meta?.suspended !== undefined && <DataBlock title={'Suspended'} data={String(drilldownQuery.meta.suspended)} />}
    {drilldownQuery.resourse_created_on && <DataBlock title={'Created At'} data={new Date(drilldownQuery.resourse_created_on).toLocaleString()} />}
    {drilldownQuery.tags && Object.keys(drilldownQuery.tags).length > 0 && (
      <Box sx={{ gridColumn: 'span 3' }}>
        <Typography fontSize='12px' fontWeight={600} color='#737373' mb='4px'>
          Tags
        </Typography>
        <TagsCell tags={drilldownQuery.tags} />
      </Box>
    )}
  </Box>
);

const SpaceDetails = ({ drilldownQuery }: any) => {
  const orgTag = drilldownQuery.tags?.org;
  const org = Array.isArray(orgTag) ? orgTag[0] : orgTag || '-';
  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: '1fr 1fr 1fr',
        columnGap: '15px',
        rowGap: '20px',
        mb: '25px',
        backgroundColor: '#fff',
        padding: '20px',
        borderRadius: '8px',
      }}
    >
      {drilldownQuery.name && <DataBlock title={'Name'} data={drilldownQuery.name} />}
      {drilldownQuery.resourse_id && <DataBlock title={'GUID'} data={drilldownQuery.resourse_id} />}
      <DataBlock title={'Organization'} data={org} />
      {drilldownQuery.meta?.org_name && <DataBlock title={'Org Name'} data={drilldownQuery.meta.org_name} />}
      {drilldownQuery.status && <DataBlock title={'Status'} data={drilldownQuery.status} />}
      {drilldownQuery.resourse_created_on && <DataBlock title={'Created At'} data={new Date(drilldownQuery.resourse_created_on).toLocaleString()} />}
      {drilldownQuery.tags && Object.keys(drilldownQuery.tags).length > 0 && (
        <Box sx={{ gridColumn: 'span 3' }}>
          <Typography fontSize='12px' fontWeight={600} color='#737373' mb='4px'>
            Tags
          </Typography>
          <TagsCell tags={drilldownQuery.tags} />
        </Box>
      )}
    </Box>
  );
};

const RouteDetails = ({ drilldownQuery }: any) => {
  const spaceTag = drilldownQuery.tags?.space;
  const space = Array.isArray(spaceTag) ? spaceTag[0] : spaceTag || '-';
  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: '1fr 1fr 1fr',
        columnGap: '15px',
        rowGap: '20px',
        mb: '25px',
        backgroundColor: '#fff',
        padding: '20px',
        borderRadius: '8px',
      }}
    >
      {drilldownQuery.meta?.url && <DataBlock title={'URL'} data={drilldownQuery.meta.url} />}
      {drilldownQuery.meta?.host && <DataBlock title={'Host'} data={drilldownQuery.meta.host} />}
      {drilldownQuery.meta?.path && <DataBlock title={'Path'} data={drilldownQuery.meta.path} />}
      {drilldownQuery.meta?.protocol && <DataBlock title={'Protocol'} data={drilldownQuery.meta.protocol} />}
      <DataBlock title={'Space'} data={space} />
      {drilldownQuery.resourse_id && <DataBlock title={'GUID'} data={drilldownQuery.resourse_id} />}
      {drilldownQuery.status && <DataBlock title={'Status'} data={drilldownQuery.status} />}
      {drilldownQuery.meta?.destination_apps && drilldownQuery.meta.destination_apps.length > 0 && (
        <DataBlock title={'Destination Apps'} data={drilldownQuery.meta.destination_apps.join(', ')} />
      )}
      {drilldownQuery.resourse_created_on && <DataBlock title={'Created At'} data={new Date(drilldownQuery.resourse_created_on).toLocaleString()} />}
      {drilldownQuery.tags && Object.keys(drilldownQuery.tags).length > 0 && (
        <Box sx={{ gridColumn: 'span 3' }}>
          <Typography fontSize='12px' fontWeight={600} color='#737373' mb='4px'>
            Tags
          </Typography>
          <TagsCell tags={drilldownQuery.tags} />
        </Box>
      )}
    </Box>
  );
};

const DETAIL_COMPONENTS: Record<string, React.FC<{ drilldownQuery: any }>> = {
  organizations: OrgDetails,
  spaces: SpaceDetails,
  routes: RouteDetails,
};

const SEARCH_LABELS: Record<string, string> = {
  organizations: 'Search By Org Name',
  spaces: 'Search By Space Name',
  routes: 'Search By Route URL',
};

const createDetailComponentFn = (DetailComp: React.FC<{ drilldownQuery: any }>) => (_opt: any, drilldownQuery: any) =>
  <DetailComp drilldownQuery={drilldownQuery} />;

const createResourceEventsComponentFn = (accountId: string, serviceName: string) => (_opt: any, drilldownQuery: any) =>
  <CloudAccountEvents accountId={accountId} serviceName={serviceName} subjectName={drilldownQuery?.name || drilldownQuery?.resourse_id} />;

// --- Main Component ---
const CFResources = (props: any) => {
  const resourceType = props?.serviceName || 'organizations';
  const tableId = `CF_${resourceType.toUpperCase()}_TABLE`;
  const headers = HEADERS[resourceType] || HEADERS.organizations;
  const mapRow = ROW_MAPPERS[resourceType] || mapOrganizationRow;
  const DetailComponent = DETAIL_COMPONENTS[resourceType] || OrgDetails;
  const searchLabel = SEARCH_LABELS[resourceType] || 'Search By Name';

  const [loading, setLoading] = useState(false);
  const [resources, setResources] = useState<any>([]);
  const [resourcesCount, setResourcesCount] = useState(0);
  const [selectedInstanceIdName, setSelectedInstanceIdName] = useState('');
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [appEnrichment, setAppEnrichment] = useState<AppEnrichment>({ orgApps: {}, spaceApps: {}, appNames: {} });
  const { page, rowsPerPage, changePage, setPage } = usePagination();

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, props?.serviceName, []).then(setAvailableTagKeys);
    }
  }, [props?.accountId, props?.serviceName]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, props?.serviceName, []).then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, selectedTagKey]);

  // Fetch all app data for enrichment (org/space app counts, memory, app name lookup for routes)
  useEffect(() => {
    if (!props?.accountId) return;
    const PAGE_SIZE = 500;
    const fetchParams = { account_id: props.accountId, serviceName: 'apps', type: [] };

    const fetchAllApps = async () => {
      const firstPage = await apiCloudAccount.getCloudResource(fetchParams, PAGE_SIZE, 0);
      const totalCount = firstPage.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
      let allApps = firstPage.data?.data?.cloud_resourses || [];

      if (totalCount > PAGE_SIZE) {
        const remaining = [];
        for (let offset = PAGE_SIZE; offset < totalCount; offset += PAGE_SIZE) {
          remaining.push(apiCloudAccount.getCloudResource(fetchParams, PAGE_SIZE, offset));
        }
        const pages = await Promise.all(remaining);
        for (const page of pages) {
          allApps = allApps.concat(page.data?.data?.cloud_resourses || []);
        }
      }

      return allApps.map(parseResource);
    };

    fetchAllApps()
      .then((apps) => {
        const orgApps: Record<string, { count: number; instances: number; memoryMB: number }> = {};
        const spaceApps: Record<string, { count: number; memoryMB: number }> = {};
        const appNames: Record<string, string> = {};
        apps.forEach((app: any) => {
          // Build GUID->name map for route destination resolution
          if (app.resourse_id && app.name) {
            appNames[app.resourse_id] = app.name;
          }
          const orgTag = app.tags?.org;
          const orgName = Array.isArray(orgTag) ? orgTag[0] : orgTag || '';
          const spaceTag = app.tags?.space;
          const spaceName = Array.isArray(spaceTag) ? spaceTag[0] : spaceTag || '';
          const mem = app.meta?.memory_in_mb || 0;
          const inst = app.meta?.instances || 0;
          if (orgName) {
            if (!orgApps[orgName]) orgApps[orgName] = { count: 0, instances: 0, memoryMB: 0 };
            orgApps[orgName].count += 1;
            orgApps[orgName].instances += inst;
            orgApps[orgName].memoryMB += mem * inst;
          }
          if (spaceName) {
            if (!spaceApps[spaceName]) spaceApps[spaceName] = { count: 0, memoryMB: 0 };
            spaceApps[spaceName].count += 1;
            spaceApps[spaceName].memoryMB += mem * inst;
          }
        });
        setAppEnrichment({ orgApps, spaceApps, appNames });
      })
      .catch((error: any) => {
        console.error('Failed to fetch app enrichment data:', error);
      });
  }, [props?.accountId, resourceType]);

  useEffect(() => {
    fetchResources();
  }, [props?.accountId, page, rowsPerPage, selectedTagKey, selectedTagValue, appEnrichment]);

  const fetchResources = () => {
    if (!props?.accountId) return;
    setLoading(true);
    apiCloudAccount
      .getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: props?.serviceName,
          type: [],
          nameFilter: selectedInstanceIdName,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        const count = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
        const data = res.data?.data?.cloud_resourses?.map((rawItem: any) => {
          const item = parseResource(rawItem);
          return mapRow(item, appEnrichment);
        });
        setResources(data || []);
        setResourcesCount(count);
      })
      .catch((error: any) => {
        console.error(`Failed to fetch CloudFoundry ${resourceType}:`, error);
        setLoading(false);
      });
  };

  const onSearchFilterChange = (e: any) => setSelectedInstanceIdName(e?.target?.value);

  const onEnterPress = () => {
    if (page !== 0) {
      setPage(0);
    } else {
      fetchResources();
    }
  };

  return (
    <BoxLayout2
      filterOptions={[
        {
          type: 'input',
          value: selectedInstanceIdName,
          enabled: true,
          onSelect: onSearchFilterChange,
          minWidth: '150px',
          label: searchLabel,
          onEnter: onEnterPress,
        },
        {
          type: 'dropdown',
          label: 'Tag Key',
          value: selectedTagKey,
          options: availableTagKeys,
          onSelect: (e: any) => {
            setPage(0);
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) setSelectedTagValue(null);
          },
        },
        {
          type: 'dropdown',
          enabled: !!selectedTagKey,
          label: 'Tag Value',
          value: selectedTagValue,
          options: availableTagValues,
          onSelect: (e: any) => {
            setPage(0);
            setSelectedTagValue(e.target.value || null);
          },
        },
      ]}
      sharingOptions={{
        download: { enabled: true, onClick: () => ({ tableId }) },
        sharing: { enabled: false, onClick: null },
      }}
    >
      <CloudAccountTable
        id={tableId}
        headers={headers}
        data={resources}
        rowsPerPage={rowsPerPage}
        onPageChange={changePage}
        totalRows={resourcesCount}
        loading={loading}
        showExpandable={true}
        pageNumber={page + 1}
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              key: `cf-${resourceType}-details`,
              componentFn: createDetailComponentFn(DetailComponent),
            },
            {
              text: 'Events',
              value: 1,
              key: `cf-${resourceType}-events`,
              componentFn: createResourceEventsComponentFn(props?.accountId, props?.serviceName),
            },
          ],
        }}
      />
    </BoxLayout2>
  );
};

export default CFResources;
