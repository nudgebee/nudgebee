import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import dynamic from 'next/dynamic';
import PropTypes from 'prop-types';
import ClusterCustomTooltip from './ClusterCustomTooltip';
import Text from '@common-new/format/Text';
import { formatValueWithUnit } from 'src/utils/common';
import { ds } from '@utils/colors';

const GaugeComponent = dynamic(() => import('react-gauge-component'), { ssr: false });

const K8sMemoryCpuIndicator = ({
  unit,
  title = '-',
  data = [],
  clusterSummary = false,
  showUpdatedUi = false,
  requiredTooltip = false,
  colors = [ds.red[500], ds.green[400], ds.red[500]],
  primaryPointerColor = ds.gray[200],
  updatedOverview = false,
  showUsage = false,
  hideLabels = false,
}) => {
  const [showTooltip, setShowTooltip] = useState(false);

  if (data.length === 0) {
    return <Typography>{title} Data Not Available...</Typography>;
  }

  const { total, usage, limit, request, p50usage, p90usage, maxusage, units } = data.reduce(
    (accumulator, entry) => {
      const lowerCaseName = entry.name.toLowerCase();
      accumulator[lowerCaseName] = entry[lowerCaseName] || 0;
      accumulator.units[lowerCaseName] = formatValueWithUnit(entry[lowerCaseName], title);
      return accumulator;
    },
    { units: {} }
  );

  const styles = {
    tooltip: { border: 'none', color: 'black', textShadow: 'none' },
    values: {
      fontSize: 'var(--ds-text-small)',
      fontWeight: 'var(--ds-font-weight-medium)',
      color: 'var(--ds-brand-500)',
      width: ds.space.mul(0, 40),
      span: {
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-regular)',
        color: 'var(--ds-brand-300)',
      },
      display: 'grid',
      gridTemplateColumns: 'auto auto',
    },
    keys: {
      fontSize: 'var(--ds-text-caption)',
      fontWeight: 'var(--ds-font-weight-regular)',
      color: 'var(--ds-gray-400)',
      width: ds.space.mul(0, 30),
    },
  };
  return (
    <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'column', flex: 1 }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text value={title} sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
      </Box>
      {updatedOverview ? (
        <>
          <Box
            sx={{
              position: 'relative',
              top: ds.space.mul(0, -8),
              left: ds.space.mul(0, -20),
              width: clusterSummary ? ds.space.mul(0, 60) : ds.space.mul(0, 42),
              '.doughnut .outerSubArc': {
                display: `none !important`,
              },
              '.doughnut .subArc:last-child path': {
                fill: `${primaryPointerColor} !important`,
              },
              '@media (max-width: 1300px)': {
                left: ds.space.mul(0, -30),
              },
            }}
          >
            <GaugeComponent
              className='custom-gauge'
              labels={{
                tickLabels: { hideMinMax: true },
                valueLabel: {
                  style: {
                    fontSize: 'var(--ds-text-display)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    fill: 'var(--ds-brand-500)',
                    textShadow: 'none',
                    transform: 'translateY(-20px)',
                  },
                },
              }}
              style={{ width: '230px' }}
              value={usage > 0 && total > 0 ? ((usage / total) * 100)?.toFixed() : 0}
              arc={{
                colorArray: colors,
                subArcs: [
                  {
                    tooltip: { text: 'low', style: styles.tooltip },
                    showTick: false,
                    length: 0.3,
                    onMouseMove: () => {
                      setShowTooltip(true);
                    },
                    onMouseLeave: () => {
                      setShowTooltip(false);
                    },
                  },
                  {
                    tooltip: { text: 'moderate', style: styles.tooltip },
                    showTick: false,
                    length: 0.7,
                    onMouseMove: () => {
                      setShowTooltip(true);
                    },
                    onMouseLeave: () => {
                      setShowTooltip(false);
                    },
                  },
                  {
                    tooltip: { text: 'high', style: styles.tooltip },
                    showTick: false,
                    length: 0.1,
                    onMouseMove: () => {
                      setShowTooltip(true);
                    },
                    onMouseLeave: () => {
                      setShowTooltip(false);
                    },
                  },
                ],
                padding: 0.03,
                width: 0.3,
              }}
              pointer={{
                elastic: true,
                animationDelay: 0,
              }}
            />
            <Box
              sx={{
                position: 'absolute',
                top: '64%',
                left: '133%',
                transform: 'translate(-50%, -50%)',
                width: ds.space.mul(0, 49),
                height: ds.space.mul(0, 36),
                borderRadius: ds.radius.pill,
                cursor: 'pointer',
              }}
              onMouseEnter={() => setShowTooltip(true)}
              onMouseLeave={() => setShowTooltip(false)}
            />
            <Typography
              sx={{
                position: 'absolute',
                bottom: ds.space[3],
                left: ds.space.mul(0, 49),
                color: 'var(--ds-brand-300)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
              }}
            >
              Usage
            </Typography>
            {requiredTooltip && (
              <ClusterCustomTooltip
                showTooltip={showTooltip}
                usage={usage}
                available={total}
                limit={limit}
                request={request}
                unit={unit}
                title={title}
              />
            )}
          </Box>
          {!hideLabels && (
            <Box>
              <Box display={'flex'} gap={ds.space.mul(0, 7)}>
                <Box width={ds.space.mul(0, 40)} />
                <Box sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-400)' }}>
                  {unit}
                </Box>
              </Box>
              <Box display={'flex'} gap={ds.space.mul(0, 7)} alignItems={'center'} mt={ds.space[1]}>
                <Box sx={styles.keys}>Total</Box>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-brand-500)',
                    width: ds.space.mul(0, 40),
                  }}
                >
                  {total > 0 ? units.total.value.toFixed(1) + (units?.total?.unit || '') : '-'}
                </Box>
              </Box>
              <Box display={'flex'} gap={ds.space.mul(0, 7)} alignItems={'center'} mt={ds.space[1]}>
                <Box sx={styles.keys}>Usage</Box>
                <Box sx={styles.values}>
                  {usage > 0 ? units.usage.value.toFixed(1) + (units?.usage?.unit || '') : '-'}
                  <span>{usage > 0 && total > 0 ? '(' + ((usage / total) * 100)?.toFixed() + '%)' : '-'}</span>
                </Box>
              </Box>
              <Box display={'flex'} gap={ds.space.mul(0, 7)} alignItems={'center'} mt={ds.space[1]}>
                <Box sx={styles.keys}>Request</Box>
                <Box sx={styles.values}>
                  {request > 0 ? units.request.value.toFixed(1) + (units?.request?.unit || '') : '-'}{' '}
                  <span>{request > 0 && total > 0 ? '(' + ((request / total) * 100)?.toFixed() + '%)' : '-'}</span>
                </Box>
              </Box>
              <Box display={'flex'} gap={ds.space.mul(0, 7)} alignItems={'center'} mt={ds.space[1]}>
                <Box sx={styles.keys}>Limit</Box>
                <Box sx={styles.values}>
                  {limit > 0 ? units.limit.value.toFixed(1) + (units?.limit?.unit || '') : '-'}{' '}
                  <span>{limit > 0 && total > 0 ? '(' + ((limit / total) * 100)?.toFixed() + '%)' : '-'}</span>
                </Box>
              </Box>
              {showUsage && (
                <Box display={'flex'} gap={ds.space.mul(0, 7)} alignItems={'center'} mt={ds.space[1]}>
                  <Box sx={styles.keys}>P50 Usage</Box>
                  <Box sx={styles.values}>
                    {p50usage > 0 ? units.p50usage.value?.toFixed(1) + (units.p50usage?.unit || '') : '-'}{' '}
                    <span>{p50usage > 0 && total > 0 ? '(' + ((p50usage / total) * 100).toFixed() + '%)' : '-'}</span>
                  </Box>
                </Box>
              )}
              {showUsage && (
                <Box display={'flex'} gap={ds.space.mul(0, 7)} alignItems={'center'} mt={ds.space[1]}>
                  <Box sx={styles.keys}>P90 Usage</Box>
                  <Box sx={styles.values}>
                    {p90usage > 0 ? units.p90usage.value?.toFixed(1) + (units?.p90usage?.unit || '') : '-'}{' '}
                    <span>{p90usage > 0 && total > 0 ? '(' + ((p90usage / total) * 100).toFixed() + '%)' : '-'}</span>
                  </Box>
                </Box>
              )}
              {showUsage && (
                <Box display={'flex'} gap={ds.space.mul(0, 7)} alignItems={'center'} mt={ds.space[1]}>
                  <Box sx={styles.keys}>Max Usage</Box>
                  <Box sx={styles.values}>
                    {maxusage > 0 ? units.maxusage.value?.toFixed(1) + (units?.maxusage?.unit || '') : '-'}{' '}
                    <span>{maxusage > 0 && total > 0 ? '(' + (((maxusage / total) * 100).toFixed() || '-') + '%)' : '-'}</span>
                  </Box>
                </Box>
              )}
            </Box>
          )}
        </>
      ) : (
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'space-evenly',
          }}
        >
          <Box
            sx={{
              position: 'relative',
              top: ds.space.mul(0, -8),
              left: ds.space.mul(0, -40),
              width: clusterSummary ? ds.space.mul(0, 60) : ds.space.mul(0, 42),
              '.doughnut .outerSubArc': {
                display: `none !important`,
              },
              '.doughnut .subArc:last-child path': {
                fill: `${primaryPointerColor} !important`,
              },
            }}
          >
            <GaugeComponent
              className='custom-gauge'
              labels={{
                tickLabels: { hideMinMax: true },
                valueLabel: {
                  style: {
                    fontSize: 'var(--ds-text-display)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    fill: 'var(--ds-brand-500)',
                    textShadow: 'none',
                    transform: 'translateY(-20px)',
                  },
                },
              }}
              style={{ width: '230px' }}
              value={usage > 0 && total > 0 ? ((usage / total) * 100)?.toFixed() : 0}
              arc={{
                colorArray: colors,
                subArcs: [
                  {
                    tooltip: { text: 'low', style: styles.tooltip },
                    showTick: false,
                    length: 0.3,
                    onMouseMove: () => {
                      setShowTooltip(true);
                    },
                    onMouseLeave: () => {
                      setShowTooltip(false);
                    },
                  },
                  {
                    tooltip: { text: 'moderate', style: styles.tooltip },
                    showTick: false,
                    length: 0.7,
                    onMouseMove: () => {
                      setShowTooltip(true);
                    },
                    onMouseLeave: () => {
                      setShowTooltip(false);
                    },
                  },
                  {
                    tooltip: { text: 'high', style: styles.tooltip },
                    showTick: false,
                    length: 0.1,
                    onMouseMove: () => {
                      setShowTooltip(true);
                    },
                    onMouseLeave: () => {
                      setShowTooltip(false);
                    },
                  },
                ],
                padding: 0.03,
                width: 0.3,
              }}
              pointer={{
                elastic: true,
                animationDelay: 0,
              }}
            />
            <Box
              sx={{
                position: 'absolute',
                top: '64%',
                left: '134%',
                transform: 'translate(-50%, -50%)',
                width: ds.space.mul(0, 49),
                height: ds.space.mul(0, 36),
                borderRadius: ds.radius.pill,
                cursor: 'pointer',
              }}
              onMouseEnter={() => setShowTooltip(true)}
              onMouseLeave={() => setShowTooltip(false)}
            />
            <Typography
              sx={{
                position: 'absolute',
                bottom: ds.space[3],
                left: ds.space.mul(0, 49),
                color: 'var(--ds-brand-300)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
              }}
            >
              Usage
            </Typography>
            {requiredTooltip && (
              <ClusterCustomTooltip showTooltip={showTooltip} usage={usage} available={total} limit={limit} request={request} title={title} />
            )}
          </Box>
          {showUpdatedUi ? (
            <Box sx={{ marginTop: 'var(--ds-space-4)' }}>
              {unit && (
                <Typography position={'relative'} right={ds.space.mul(0, -29)} fontSize={ds.text.caption} color={ds.gray[400]}>
                  {unit}
                </Typography>
              )}
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography
                    sx={{
                      color: 'var(--ds-gray-600)',
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      alignItems: 'end',
                      minWidth: ds.space.mul(0, 28),
                    }}
                  >
                    Total:
                  </Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: ds.space[6], justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative' }}>
                    <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                      {total > 0 ? units.total.value.toFixed(1) + (units?.total?.unit || '') : ''}
                    </Typography>
                  </Box>
                </Box>
              </Box>
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography
                    sx={{
                      color: 'var(--ds-gray-600)',
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      alignItems: 'end',
                      minWidth: ds.space.mul(0, 28),
                    }}
                  >
                    Usage:{' '}
                  </Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: ds.space[6], justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'row' }}>
                    <Typography
                      sx={{
                        color: 'var(--ds-brand-500)',
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        mr: 'var(--ds-space-1)',
                      }}
                    >
                      {usage > 0 ? units.usage.value.toFixed(1) + (units?.usage?.unit || '') : '-'}
                    </Typography>
                    <Typography sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                      {usage > 0 && total > 0 ? '(' + ((usage / total) * 100)?.toFixed() + '%' + ')' : '-'}
                    </Typography>
                  </Box>
                </Box>
              </Box>
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography
                    sx={{
                      color: 'var(--ds-gray-600)',
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      alignItems: 'end',
                      minWidth: ds.space.mul(0, 28),
                    }}
                  >
                    Limit:{' '}
                  </Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: ds.space[6], justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'row' }}>
                    <Typography
                      sx={{
                        color: 'var(--ds-brand-500)',
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        mr: 'var(--ds-space-1)',
                      }}
                    >
                      {limit > 0 ? units.limit.value.toFixed(1) + (units?.limit?.unit || '') : '-'}
                    </Typography>

                    <Typography sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                      {limit > 0 && total > 0 ? '(' + ((limit / total) * 100)?.toFixed() + '%' + ')' : '-'}
                    </Typography>
                  </Box>
                </Box>
              </Box>
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography
                    sx={{
                      color: 'var(--ds-gray-600)',
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      alignItems: 'end',
                      minWidth: ds.space.mul(0, 28),
                    }}
                  >
                    Request:{' '}
                  </Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: ds.space[6], justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'row' }}>
                    <Typography
                      sx={{
                        color: 'var(--ds-brand-500)',
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        mr: 'var(--ds-space-1)',
                      }}
                    >
                      {request > 0 ? units.request.value.toFixed(1) + (units?.request?.unit || '') : '-'}
                    </Typography>
                    <Typography sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                      {request > 0 && total > 0 ? '(' + ((request / total) * 100)?.toFixed() + '%' + ')' : '-'}
                    </Typography>
                  </Box>
                </Box>
              </Box>
            </Box>
          ) : (
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', marginTop: 'var(--ds-space-6)', flexDirection: 'column', textAlign: 'left' }}>
              <Box sx={{ width: ds.space.mul(0, 26) }}>
                <Typography
                  sx={{
                    color: 'var(--ds-gray-600)',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    alignItems: 'end',
                  }}
                >
                  Usage
                </Typography>
              </Box>
              <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', alignItems: 'center' }}>
                <Box sx={{ position: 'relative', textAlign: 'right' }}>
                  <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                    {usage > 0 ? units.usage.value.toFixed(1) + (units?.usage?.unit || '') : '-'}
                  </Typography>
                </Box>
                <Box sx={{ marginLeft: 'var(--ds-space-1)' }}>
                  <Typography sx={{ color: 'var(--ds-brand-300)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                    {usage > 0 && total > 0 ? '(' + ((usage / total) * 100)?.toFixed() + '%)' : '-'}
                  </Typography>
                </Box>
              </Box>
            </Box>
          )}
        </Box>
      )}
    </Box>
  );
};

export default K8sMemoryCpuIndicator;

K8sMemoryCpuIndicator.propTypes = {
  title: PropTypes.string,
  data: PropTypes.array,
  clusterSummary: PropTypes.bool,
  unit: PropTypes.any,
  showUpdatedUi: PropTypes.bool,
  requiredTooltip: PropTypes.bool,
  colors: PropTypes.arrayOf(PropTypes.string),
  primaryPointerColor: PropTypes.string,
  updatedOverview: PropTypes.bool,
  showUsage: PropTypes.bool,
  hideLabels: PropTypes,
};
