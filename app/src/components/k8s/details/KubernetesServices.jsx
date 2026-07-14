import React, { useState, useEffect } from 'react';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import DownloadButton from '@shared/buttons/DownloadButton';
import KubernetesTable2 from '@components/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import { Label } from '@ui/Label';
import { Box, Grid, Typography } from '@mui/material';
import Datetime from '@shared/format/Datetime';
import PropTypes from 'prop-types';
import CustomTable from '@shared/tables/CustomTable2';
import { getAllowedNamespaces } from '@lib/auth';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import Text from '@shared/format/Text';
import { ds } from '@utils/colors';
import { safeJSONParse } from '@utils/common';

const NAMESPACE_HEADERS = ['Name', 'Namespaces', 'Type', 'Cluster-Ip', 'External-Ip', 'Ports', 'Endpoints', 'Age'];

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

// Unwrap the relay `get_resource` envelope (findings → evidence → JSON string) into
// the resource array. Shared by the services and endpoints fetches.
function parseRelayResourceData(res) {
  let data = res?.data?.findings?.[0]?.evidence?.[0]?.data;
  if (data) {
    try {
      const parsedData = JSON.parse(data);
      data = parsedData[0].data;
    } catch (e) {
      console.error('Error parsing data', e);
    }
  }
  if (typeof data === 'string') {
    data = safeJSONParse(data) || [];
  }
  return Array.isArray(data) ? data : [];
}

// Index v1 Endpoints objects by `namespace/name` (shared with the Service name) and
// flatten ready vs not-ready addresses so each Service can report endpoint readiness.
function buildEndpointsMap(endpoints) {
  const map = {};
  for (const ep of endpoints) {
    const key = `${ep?.metadata?.namespace}/${ep?.metadata?.name}`;
    const addresses = [];
    let ready = 0;
    let notReady = 0;
    for (const subset of ep?.subsets ?? []) {
      const ports = (subset?.ports ?? [])
        .map((p) => p.port)
        .filter(Boolean)
        .join(', ');
      for (const a of subset?.addresses ?? []) {
        ready += 1;
        addresses.push({ ip: a.ip, pod: a?.target_ref?.name, node: a.node_name, ports, ready: true });
      }
      for (const a of subset?.not_ready_addresses ?? []) {
        notReady += 1;
        addresses.push({ ip: a.ip, pod: a?.target_ref?.name, node: a.node_name, ports, ready: false });
      }
    }
    map[key] = { ready, notReady, total: ready + notReady, addresses };
  }
  return map;
}

// Endpoint-readiness status for the Endpoints column. Only selector-backed,
// non-ExternalName Services are expected to route, so only those flag red/amber;
// selectorless/ExternalName Services and clusters without endpoint data stay neutral.
function getEndpointStatus(svc, endpointInfo, endpointsAvailable) {
  const ready = endpointInfo?.ready ?? 0;
  const total = endpointInfo?.total ?? 0;
  const hasSelector = svc?.spec?.selector && Object.keys(svc.spec.selector).length > 0;
  const shouldRoute = hasSelector && svc?.spec?.type !== 'ExternalName';

  if (!endpointsAvailable || !shouldRoute) {
    return { text: total ? `${ready}/${total}` : '-', tone: 'neutral' };
  }
  if (ready === 0) {
    return { text: `${ready}/${total}`, tone: 'critical' };
  }
  if (ready < total) {
    return { text: `${ready}/${total}`, tone: 'warning' };
  }
  return { text: `${ready}/${total}`, tone: 'success' };
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

    const servicesRequest = k8sApi.relayForwardRequest({
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
    });

    // One bulk endpoints fetch, joined to Services client-side for per-Service
    // endpoint readiness. Tolerate failure so Services still render without it.
    const endpointsRequest = k8sApi
      .relayForwardRequest({
        no_sinks: true,
        cache: false,
        body: {
          account_id: accountId,
          action_name: 'get_resource',
          action_params: {
            group: '',
            version: 'v1',
            resource_type: 'endpoints',
            all_namespaces: true,
          },
        },
      })
      .catch(() => null);

    Promise.all([servicesRequest, endpointsRequest])
      .then(([servicesRes, endpointsRes]) => {
        let data = parseRelayResourceData(servicesRes);

        const endpointsList = endpointsRes ? parseRelayResourceData(endpointsRes) : [];
        const endpointsAvailable = endpointsList.length > 0;
        const endpointsMap = buildEndpointsMap(endpointsList);

        let allowedNamespace = getAllowedNamespaces(accountId);
        if (allowedNamespace != null && allowedNamespace.length > 0) {
          data = data.filter((item) => allowedNamespace.includes(item.metadata.namespace));
        }
        let namespaces = data?.map((item) => item.metadata.namespace);
        setNamespaceFilter([...new Set(namespaces)]);

        let tableData = data?.map((item) => {
          const endpointInfo = endpointsMap[`${item.metadata.namespace}/${item.metadata.name}`];
          const endpointStatus = getEndpointStatus(item, endpointInfo, endpointsAvailable);
          return [
            {
              component: <Text value={item.metadata.name} showAutoEllipsis />,
              drilldownQuery: {
                data: item,
                endpoints: endpointInfo,
                endpointsAvailable,
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
              component: <Label tone={endpointStatus.tone} dot text={endpointStatus.text} />,
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

  const svc = drilldownQuery?.data;
  const endpointInfo = drilldownQuery?.endpoints;
  const endpointsAvailable = drilldownQuery?.endpointsAvailable;
  const hasSelector = svc?.spec?.selector && Object.keys(svc.spec.selector).length > 0;
  const shouldRoute = hasSelector && svc?.spec?.type !== 'ExternalName';

  const renderEndpoints = () => {
    if (!endpointsAvailable) {
      return <Text value='-' />;
    }
    const addresses = endpointInfo?.addresses ?? [];
    if (!addresses.length) {
      return shouldRoute ? <Label tone='critical' dot text='No ready endpoints' /> : <Text value='-' />;
    }
    return (
      <CustomTable
        rowsPerPage={addresses.length}
        headers={['Endpoint IP', 'Pod', 'Node', 'Port', 'Status']}
        tableData={addresses.map((a) => {
          return [
            {
              component: <Text value={a.ip || '-'} />,
            },
            {
              component: <Text value={a.pod || '-'} showAutoEllipsis />,
            },
            {
              component: <Text value={a.node || '-'} showAutoEllipsis />,
            },
            {
              component: <Text value={a.ports || '-'} />,
            },
            {
              component: <Label tone={a.ready ? 'success' : 'critical'} dot text={a.ready ? 'Ready' : 'Not Ready'} />,
            },
          ];
        })}
      />
    );
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
            Endpoints
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
          {renderEndpoints()}
        </Grid>
      </Grid>
    </Box>
  );
}

KubernetesServiceTable.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesServiceTable;
