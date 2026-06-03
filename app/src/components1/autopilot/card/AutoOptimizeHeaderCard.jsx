import React from 'react';
import { Box, Divider, Typography } from '@mui/material';
import { Select } from '@components1/ds/Select';
import { useData } from '@context/DataContext';
import PropTypes from 'prop-types';
import k8sApi from '@api1/kubernetes';
import Currency from '@components1/common/format/Currency';
import { ds } from '@utils/colors';

const TextWithValue = ({ title, value, valueSize = ds.text.small, valueColor = ds.gray[500], direction = 'row', updatedCard = false, sx = {} }) => {
  return (
    <Box sx={{ ...sx, display: 'flex', flexDirection: direction, alignItems: 'baseline' }}>
      <Typography
        sx={{ fontSize: ds.text.small, fontWeight: ds.weight.regular, color: updatedCard ? ds.gray[400] : ds.gray[500], marginRight: ds.space[2] }}
        className='title'
      >
        {title}:
      </Typography>
      <Typography sx={{ fontSize: valueSize, color: valueColor }} className='value'>
        {value}
      </Typography>
    </Box>
  );
};

TextWithValue.propTypes = {
  title: PropTypes.any,
  value: PropTypes.any,
  valueSize: PropTypes.any,
  valueColor: PropTypes.string,
  direction: PropTypes.string,
  updatedCard: PropTypes.bool,
  sx: PropTypes.object,
};

const AutoPilotHeaderCard = ({
  header = '',
  data = {},
  children,
  updatedCard = true,
  setResourceFilter,
  isMultiSelect = true,
  type = 'workload',
  scalingType = '',
  reviewAutoOptimize = false,
  workloadRequired = true,
}) => {
  const { selectedCluster } = useData();

  const [selectedNamespace, setSelectedNamespace] = React.useState(
    data?.auto_optimize_resource_maps?.map((r) => r?.resource_identifier?.namespace) ?? ''
  );
  const [selectedWorkloads, setSelectedWorkloads] = React.useState(data?.auto_optimize_resource_maps?.map((r) => r?.resource_identifier?.name) ?? []);
  const [selectedPvs, setSelectedPvs] = React.useState(data?.auto_optimize_resource_maps?.map((r) => r?.resource_identifier?.name) || []);
  const [namespaces, setNamespaces] = React.useState([]);
  const [workloads, setWorkloads] = React.useState([]);
  const [pvc, setPvc] = React.useState([]);
  const [pvcData, setPvcData] = React.useState([]);
  const [allWorloadObjects, setAllWorloadObjects] = React.useState([]);
  const [isOptionsLoading, setIsOptionsLoading] = React.useState(false);

  React.useEffect(() => {
    if (data?.containerName) {
      return;
    }

    if (type !== 'workload') {
      return;
    }
    setIsOptionsLoading(true);
    k8sApi
      .getK8sNamespaceNames(selectedCluster?.value)
      .then((_response) => {
        setNamespaces(_response.data.namespaces);
      })
      .finally(() => {
        setIsOptionsLoading(false);
      });
  }, [data?.containerName, selectedCluster?.value]);

  React.useEffect(() => {
    if (type !== 'workload') {
      return;
    }

    setIsOptionsLoading(true);
    if (selectedNamespace) {
      k8sApi
        .getAllK8sWorkload({
          namespace: selectedNamespace,
          accountId: selectedCluster?.value,
          kind: scalingType == 'horizontal' ? ['Deployment', 'Rollout'] : ['Deployment', 'StatefulSet', 'Rollout', 'DaemonSet'],
        })
        .then((_response) => {
          setAllWorloadObjects(_response?.data ?? []);
          setWorkloads(_response?.data?.map((workload) => workload.name) ?? []);
        })
        .finally(() => {
          setIsOptionsLoading(false);
        });
    }
  }, [selectedNamespace]);

  React.useEffect(() => {
    if (data?.auto_optimize_resource_maps?.length > 0) {
      return;
    }

    if (type !== 'pvc') {
      return;
    }

    setIsOptionsLoading(true);
    k8sApi
      .relayForwardRequest({
        no_sinks: true,
        cache: false,
        body: {
          account_id: selectedCluster?.value,
          action_name: 'get_resource',
          action_params: {
            group: '',
            version: 'v1',
            resource_type: 'persistentvolumeclaims',
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
        let namespaces = data?.map((item) => item.metadata.namespace);
        setNamespaces([...new Set(namespaces)]);
        setPvcData(data);
      })
      .finally(() => {
        setIsOptionsLoading(false);
      });
  }, [selectedCluster?.value, type]);

  React.useEffect(() => {
    if (type !== 'pvc') {
      return;
    }

    if (selectedNamespace) {
      setPvc(pvcData?.filter((item) => item.metadata.namespace == selectedNamespace).map((item) => item.metadata.name) ?? []);
    }
  }, [selectedNamespace, selectedCluster?.value, type]);

  const handleNamespaceChange = (next) => {
    setSelectedNamespace(next);
    setSelectedWorkloads(isMultiSelect ? [] : '');
    if (setResourceFilter) {
      setResourceFilter([{ namespace: next }]);
    }
  };

  const handleWorkloadSingleChange = (next) => {
    setSelectedWorkloads(next);
    const workloadObj = allWorloadObjects.find((g) => g.name === next) || {};
    setResourceFilter([{ name: next, namespace: workloadObj?.namespace ?? '', type: workloadObj?.kind ?? '' }]);
  };

  const handleWorkloadMultiChange = (next) => {
    setSelectedWorkloads(next);
    setResourceFilter(
      next?.map((v) => {
        const workloadObj = allWorloadObjects.find((g) => g.name === v) || {};
        return { name: v, namespace: workloadObj?.namespace ?? '', type: workloadObj?.kind ?? '' };
      })
    );
  };

  const handlePvcNamespaceChange = (next) => {
    setSelectedNamespace(next);
    if (setResourceFilter) {
      setResourceFilter([{ namespace: next, type: 'PersistentVolumeClaim' }]);
    }
    setSelectedPvs(isMultiSelect ? [] : '');
  };

  const handlePvcSingleChange = (next) => {
    setSelectedPvs(next);
    setResourceFilter([{ name: next, type: 'PersistentVolumeClaim', namespace: selectedNamespace }]);
  };

  const handlePvcMultiChange = (next) => {
    setSelectedPvs(next);
    setResourceFilter(next?.map((v) => ({ name: v, type: 'PersistentVolumeClaim', namespace: selectedNamespace })));
  };

  // Coerce legacy array-shaped state into the shape DS Select expects per mode.
  const workloadSingleValue = Array.isArray(selectedWorkloads) ? selectedWorkloads[0] ?? '' : selectedWorkloads ?? '';
  const workloadMultiValue = Array.isArray(selectedWorkloads) ? selectedWorkloads : selectedWorkloads ? [selectedWorkloads] : [];
  const pvcSingleValue = Array.isArray(selectedPvs) ? selectedPvs[0] ?? '' : selectedPvs ?? '';
  const pvcMultiValue = Array.isArray(selectedPvs) ? selectedPvs : selectedPvs ? [selectedPvs] : [];

  if (!data?.data) {
    // data is coming from autoOptimization
    return (
      <Box sx={{ display: 'flex', gap: updatedCard ? ds.space[5] : ds.space[7], flexDirection: 'column' }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: updatedCard ? '2.5fr 0.5fr' : '1fr', gap: ds.space[3] }}>
          <Box sx={{ display: 'flex', gap: ds.space[5] }}>
            {type == 'workload' && (
              <Box sx={{ gap: ds.space[3], display: 'flex', flexDirection: 'row' }}>
                <Box sx={{ width: 240 }}>
                  <Select
                    id='auto-complete-namespace'
                    label='Namespace'
                    required
                    value={selectedNamespace ?? ''}
                    options={namespaces}
                    onChange={handleNamespaceChange}
                    disabled={reviewAutoOptimize || isOptionsLoading}
                    placeholder={isOptionsLoading ? 'Loading…' : 'Select namespace'}
                  />
                </Box>
                <Box sx={{ width: 280 }}>
                  {isMultiSelect ? (
                    <Select
                      id='auto-complete-workloads'
                      label='Application'
                      required={workloadRequired}
                      multiple
                      value={workloadMultiValue}
                      options={workloads}
                      onChange={handleWorkloadMultiChange}
                      disabled={!selectedNamespace || reviewAutoOptimize || isOptionsLoading}
                      placeholder={!selectedNamespace ? 'Select a namespace first' : isOptionsLoading ? 'Loading…' : 'Select application(s)'}
                    />
                  ) : (
                    <Select
                      id='auto-complete-workloads'
                      label='Application'
                      required={workloadRequired}
                      value={workloadSingleValue}
                      options={workloads}
                      onChange={handleWorkloadSingleChange}
                      disabled={!selectedNamespace || reviewAutoOptimize || isOptionsLoading}
                      placeholder={!selectedNamespace ? 'Select a namespace first' : isOptionsLoading ? 'Loading…' : 'Select application'}
                    />
                  )}
                </Box>
              </Box>
            )}
            {type == 'pvc' && (
              <Box sx={{ gap: ds.space[3], display: 'flex', flexDirection: 'row' }}>
                <Box sx={{ width: 280 }}>
                  <Select
                    id='auto-complete-pvc-namespace'
                    label='Namespace'
                    required
                    value={selectedNamespace ?? ''}
                    options={namespaces}
                    onChange={handlePvcNamespaceChange}
                    disabled={reviewAutoOptimize || (data?.auto_optimize_resource_maps?.length ?? 0) > 0 || isOptionsLoading}
                    placeholder={isOptionsLoading ? 'Loading…' : 'Select namespace'}
                  />
                </Box>
                <Box sx={{ width: 280 }}>
                  {isMultiSelect ? (
                    <Select
                      id='auto-complete-pv'
                      label='Persistent Volume Claim'
                      required
                      multiple
                      value={pvcMultiValue}
                      options={pvc}
                      onChange={handlePvcMultiChange}
                      disabled={!selectedNamespace || reviewAutoOptimize || (data?.auto_optimize_resource_maps?.length ?? 0) > 0}
                      placeholder={!selectedNamespace ? 'Select a namespace first' : 'Select PVC(s)'}
                    />
                  ) : (
                    <Select
                      id='auto-complete-pv'
                      label='Persistent Volume Claim'
                      required
                      value={pvcSingleValue}
                      options={pvc}
                      onChange={handlePvcSingleChange}
                      disabled={!selectedNamespace || reviewAutoOptimize || (data?.auto_optimize_resource_maps?.length ?? 0) > 0}
                      placeholder={!selectedNamespace ? 'Select a namespace first' : 'Select PVC'}
                    />
                  )}
                </Box>
              </Box>
            )}
          </Box>
        </Box>
        {children && <>{children}</>}
        {header && (
          <Box
            sx={{
              borderRadius: `${ds.radius.sm} ${ds.radius.sm} 0 0`,
              borderTop: `1px solid ${ds.blue[100]}`,
              background: ds.blue[100],
              padding: `${ds.space[2]} ${ds.space[4]}`,
            }}
          >
            <Typography sx={{ color: ds.gray[700], fontSize: ds.text.title, fontWeight: ds.weight.semibold }}>{header}</Typography>
          </Box>
        )}
      </Box>
    );
  }
  // data is coming from optimization
  return (
    <Box sx={{ display: 'flex', gap: updatedCard ? ds.space[5] : ds.space[7], flexDirection: 'column' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: updatedCard ? '2.5fr 0.5fr' : '1fr', gap: ds.space[3] }}>
        <Box
          sx={{
            width: 'auto',
            minHeight: '88px',
            borderRadius: ds.radius.md,
            padding: `${ds.space[3]} ${ds.space[4]}`,
            background: ds.background[100],
            border: updatedCard && `0.5px solid ${ds.blue[600]}`,
            boxShadow: updatedCard
              ? '0px 2px 7px 0px #3B82F60F, 0px 4px 6px -1px #3B82F61F'
              : '0px 0px 6px -1px rgba(83, 123, 216, 0.40), 0px 2px 10.5px -2px rgba(0, 0, 0, 0.05)',
            display: updatedCard && 'flex',
            alignItems: updatedCard && 'center',
          }}
        >
          {!updatedCard && (
            <Box sx={{ display: 'flex', gap: ds.space[5] }}>
              <Box>
                <Box sx={{ gap: ds.space[1], display: 'flex', flexDirection: 'column' }}>
                  <TextWithValue
                    title='Workload'
                    value={data?.data?.cloud_resourse?.name ?? data?.data?.recommendation?.metadata?.name}
                    valueSize={ds.text.body}
                    valueColor={ds.gray[700]}
                    direction='column'
                  />
                  <Box>
                    <TextWithValue title='Cluster' value={selectedCluster?.label ?? '-'} valueSize={ds.text.body} valueColor={ds.gray[700]} />
                    <TextWithValue
                      title='Namespace'
                      value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.cloud_resourse?.meta?.namespace}
                      valueSize={ds.text.body}
                      valueColor={ds.gray[700]}
                    />
                    <TextWithValue title='Container' value={data?.containerName} valueSize={ds.text.body} valueColor={ds.gray[700]} />
                  </Box>
                </Box>
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />
              <Box>
                <TextWithValue
                  title='Pods'
                  value={data?.data?.cloud_resourse?.meta?.total_pods ?? data?.data?.cloud_resourse?.meta?.total_pods}
                  valueSize={ds.text.body}
                  valueColor={ds.gray[700]}
                />
                <TextWithValue
                  title='Kind'
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.cloud_resourse?.meta?.controllerKind}
                  valueSize={ds.text.body}
                  valueColor={ds.gray[700]}
                />
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />
            </Box>
          )}
          {updatedCard && (
            <Box sx={{ display: 'flex', justifyContent: 'space-between', width: '100%' }}>
              <TextWithValue
                title='Workload'
                value={data?.data?.cloud_resourse?.name}
                valueSize={ds.text.title}
                valueColor={ds.gray[700]}
                direction='column'
                updatedCard={updatedCard}
              />
              <Divider orientation='vertical' sx={{ height: '60px' }} />
              <Box sx={{ gap: ds.space[1], display: 'flex' }}>
                <Box>
                  <TextWithValue
                    title='Cluster'
                    value={selectedCluster?.label ?? '-'}
                    valueSize={ds.text.small}
                    valueColor={ds.gray[700]}
                    sx={{
                      '& .title': {
                        width: '90px',
                      },
                    }}
                  />
                  <TextWithValue
                    title='Namespace'
                    value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.cloud_resourse?.meta?.namespace}
                    valueSize={ds.text.body}
                    valueColor={ds.gray[700]}
                    sx={{
                      '& .title': {
                        width: '90px',
                      },
                    }}
                  />
                </Box>
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />

              <Box>
                <TextWithValue
                  title='Pods'
                  value={data?.data?.cloud_resourse?.meta?.total_pods ?? data?.data?.cloud_resourse?.meta?.total_pods}
                  valueSize={ds.text.body}
                  valueColor={ds.gray[700]}
                  sx={{
                    '& .title': {
                      width: '90px',
                    },
                  }}
                />
                <TextWithValue
                  title='Kind'
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.cloud_resourse?.meta?.controllerKind}
                  valueSize={ds.text.body}
                  valueColor={ds.gray[700]}
                  sx={{
                    '& .title': {
                      width: '90px',
                    },
                  }}
                />
              </Box>
              <Box />
            </Box>
          )}
        </Box>
        {updatedCard && (
          <Box
            sx={{
              width: 'auto',
              minHeight: '88px',
              borderRadius: ds.radius.md,
              padding: `${ds.space[3]} ${ds.space[4]}`,
              background: ds.background[100],
              border: `0.5px solid ${ds.green[400]}`,
              boxShadow: '0px 2px 7px 0px #22C55E0F, 0px 4px 6px -1px #22C55E1F',
            }}
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', height: '100%' }}>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[400], fontWeight: ds.weight.regular, textAlign: 'right' }}>
                Savings
              </Typography>
              <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Currency
                  value={data.data.estimated_savings}
                  precison={1}
                  sx={{
                    color: ds.green[500],
                    fontSize: '24px',
                    fontWeight: ds.weight.medium,
                  }}
                  sxSuffix={{
                    color: ds.gray[400],
                    fontSize: ds.text.small,
                    fontWeight: ds.weight.regular,
                  }}
                  sxPrefix={{
                    color: ds.gray[400],
                    fontSize: ds.text.small,
                    fontWeight: ds.weight.regular,
                  }}
                  suffix='/mo'
                />{' '}
              </Box>
            </Box>
          </Box>
        )}
      </Box>
      {children && <>{children}</>}
      {header && (
        <Box
          sx={{
            borderRadius: '4px 4px 0px 0px',
            borderTop: `1px solid ${ds.blue[100]}`,
            background: ds.blue[100],
            padding: '8px 16px',
          }}
        >
          <Typography sx={{ color: ds.gray[700], fontSize: ds.text.title, fontWeight: ds.weight.semibold }}>{header}</Typography>
        </Box>
      )}
    </Box>
  );
};

AutoPilotHeaderCard.propTypes = {
  header: PropTypes.string,
  data: PropTypes.object,
  children: PropTypes.any,
  updatedCard: PropTypes.bool,
  setResourceFilter: PropTypes.func,
  isMultiSelect: PropTypes.bool,
  type: PropTypes.string,
  scalingType: PropTypes.string,
  reviewAutoOptimize: PropTypes.bool,
  workloadRequired: PropTypes.bool,
};

export default AutoPilotHeaderCard;
