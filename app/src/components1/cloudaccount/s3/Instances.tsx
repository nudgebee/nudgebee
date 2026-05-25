import { Box, Typography } from '@mui/material';
import React, { useEffect, useState, type JSX } from 'react';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import apiCloudAccount from '@api1/cloud-account';
import CloudAccountTable from '@components1/cloudaccount/CloudAccountTable';
import Currency from '@common-new/format/Currency';
import { hasWriteAccess } from '@lib/auth';
import CustomLabels from '@common-new/widgets/CustomLabels';
import OptimizeSummary from '@components1/cloudaccount/ec2/Summary';
import CloudAccountEvents from '@components1/cloudaccount/CloudAccountEvents';
import Datetime from '@common-new/format/Datetime';
import TagsCell from '@components1/cloudaccount/TagsCell';
import { buildStateApiParams, getStateDropdownOptions } from '@components1/cloudaccount/stateFilter';
import { usePagination } from '@hooks/usePagination';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import DownloadButton from '@common-new/DownloadButton';

const INSTANCE_HEADER = [
  { name: 'Bucket Name', width: '28%' },
  { name: 'State', width: '10%' },
  { name: 'Tags', width: '38%' },
  { name: 'Created At', width: '10%' },
  { name: 'Cost', width: '8%' },
  { name: '', width: '6%' },
];

export const CustomText = (data: { text1: string | null; text2?: string | null; subtext1?: string | null; subtext2?: string | null }) => {
  return (
    <>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'row',
        }}
      >
        {data.text1 && <Typography sx={{ color: '#374151', fontWeight: 400, fontSize: 13, marginRight: '2px' }}>{data.text1}</Typography>}
        {data.text2 && <Typography sx={{ color: '#9F9F9F', fontSize: 13 }}>{data.text2}</Typography>}
      </Box>
      {(data.subtext1 || data.subtext2) && (
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'row',
          }}
        >
          <Typography sx={{ color: '#9F9F9F', fontSize: 13, marginRight: '4px' }}>{data.subtext1}</Typography>
          {data.subtext2 && (
            <>
              <span
                style={{
                  width: '1px',
                  height: '13px',
                  marginTop: '3px',
                  backgroundColor: '#737373',
                }}
              />
              <Typography sx={{ color: '#9F9F9F', fontSize: 13, marginLeft: '4px' }}>{data.subtext2}</Typography>
            </>
          )}
        </Box>
      )}
    </>
  );
};

export interface ICustomTable2Row {
  component?: JSX.Element;
  drilldownQuery?: {
    podName?: any;
    workloadName?: any;
    namespaceName?: any;
    cpuRecc?: string;
    cpuReq?: string;
    memoryReq?: string;
    memoryRecc?: string;
    resourceId?: any;
    memLimit?: string | undefined;
    cpuLimit?: any;
    recommendation?: any;
    event?: any;
  };
  text?: any;
  data?: any;
}

const InstancesView = (props: {
  accountId: string | undefined;
  heading: string | undefined;
  serviceName: string;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
}) => {
  const [s3Instances, setS3Instances] = useState([]);
  const [s3InstancesCount, setS3InstancesCount] = useState(0);
  const [loading, setLoading] = useState(false);
  // Two-state pattern matches ManualInvestigated.jsx: `selectedInstanceName`
  // is what the user is typing; `appliedInstanceName` is what the API actually
  // filters by. The fetch only fires when the user presses Enter or clears
  // the input — avoids API spam while typing.
  const [selectedInstanceName, setSelectedInstanceName] = useState('');
  const [appliedInstanceName, setAppliedInstanceName] = useState('');
  const [selectedTagKey, setSelectedTagKey] = useState<string | null>(null);
  const [selectedTagValue, setSelectedTagValue] = useState<string | null>(null);
  const [availableTagKeys, setAvailableTagKeys] = useState<{ label: string; value: string }[]>([]);
  const [availableTagValues, setAvailableTagValues] = useState<{ label: string; value: string }[]>([]);
  const [selectedState, setSelectedState] = useState<string>('all');
  const [selectedRegion, setSelectedRegion] = useState<string | null>(null);
  const [availableRegions, setAvailableRegions] = useState<{ label: string; value: string }[]>([]);
  const stateOptions = getStateDropdownOptions(props?.serviceName);

  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const s3OptimizeInstancesTable = 's3OptimizeInstancesTable';
  const s3TypeMap: Record<string, string> = { AmazonS3: 'storage', 'Cloud Storage': 'storage.googleapis.com/Bucket' };
  const s3Type = s3TypeMap[props.serviceName] || 'storageaccounts';

  const onSearchEnter = () => {
    setAppliedInstanceName(selectedInstanceName);
    setPage(0);
  };

  const onSearchClear = () => {
    setSelectedInstanceName('');
    setAppliedInstanceName('');
    setPage(0);
  };

  const listS3Instances = () => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .getCloudResource(
        {
          account_id: props?.accountId,
          serviceName: props.serviceName,
          type: s3Type,
          nameFilter: appliedInstanceName,
          tagFilterKey: selectedTagKey || undefined,
          tagFilterValue: selectedTagValue || undefined,
          region: selectedRegion || undefined,
          ...buildStateApiParams(props?.serviceName, selectedState),
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        const cloudResourceCount = res.data?.data?.cloud_resourses_aggregate?.aggregate?.count || 0;
        const cloudResourceData = res.data?.data?.cloud_resourses?.map((item: any) => {
          const data: ICustomTable2Row[] = [];
          const writeAccess = hasWriteAccess(props?.accountId);

          data.push({
            component: <CustomText text1={item.resourse_id} subtext1={item.region} />,
            drilldownQuery: item,
          });
          data.push({
            component: <CustomLabels text={item.status} />,
          });
          data.push({ component: <TagsCell tags={item.tags} /> });
          data.push({
            component: <Datetime value={item.meta?.CreationDate || item.meta?.timeCreated || item.created_at} />,
          });
          data.push({ component: <Currency value={item?.spends_aggregate?.aggregate?.sum?.amount} precison={1} /> });
          data.push({
            component: writeAccess ? (
              <Box display={'flex'} justifyContent={'flex-end'} gap={'4px'}>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `s3-delete-${item.resourse_id}`,
                      label: 'Delete',
                      tone: 'danger',
                      disabled: true,
                      onSelect: () => undefined,
                    },
                  ]}
                  trigger={
                    <DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon fontSize='small' />} />
                  }
                />
              </Box>
            ) : undefined,
          });

          return data;
        });
        setS3Instances(cloudResourceData);

        setS3InstancesCount(cloudResourceCount);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    if (props?.accountId) {
      apiCloudAccount.getDistinctTagKeys(props.accountId, props.serviceName, s3Type).then(setAvailableTagKeys);
      apiCloudAccount.getDistinctRegions(props.accountId, props.serviceName).then(setAvailableRegions);
    }
  }, [props?.accountId, props.serviceName]);

  useEffect(() => {
    if (props?.accountId && selectedTagKey) {
      apiCloudAccount.getDistinctTagValues(props.accountId, selectedTagKey, props.serviceName, s3Type).then(setAvailableTagValues);
    } else {
      setAvailableTagValues([]);
    }
  }, [props?.accountId, selectedTagKey]);

  useEffect(() => {
    listS3Instances();
  }, [props?.accountId, props?.serviceName, page, rowsPerPage, selectedTagKey, selectedTagValue, selectedState, selectedRegion, appliedInstanceName]);

  return (
    <ListingLayout id='right-sizing'>
      <ListingLayout.Toolbar
        title={props.heading || undefined}
        actions={<DownloadButton id={`${s3OptimizeInstancesTable}-download`} onClick={() => ({ tableId: s3OptimizeInstancesTable })} />}
      >
        <CustomSearch
          id='s3-instances-search'
          label='Search By Bucket Name'
          value={selectedInstanceName}
          onChange={(next) => {
            // Auto-clear the applied filter (and reset page) when the user
            // backspaces the input to empty — restores legacy "clear to
            // re-fetch unfiltered" behaviour.
            if (selectedInstanceName !== '' && next === '') {
              setAppliedInstanceName('');
              setPage(0);
            }
            setSelectedInstanceName(next);
          }}
          onEnterPress={onSearchEnter}
          onClear={onSearchClear}
        />
        <FilterDropdown
          id='s3-filter-state'
          label='State'
          value={selectedState}
          options={stateOptions}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedState(e.target.value || 'all');
          }}
        />
        <FilterDropdown
          id='s3-filter-region'
          label='Region'
          value={selectedRegion}
          options={availableRegions}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedRegion(e.target.value || null);
          }}
        />
        <FilterDropdown
          id='s3-filter-tag-key'
          label='Tag Key'
          value={selectedTagKey}
          options={availableTagKeys}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) {
              setSelectedTagValue(null);
            }
          }}
        />
        <FilterDropdown
          id='s3-filter-tag-value'
          label='Tag Value'
          value={selectedTagValue}
          options={availableTagValues}
          disabled={!selectedTagKey}
          onSelect={(e: any) => {
            setPage(0);
            setSelectedTagValue(e.target.value || null);
          }}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CloudAccountTable
          id={s3OptimizeInstancesTable}
          headers={INSTANCE_HEADER}
          data={s3Instances}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={s3InstancesCount}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
          expandable={{
            tabs: [
              {
                text: 'Monitoring',
                value: 1,
                key: 's3-monitoring',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  return <OptimizeSummary accountId={props?.accountId} resourceId={drilldownQuery.resourse_id} serviceName={props.serviceName} />;
                },
              },
              {
                text: 'Events',
                value: 2,
                key: 's3-events',
                componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                  return <CloudAccountEvents accountId={props?.accountId} serviceName={props.serviceName} subjectName={drilldownQuery.resourse_id} />;
                },
              },
            ],
          }}
          stickyColumnIndex={props.stickyColumnIndex}
          tableHeadingCenter={props.tableHeadingCenter}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};
export default InstancesView;
