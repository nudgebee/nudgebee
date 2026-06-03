import React, { useState, useEffect } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import { Label } from '@components1/ds/Label';
import { Box, Grid, Typography } from '@mui/material';
import Datetime from '@common-new/format/Datetime';
import PropTypes from 'prop-types';
import Text from '@common-new/format/Text';
import { ds } from '@utils/colors';

const NAMESPACE_HEADERS = ['Name', 'Capacity', 'AccessMode', 'Reclaim Policy', 'Status', 'Claim', 'Storage Class', 'Age'];

function parseK8sDate(date) {
  return new Date(date?.replace(' ', 'T'));
}

const KubernetesPVTable = ({ accountId }) => {
  const [data, setData] = useState([]);
  const [allData, setAllData] = useState([]);
  const [allRawItems, setAllRawItems] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [namespaces, setNamespaces] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState('');

  const kubernetesPVTable = 'kubernetesPVTable';

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoading(true);

    k8sApi
      .relayForwardRequest({
        no_sinks: true,
        cache: false,
        body: {
          account_id: accountId,
          action_name: 'get_resource',
          action_params: {
            group: '',
            version: 'v1',
            resource_type: 'persistentvolumes',
            all_namespaces: true,
          },
        },
      })
      .then((res) => {
        let pvData = res?.data?.findings?.[0]?.evidence?.[0]?.data;
        if (pvData) {
          try {
            let parsedData = JSON.parse(pvData);
            pvData = parsedData[0].data;
          } catch (e) {
            console.error('Error parsing data', e);
          }
        }
        if (typeof pvData === 'string') {
          pvData = JSON.parse(pvData);
        }
        let tableData = pvData?.map((item) => {
          return [
            {
              component: <Text value={item.metadata.name} />,
              drilldownQuery: {
                data: item,
              },
            },
            {
              component: <Text value={item.spec.capacity.storage} />,
            },
            {
              component: <Text value={item.spec.access_modes.join(',')} />,
            },
            {
              component: <Text value={item.spec.persistent_volume_reclaim_policy} />,
            },
            {
              component: <Text value={item.status.phase} />,
            },
            {
              component: <Text value={item.spec.claim_ref?.namespace + '/' + item.spec.claim_ref?.name} />,
            },
            {
              component: <Text value={item.spec.storage_class_name} />,
            },
            {
              component: <Datetime value={parseK8sDate(item.metadata.creation_timestamp)} />,
            },
          ];
        });
        const uniqueNamespaces = [...new Set(pvData?.map((item) => item?.spec?.claim_ref?.namespace).filter(Boolean))];
        setNamespaces(uniqueNamespaces);
        setAllRawItems(pvData ?? []);
        setAllData(tableData ?? []);
        setData(tableData ?? []);
        setTotalCount(tableData?.length);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [accountId]);

  const onNamespaceFilterChange = (e) => {
    const ns = e?.target?.value;
    setSelectedNamespace(ns);
    if (ns) {
      const filtered = allData.filter((_, i) => allRawItems[i]?.spec?.claim_ref?.namespace === ns);
      setData(filtered);
      setTotalCount(filtered.length);
    } else {
      setData(allData);
      setTotalCount(allData.length);
    }
  };

  return (
    <ListingLayout id='all-namespaces'>
      <ListingLayout.Toolbar
        actions={
          <>
            <DownloadButton onClick={() => ({ tableId: kubernetesPVTable })} />
          </>
        }
      >
        <FilterDropdown
          label='Namespace'
          options={namespaces.map((o) => ({ value: o, label: o }))}
          value={selectedNamespace}
          onSelect={onNamespaceFilterChange}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <KubernetesTable2
          id={kubernetesPVTable}
          headers={NAMESPACE_HEADERS}
          data={data}
          resetPage={`namespace-${selectedNamespace}`}
          expandable={{
            tabs: [
              {
                text: 'Details',
                value: 0,
                key: 'WorkloadDetails',
                componentFn: pvDetailsFn,
              },
            ],
          }}
          rowsPerPage={totalCount}
          totalRows={totalCount}
          showExpandable
          loading={loading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

function pvDetailsFn(accountId, drilldownQuery) {
  const mapLabels = (label) => {
    if (!label) {
      return [];
    }
    const labelArray = [];

    for (let [k, v] of Object.entries(label)) {
      let name = k + '=' + v;
      labelArray.push(<Label height='auto' margin='0px' wordBreak={''} displayTooltip key={k} text={name} variant={'grey'} />);
    }
    return labelArray;
  };

  return (
    <Box
      sx={{
        backgroundColor: ds.background[100],
        padding: 'var(--ds-space-4) var(--ds-space-4)',
        borderRadius: 'var(--ds-radius-lg)',
        border: `1px solid ${ds.blue[300]}`,
      }}
    >
      <Grid container sx={{ marginBottom: 'var(--ds-space-2)' }}>
        <Grid item md={3}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Labels:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            gap: 'var(--ds-space-3)',
            fontFamily: 'Roboto',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: '20px',
            color: 'var(--ds-blue-500)',
            maxWidth: ds.space.mul(0, 180),
          }}
        >
          {mapLabels(drilldownQuery?.data?.metadata?.labels) ?? []}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: 'var(--ds-space-2)' }}>
        <Grid item md={3}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Annotations:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: 'var(--ds-space-3)',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: '20px',
            color: 'var(--ds-blue-500)',
            maxWidth: ds.space.mul(0, 180),
          }}
        >
          {mapLabels(drilldownQuery?.data?.metadata?.annotations) ?? []}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: 'var(--ds-space-2)' }}>
        <Grid item md={3}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Finalizers:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: 'var(--ds-space-3)',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: '20px',
            color: 'var(--ds-blue-500)',
            maxWidth: ds.space.mul(0, 180),
          }}
        >
          {drilldownQuery?.data?.metadata?.finalizers?.join(',')}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: 'var(--ds-space-2)' }}>
        <Grid item md={3}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Volume Mode:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: 'var(--ds-space-3)',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: '20px',
            color: 'var(--ds-blue-500)',
            maxWidth: ds.space.mul(0, 180),
          }}
        >
          {drilldownQuery?.data?.spec?.volume_mode}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: 'var(--ds-space-2)' }}>
        <Grid item md={3}>
          <Typography
            width={ds.space.mul(0, 75)}
            sx={{
              fontFamily: 'Roboto',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-medium)',
              lineHeight: '20px',
              color: 'var(--ds-brand-500)',
            }}
          >
            Node Affinity:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: 'var(--ds-space-3)',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: '20px',
            color: 'var(--ds-blue-500)',
            maxWidth: ds.space.mul(0, 180),
          }}
        >
          <pre>{JSON.stringify(drilldownQuery?.data?.spec?.node_affinity, null, 2)}</pre>
        </Grid>
        {Object.entries(drilldownQuery?.data?.spec)
          .filter(([key, value]) => {
            return (
              key !== 'node_affinity' &&
              key !== 'volume_mode' &&
              key != 'access_modes' &&
              key != 'claim_ref' &&
              key != 'persistent_volume_reclaim_policy' &&
              key != 'storage_class_name' &&
              key != 'capacity' &&
              value != null
            );
          })
          .map(([key, value]) => {
            return (
              <Grid key={key} container sx={{ marginBottom: 'var(--ds-space-2)' }}>
                <Grid item md={3}>
                  <Typography
                    width={ds.space.mul(0, 75)}
                    sx={{
                      fontFamily: 'Roboto',
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      lineHeight: '20px',
                      color: 'var(--ds-brand-500)',
                    }}
                  >
                    {key}:
                  </Typography>
                </Grid>
                <Grid
                  item
                  md={9}
                  sx={{
                    display: 'flex',
                    flexDirection: 'row',
                    flexWrap: 'wrap',
                    fontFamily: 'Roboto',
                    gap: 'var(--ds-space-3)',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    lineHeight: '20px',
                    color: 'var(--ds-blue-500)',
                    maxWidth: ds.space.mul(0, 180),
                  }}
                >
                  {JSON.stringify(value, null, 2)}
                </Grid>
              </Grid>
            );
          })}
      </Grid>
    </Box>
  );
}

KubernetesPVTable.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesPVTable;
