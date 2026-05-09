import { Box, Typography } from '@mui/material';
import React, { useEffect, useState, type JSX } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import BoxLayout2 from '@common/BoxLayout2';
import CloudAccountTable from '@components1/cloudaccount/CloudAccountTable';
import Currency from '@components1/common/format/Currency';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { hasWriteAccess } from '@lib/auth';
import { action } from 'src/utils/actionStyles';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { DeleteIconRed as NewDelete } from '@assets';
import OptimizeSummary from '@components1/cloudaccount/ec2/Summary';
import CloudAccountEvents from '@components1/cloudaccount/CloudAccountEvents';
import Datetime from '@components1/common/format/Datetime';
import TagsCell from '@components1/cloudaccount/TagsCell';
import { buildStateApiParams, getStateDropdownOptions } from '@components1/cloudaccount/stateFilter';
import { usePagination } from '@hooks/usePagination';

const INSTANCE_HEADER = ['Bucket Name', 'State', 'Tags', 'Created At', 'Cost', ''];

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
  const [selectedInstanceName, setSelectedInstanceName] = useState('');
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

  const onInstanceSearchFilterChange = (e: any) => {
    setSelectedInstanceName(e.target.value);
  };

  const onEnterPress = () => {
    listS3Instances();
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
          nameFilter: selectedInstanceName,
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
          let MENU_ITEMS;
          if (hasWriteAccess(props?.accountId)) {
            MENU_ITEMS = [
              {
                icon: NewDelete,
                label: 'Delete',
                id: 0,
                disabled: true,
              },
            ];
          }

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
            component: (
              <Box display={'flex'} justifyContent={'flex-end'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={undefined} />
              </Box>
            ),
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
  }, [props?.accountId, props?.serviceName, page, rowsPerPage, selectedTagKey, selectedTagValue, selectedState, selectedRegion]);

  return (
    <BoxLayout2
      heading={props.heading ?? ''}
      id='right-sizing'
      filterOptions={[
        {
          type: 'search',
          enabled: true,
          onSelect: onInstanceSearchFilterChange,
          minWidth: '150px',
          label: 'Search By Bucket Name',
          onEnter: onEnterPress,
        },
        {
          type: 'dropdown',
          label: 'State',
          value: selectedState,
          options: stateOptions,
          onSelect: (e: any) => {
            setPage(0);
            setSelectedState(e.target.value || 'all');
          },
        },
        {
          type: 'dropdown',
          label: 'Region',
          value: selectedRegion,
          options: availableRegions,
          onSelect: (e: any) => {
            setPage(0);
            setSelectedRegion(e.target.value || null);
          },
        },
        {
          type: 'dropdown',
          label: 'Tag Key',
          value: selectedTagKey,
          options: availableTagKeys,
          onSelect: (e: any) => {
            setPage(0);
            setSelectedTagKey(e.target.value || null);
            if (!e.target.value) {
              setSelectedTagValue(null);
            }
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
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: s3OptimizeInstancesTable,
            };
          },
        },
        sharing: { enabled: false, onClick: null },
      }}
    >
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
              key: 'rds-monitoring',
              componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                return <OptimizeSummary accountId={props?.accountId} resourceId={drilldownQuery.resourse_id} serviceName={props.serviceName} />;
              },
            },
            {
              text: 'Events',
              value: 2,
              key: 'rds-events',
              componentFn: function (opt: any, drilldownQuery: any, _row: any) {
                return <CloudAccountEvents accountId={props?.accountId} serviceName={props.serviceName} subjectName={drilldownQuery.resourse_id} />;
              },
            },
          ],
        }}
        stickyColumnIndex={props.stickyColumnIndex}
        tableHeadingCenter={props.tableHeadingCenter}
      />
    </BoxLayout2>
  );
};
export default InstancesView;
