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
import CustomTable from '@common-new/tables/CustomTable2';
import { getAllowedNamespaces } from '@lib/auth';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import Text from '@common-new/format/Text';
import { ds } from '@utils/colors';

const NAMESPACE_HEADERS = ['Name', 'Namespaces', 'Type', 'Cluster-Ip', 'External-Ip', 'Ports', 'Age'];

function parseK8sDate(date) {
  return new Date(date?.replace(' ', 'T'));
}

function getExternalIP(svc) {
  const type = svc?.spec?.type;

  if (type === 'LoadBalancer') {
    const ingress = svc?.status?.load_balancer?.ingress;
    if (ingress?.length) {
      const values = ingress.map((i) => i.ip || i.hostname).filter(Boolean);
      if (values.length) return values.join(',');
    }
    return svc?.spec?.external_i_ps?.join(',') || '<pending>';
  }

  if (type === 'ExternalName') {
    return svc?.spec?.external_name || '';
  }

  return svc?.spec?.external_i_ps?.join(',') || '';
}

const KubernetesServiceTable = ({ accountId }) => {
  const router = useRouter();

  const [data, setData] = useState([]);
  const [filteredData, setFilteredData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router.query.namespace || '');

  const kubernetesServiceTable = 'kubernetesServiceTable';

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
            resource_type: 'services',
            all_namespaces: true,
          },
        },
      })
      .then((res) => {
        let data = res?.data?.findings?.[0]?.evidence?.[0]?.data;
        if (data) {
          try {
            let parsedData = JSON.parse(data);
            data = parsedData[0].data;
          } catch (e) {
            console.error('Error parsing data', e);
          }
        }
        if (typeof data === 'string') {
          data = JSON.parse(data);
        }
        let allowedNamespace = getAllowedNamespaces(accountId);
        if (allowedNamespace != null && allowedNamespace.length > 0) {
          data = data.filter((item) => allowedNamespace.includes(item.metadata.namespace));
        }
        let namespaces = data?.map((item) => item.metadata.namespace);
        setNamespaceFilter([...new Set(namespaces)]);

        let tableData = data?.map((item) => {
          return [
            {
              component: <Text value={item.metadata.name} showAutoEllipsis />,
              drilldownQuery: {
                data: item,
              },
            },
            {
              component: <Text value={item.metadata.namespace} showAutoEllipsis />,
            },
            {
              component: <Text value={item.spec.type} />,
            },
            {
              component: <Text value={item.spec.cluster_i_ps?.join(',') || '-'} />,
            },
            {
              component: <Text value={getExternalIP(item) || '-'} />,
            },
            {
              component: (
                <Text
                  showAutoEllipsis
                  sx={{ minWidth: ds.space.mul(0, 50) }}
                  value={
                    item.spec.ports
                      ?.map((p) => {
                        return p.port;
                      })
                      ?.join(',') ?? '-'
                  }
                />
              ),
            },
            {
              component: <Datetime value={parseK8sDate(item.metadata.creation_timestamp)} />,
            },
          ];
        });

        setData(tableData ?? []);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [accountId]);

  // Client-side namespace filtering — no API call needed
  useEffect(() => {
    if (!data.length) return;
    const ns = router.query.namespace;
    if (ns) {
      const newData = data.filter((item) => item[0].drilldownQuery.data.metadata.namespace === ns);
      setFilteredData(newData);
      setTotalCount(newData.length);
    } else {
      setFilteredData([...data]);
      setTotalCount(data.length);
    }
  }, [router.query.namespace, data]);

  const onNamespaceFilterChange = (e) => {
    setSelectedNamespace(e?.target?.value);
    applyFiltersOnRouter(router, { namespace: e?.target?.value });
    if (e?.target?.value) {
      let newData = data.filter((item) => item[0]?.drilldownQuery?.data?.metadata?.namespace === e?.target?.value);
      setFilteredData(newData);
      setTotalCount(newData?.length);
    } else {
      setFilteredData([...data]);
      setTotalCount(data?.length);
    }
  };

  return (
    <ListingLayout id='all-namespaces'>
      <ListingLayout.Toolbar
        actions={
          <>
            <DownloadButton onClick={() => ({ tableId: kubernetesServiceTable })} />
          </>
        }
      >
        <FilterDropdown
          label='Namespace'
          options={namespaceFilter.map((o) => ({ value: o, label: o }))}
          value={selectedNamespace}
          onSelect={onNamespaceFilterChange}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <KubernetesTable2
          id={kubernetesServiceTable}
          headers={NAMESPACE_HEADERS}
          data={filteredData}
          expandable={{
            tabs: [
              {
                text: 'Details',
                value: 0,
                key: 'WorkloadDetails',
                componentFn: servicesDetailsFn,
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

function servicesDetailsFn(accountId, drilldownQuery) {
  const mapLabels = (label) => {
    if (!label) {
      return [];
    }
    const labelArray = [];

    for (let [k, v] of Object.entries(label)) {
      let name = k + '=' + v;
      labelArray.push(
        <Label
          height='auto'
          margin='0px'
          wordBreak={''}
          displayTooltip
          key={k}
          text={name}
          variant={'grey'}
          customLabelStyle={{ wordBreak: 'break-all' }}
        />
      );
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
      </Grid>{' '}
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
            Selectors:
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
          {mapLabels(drilldownQuery?.data?.spec?.selector) ?? []}
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
            Session Affinity:
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
          {drilldownQuery?.data?.spec?.session_affinity ?? '-'}
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
            Internal Traffic Policy:
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
          {drilldownQuery?.data?.spec?.internal_traffic_policy ?? '-'}
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
            Ports
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
          <CustomTable
            rowsPerPage={drilldownQuery?.data?.spec?.ports?.length}
            headers={['Name', 'Node Port', 'Port', 'Target Port', 'Protocol', 'App Protocol']}
            tableData={drilldownQuery?.data?.spec?.ports.map((p) => {
              return [
                {
                  component: <Text value={p.name} />,
                },
                {
                  component: <Text value={p.node_port} />,
                },
                {
                  component: <Text value={p.port} />,
                },
                {
                  component: <Text value={p.target_port} />,
                },
                {
                  component: <Text value={p.protocol} />,
                },
                {
                  component: <Text value={p.app_protocol} />,
                },
              ];
            })}
          />
        </Grid>
      </Grid>
    </Box>
  );
}

KubernetesServiceTable.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesServiceTable;
