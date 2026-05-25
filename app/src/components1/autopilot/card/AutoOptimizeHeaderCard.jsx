import React from 'react';
import { Autocomplete, Box, Chip, Divider, Paper, TextField, Typography, CircularProgress } from '@mui/material';
import { useData } from '@context/DataContext';
import PropTypes from 'prop-types';
import k8sApi from '@api1/kubernetes';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { Text } from '@components1/common';
import CustomTooltip from '@components1/common/CustomTooltip';
import Currency from '@components1/common/format/Currency';
import { colors } from 'src/utils/colors';

const CustomPaper = (props) => {
  return <Paper sx={{ width: 'fit-content', overflowY: 'auto' }} elevation={8} {...props} />;
};

const TextWithValue = ({ title, value, valueSize = '12px', valueColor = colors.text.tertiary, direction = 'row', updatedCard = false, sx = {} }) => {
  return (
    <Box sx={{ ...sx, display: 'flex', flexDirection: direction, alignItems: 'baseline' }}>
      <Typography
        sx={{ fontSize: '12px', fontWeight: 400, color: updatedCard ? colors.text.secondaryDark : colors.text.tertiary, marginRight: '8px' }}
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

  const _sortOptions = (options, selectedValues) =>
    options.sort((a, b) => {
      const aSelected = selectedValues.includes(a);
      const bSelected = selectedValues.includes(b);
      let value = 1;
      if (aSelected) {
        value = -1;
      }
      return aSelected === bSelected ? 0 : value;
    });

  if (!data?.data) {
    // data is coming from autoOptimization
    return (
      <Box sx={{ display: 'flex', gap: updatedCard ? '22px' : '52px', flexDirection: 'column' }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: updatedCard ? '2.5fr 0.5fr' : '1fr', gap: '10px' }}>
          <Box sx={{ display: 'flex', gap: '24px' }}>
            {type == 'workload' && (
              <Box sx={{ gap: '10px', display: 'flex', flexDirection: 'row' }}>
                <Autocomplete
                  size='medium'
                  sx={{
                    maxWidth: 240,
                    minWidth: 240,
                    '& .MuiOutlinedInput-root': {
                      padding: '2px 14px !important',
                    },
                    '& .MuiAutocomplete-input': {
                      padding: '7.5px 45px 7.5px 5px !important',
                    },
                    '& .MuiInputLabel-root': {
                      lineHeight: '0.7em !important',
                      overflow: 'visible !important',
                    },
                    height: '35px',
                  }}
                  id={`auto-complete-namespace`}
                  blurOnSelect={'mouse'}
                  value={selectedNamespace}
                  loading={isOptionsLoading}
                  options={namespaces}
                  popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                  onChange={(event, value) => {
                    setSelectedNamespace(value);
                    setSelectedWorkloads([]);
                    if (setResourceFilter) {
                      setResourceFilter([{ namespace: value }]);
                    }
                  }}
                  disabled={reviewAutoOptimize}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={'Namespace'}
                      required
                      InputProps={{
                        ...params.InputProps,
                        endAdornment: (
                          <React.Fragment>
                            {isOptionsLoading ? <CircularProgress color='inherit' size={20} /> : null}
                            {params.InputProps.endAdornment}
                          </React.Fragment>
                        ),
                      }}
                      disabled={reviewAutoOptimize}
                    />
                  )}
                  ListboxProps={{ sx: { wordBreak: 'break-word' } }}
                  PaperComponent={CustomPaper}
                  isOptionEqualToValue={(option, value) => {
                    if (value === '' || option.value === '') {
                      return true;
                    }
                    if (typeof option === 'string') {
                      return option === value;
                    }
                    return option.value === value.value;
                  }}
                />
                <Autocomplete
                  size='medium'
                  key={`auto-complete-workloads`}
                  id={`auto-complete-workloads`}
                  sx={{
                    maxWidth: 280,
                    minWidth: 280,
                    '& .MuiOutlinedInput-root': {
                      padding: '2px 14px !important',
                      backgroundColor: !selectedNamespace ? colors.background.input : colors.background.white,
                    },
                    '& .MuiAutocomplete-input': {
                      padding: '7.5px 45px 7.5px 5px !important',
                    },
                    '& .MuiInputLabel-root': {
                      lineHeight: '0.7em !important',
                      overflow: 'visible !important',
                    },
                    height: '35px',
                  }}
                  disabled={!selectedNamespace || reviewAutoOptimize}
                  value={selectedWorkloads}
                  loading={isOptionsLoading}
                  options={workloads}
                  popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                  onChange={(event, value) => {
                    setSelectedWorkloads(value);
                    if (!isMultiSelect) {
                      const workloadObj = allWorloadObjects.find((g) => g.name === value) || {};
                      setResourceFilter([{ name: value, namespace: workloadObj?.namespace ?? '', type: workloadObj?.kind ?? '' }]);
                    } else {
                      setResourceFilter(
                        value?.map((v) => {
                          const workloadObj = allWorloadObjects.find((g) => g.name === v) || {};
                          return { name: v, namespace: workloadObj?.namespace ?? '', type: workloadObj?.kind ?? '' };
                        })
                      );
                    }
                  }}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={'Application'}
                      InputProps={{
                        ...params.InputProps,
                        endAdornment: (
                          <React.Fragment>
                            {isOptionsLoading ? <CircularProgress color='inherit' size={20} /> : null}
                            {params.InputProps.endAdornment}
                          </React.Fragment>
                        ),
                      }}
                      disabled={reviewAutoOptimize}
                      required={workloadRequired}
                    />
                  )}
                  ListboxProps={{ sx: { wordBreak: 'break-word' } }}
                  PaperComponent={CustomPaper}
                  isOptionEqualToValue={(option, value) => {
                    if (option.value === '') {
                      return true;
                    }
                    if (typeof option === 'string') {
                      return option === value;
                    }
                    return option.value === value.value;
                  }}
                  renderTags={(tagValue, getTagProps) => {
                    const visibleTagValue = tagValue.slice(0, 1);
                    const hiddenTagValue = tagValue.slice(1);

                    const allLabels = hiddenTagValue?.map((tag, index) => {
                      const chipLabel = typeof tag === 'string' ? tag : 'Unknown';
                      return (
                        <Box key={chipLabel}>
                          <Chip
                            label={chipLabel}
                            size='small'
                            sx={{ my: '3px' }}
                            onDelete={() => {
                              const newValue = tagValue.filter((_, i) => i !== index + 1);
                              setSelectedWorkloads(newValue);
                            }}
                          />
                        </Box>
                      );
                    });

                    return (
                      <div style={{ display: 'flex', alignItems: 'center' }}>
                        {visibleTagValue.map((tag, index) => {
                          let label = '';
                          if (typeof tag === 'string') {
                            label = tag;
                          } else if (tag && typeof tag === 'object') {
                            label = tag.label || tag.value;
                          }

                          return (
                            <Chip
                              size='small'
                              label={<Text value={label} showAutoEllipsis fontSize='12px' />}
                              {...getTagProps({ index })}
                              key={label}
                              onDelete={() => {
                                const newValue = tagValue.filter((_, i) => i !== 0);
                                setSelectedWorkloads(newValue);
                              }}
                            />
                          );
                        })}
                        {hiddenTagValue?.length > 0 && (
                          <CustomTooltip
                            tooltipStyle={{
                              maxHeight: '130px',
                              overflowY: 'scroll',
                              '::-webkit-scrollbar': {
                                width: '4px',
                              },
                            }}
                            placement='bottom'
                            title={<div>{allLabels}</div>}
                            arrow
                          >
                            <span style={{ marginLeft: '4px', cursor: 'pointer' }}>{`+${hiddenTagValue?.length}`}</span>
                          </CustomTooltip>
                        )}
                      </div>
                    );
                  }}
                />
              </Box>
            )}
            {type == 'pvc' && (
              <Box sx={{ gap: '10px', display: 'flex', flexDirection: 'row' }}>
                <Autocomplete
                  size='medium'
                  sx={{
                    maxWidth: 280,
                    minWidth: 280,
                    '& .MuiOutlinedInput-root': {
                      padding: '2px 14px !important',
                    },
                    '& .MuiInputLabel-root': {
                      lineHeight: '0.6em',
                      overflow: 'visible',
                      fontSize: '14px',
                    },
                    height: '35px',
                  }}
                  id={`auto-complete-namespace`}
                  blurOnSelect={'mouse'}
                  value={selectedNamespace}
                  options={namespaces}
                  popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                  onChange={(event, value) => {
                    setSelectedNamespace(value);
                    if (setResourceFilter) {
                      setResourceFilter([{ namespace: value, type: 'PersistentVolumeClaim' }]);
                    }
                    setSelectedPvs([]);
                  }}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={'Namespace'}
                      required
                      size='medium'
                      InputProps={{
                        ...params.InputProps,
                        endAdornment: (
                          <React.Fragment>
                            {isOptionsLoading ? <CircularProgress color='inherit' size={20} /> : null}
                            {params.InputProps.endAdornment}
                          </React.Fragment>
                        ),
                      }}
                    />
                  )}
                  ListboxProps={{ sx: { wordBreak: 'break-word' } }}
                  PaperComponent={CustomPaper}
                  isOptionEqualToValue={(option, value) => {
                    if (value === '' || option.value === '') {
                      return true;
                    }
                    if (typeof option === 'string') {
                      return option === value;
                    }
                    return option.value === value.value;
                  }}
                  disabled={reviewAutoOptimize || (data?.auto_optimize_resource_maps?.length ?? 0) > 0}
                />
                <Autocomplete
                  size='medium'
                  multiple={isMultiSelect ?? true}
                  key={`auto-complete-pv`}
                  id={`auto-complete-pv`}
                  sx={{
                    maxWidth: 280,
                    minWidth: 280,
                    '& .MuiOutlinedInput-root': {
                      padding: '2px 14px !important',
                    },
                    '& .MuiInputLabel-root': {
                      lineHeight: '0.6em',
                      overflow: 'visible',
                      fontSize: '14px',
                    },
                    height: '35px',
                  }}
                  blurOnSelect={'mouse'}
                  value={selectedPvs}
                  options={pvc}
                  popupIcon={<KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: colors.text.tertiary }} />}
                  onChange={(event, value) => {
                    setSelectedPvs(value);
                    if (!isMultiSelect) {
                      setResourceFilter([{ name: value, type: 'PersistentVolumeClaim', namespace: selectedNamespace }]);
                    } else {
                      setResourceFilter(
                        value?.map((v) => {
                          return { name: v, type: 'PersistentVolumeClaim', namespace: selectedNamespace };
                        })
                      );
                    }
                  }}
                  renderInput={(params) => <TextField {...params} required label={'Persistent Volume Claim'} size='medium' sx={{ width: '260px' }} />}
                  ListboxProps={{ sx: { wordBreak: 'break-word' } }}
                  PaperComponent={CustomPaper}
                  isOptionEqualToValue={(option, value) => {
                    if (option.value === '') {
                      return true;
                    }
                    if (typeof option === 'string') {
                      return option === value;
                    }
                    return option.value === value.value;
                  }}
                  disabled={!selectedNamespace || reviewAutoOptimize || (data?.auto_optimize_resource_maps?.length ?? 0) > 0}
                  renderTags={(tagValue, getTagProps) => {
                    const visibleTagValue = tagValue.slice(0, 1);
                    const hiddenTagValue = tagValue.slice(1);

                    const allLabels = hiddenTagValue?.map((tag, index) => {
                      const chipLabel = typeof tag === 'string' ? tag : 'Unknown';
                      return (
                        <Box key={chipLabel}>
                          <Chip
                            label={chipLabel}
                            size='small'
                            sx={{ my: '3px' }}
                            onDelete={() => {
                              const newValue = tagValue.filter((_, i) => i !== index + 1);
                              setSelectedPvs(newValue);
                            }}
                            disabled={reviewAutoOptimize}
                          />
                        </Box>
                      );
                    });

                    return (
                      <div style={{ display: 'flex', alignItems: 'center' }}>
                        {visibleTagValue.map((tag, index) => {
                          let label = '';
                          if (typeof tag === 'string') {
                            label = tag;
                          } else if (tag && typeof tag === 'object') {
                            label = tag.label || tag.value;
                          }

                          return (
                            <Chip
                              size='small'
                              label={<Text value={label} showAutoEllipsis fontSize='12px' />}
                              {...getTagProps({ index })}
                              key={label}
                              onDelete={() => {
                                const newValue = tagValue.filter((_, i) => i !== 0);
                                setSelectedPvs(newValue);
                              }}
                              disabled={reviewAutoOptimize}
                            />
                          );
                        })}
                        {hiddenTagValue?.length > 0 && (
                          <CustomTooltip placement='bottom' title={<div>{allLabels}</div>} arrow>
                            <span style={{ marginLeft: '4px', cursor: 'pointer' }}>{`+${hiddenTagValue?.length}`}</span>
                          </CustomTooltip>
                        )}
                      </div>
                    );
                  }}
                />
              </Box>
            )}
          </Box>
        </Box>
        {children && <>{children}</>}
        {header && (
          <Box
            sx={{
              borderRadius: '4px 4px 0px 0px',
              borderTop: `1px solid ${colors.background.activeAnchorButton})`,
              background: colors.background.primaryLightest,
              padding: '8px 16px',
            }}
          >
            <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{header}</Typography>
          </Box>
        )}
      </Box>
    );
  }
  // data is coming from optimization
  return (
    <Box sx={{ display: 'flex', gap: updatedCard ? '22px' : '52px', flexDirection: 'column' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: updatedCard ? '2.5fr 0.5fr' : '1fr', gap: '10px' }}>
        <Box
          sx={{
            width: 'auto',
            minHeight: '88px',
            borderRadius: '6px',
            padding: '12px 16px',
            background: colors.background.white,
            border: updatedCard && `0.5px solid ${colors.border.primary}`,
            boxShadow: updatedCard
              ? '0px 2px 7px 0px #3B82F60F, 0px 4px 6px -1px #3B82F61F'
              : '0px 0px 6px -1px rgba(83, 123, 216, 0.40), 0px 2px 10.5px -2px rgba(0, 0, 0, 0.05)',
            display: updatedCard && 'flex',
            alignItems: updatedCard && 'center',
          }}
        >
          {!updatedCard && (
            <Box sx={{ display: 'flex', gap: '24px' }}>
              <Box>
                <Box sx={{ gap: '4px', display: 'flex', flexDirection: 'column' }}>
                  <TextWithValue
                    title='Workload'
                    value={data?.data?.cloud_resourse?.name ?? data?.data?.recommendation?.metadata?.name}
                    valueSize='14px'
                    valueColor={colors.text.secondary}
                    direction='column'
                  />
                  <Box>
                    <TextWithValue title='Cluster' value={selectedCluster?.label ?? '-'} valueSize='14px' valueColor={colors.text.secondary} />
                    <TextWithValue
                      title='Namespace'
                      value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.cloud_resourse?.meta?.namespace}
                      valueSize='14px'
                      valueColor={colors.text.secondary}
                    />
                    <TextWithValue title='Container' value={data?.containerName} valueSize='14px' valueColor={colors.text.secondary} />
                  </Box>
                </Box>
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />
              <Box>
                <TextWithValue
                  title='Pods'
                  value={data?.data?.cloud_resourse?.meta?.total_pods ?? data?.data?.cloud_resourse?.meta?.total_pods}
                  valueSize='14px'
                  valueColor={colors.text.secondary}
                />
                <TextWithValue
                  title='Kind'
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.cloud_resourse?.meta?.controllerKind}
                  valueSize='14px'
                  valueColor={colors.text.secondary}
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
                valueSize='16px'
                valueColor={colors.text.secondary}
                direction='column'
                updatedCard={updatedCard}
              />
              <Divider orientation='vertical' sx={{ height: '60px' }} />
              <Box sx={{ gap: '4px', display: 'flex' }}>
                <Box>
                  <TextWithValue
                    title='Cluster'
                    value={selectedCluster?.label ?? '-'}
                    valueSize='12px'
                    valueColor={colors.text.secondary}
                    sx={{
                      '& .title': {
                        width: '90px',
                      },
                    }}
                  />
                  <TextWithValue
                    title='Namespace'
                    value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.cloud_resourse?.meta?.namespace}
                    valueSize='14px'
                    valueColor={colors.text.secondary}
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
                  valueSize='14px'
                  valueColor={colors.text.secondary}
                  sx={{
                    '& .title': {
                      width: '90px',
                    },
                  }}
                />
                <TextWithValue
                  title='Kind'
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.cloud_resourse?.meta?.controllerKind}
                  valueSize='14px'
                  valueColor={colors.text.secondary}
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
              borderRadius: '6px',
              padding: '12px 16px',
              background: colors.background.white,
              border: '0.5px solid #4ADE80',
              boxShadow: '0px 2px 7px 0px #22C55E0F, 0px 4px 6px -1px #22C55E1F',
            }}
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', height: '100%' }}>
              <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark, fontWeight: 400, textAlign: 'right' }}>Savings</Typography>
              <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Currency
                  value={data.data.estimated_savings}
                  precison={1}
                  sx={{
                    color: colors.text.currency,
                    fontSize: '24px',
                    fontWeight: 500,
                  }}
                  sxSuffix={{
                    color: colors.text.secondaryDark,
                    fontSize: '12px',
                    fontWeight: 400,
                  }}
                  sxPrefix={{
                    color: colors.text.secondaryDark,
                    fontSize: '12px',
                    fontWeight: 400,
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
            borderTop: `1px solid ${colors.background.activeAnchorButton}`,
            background: colors.background.primaryLightest,
            padding: '8px 16px',
          }}
        >
          <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{header}</Typography>
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
