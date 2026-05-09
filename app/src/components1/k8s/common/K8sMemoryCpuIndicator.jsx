import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import dynamic from 'next/dynamic';
import PropTypes from 'prop-types';
import ClusterCustomTooltip from './ClusterCustomTooltip';
import { Text } from '@components1/common';
import { formatValueWithUnit } from 'src/utils/common';

const GaugeComponent = dynamic(() => import('react-gauge-component'), { ssr: false });

const K8sMemoryCpuIndicator = ({
  unit,
  title = '-',
  data = [],
  clusterSummary = false,
  showUpdatedUi = false,
  requiredTooltip = false,
  colors = ['#EF4444', '#4ADE80', '#EF4444'],
  primaryPointerColor = '#EBEBEB',
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
      fontSize: '12px',
      fontWeight: 500,
      color: '#374151',
      width: '80px',
      span: {
        fontSize: '12px',
        fontWeight: 400,
        color: '#B9B9B9',
      },
      display: 'grid',
      gridTemplateColumns: 'auto auto',
    },
    keys: { fontSize: '10px', fontWeight: 400, color: '#9F9F9F', width: '60px' },
  };
  return (
    <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'column', flex: 1 }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Text value={title} sx={{ fontWeight: 500 }} />
      </Box>
      {updatedOverview ? (
        <>
          <Box
            sx={{
              position: 'relative',
              top: '-16px',
              left: '-40px',
              width: clusterSummary ? '120px' : '85px',
              '.doughnut .outerSubArc': {
                display: `none !important`,
              },
              '.doughnut .subArc:last-child path': {
                fill: `${primaryPointerColor} !important`,
              },
              '@media (max-width: 1300px)': {
                left: '-60px',
              },
            }}
          >
            <GaugeComponent
              className='custom-gauge'
              labels={{
                tickLabels: { hideMinMax: true },
                valueLabel: { style: { fontSize: '30px', fontWeight: 500, fill: '#374151', textShadow: 'none', transform: 'translateY(-20px)' } },
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
                width: '99px',
                height: '72px',
                borderRadius: '50%',
                cursor: 'pointer',
              }}
              onMouseEnter={() => setShowTooltip(true)}
              onMouseLeave={() => setShowTooltip(false)}
            />
            <Typography sx={{ position: 'absolute', bottom: '12px', left: '99px', color: '#B9B9B9', fontSize: '12px', fontWeight: 400 }}>
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
              <Box display={'flex'} gap='15px'>
                <Box width={'80px'} />
                <Box sx={{ fontSize: '10px', fontWeight: 400, color: '#9F9F9F' }}>{unit}</Box>
              </Box>
              <Box display={'flex'} gap='15px' alignItems={'center'} mt={'4px'}>
                <Box sx={styles.keys}>Total</Box>
                <Box
                  sx={{
                    fontSize: '12px',
                    fontWeight: 500,
                    color: '#374151',
                    width: '80px',
                  }}
                >
                  {total > 0 ? units.total.value.toFixed(1) + (units?.total?.unit || '') : '-'}
                </Box>
              </Box>
              <Box display={'flex'} gap='15px' alignItems={'center'} mt={'4px'}>
                <Box sx={styles.keys}>Usage</Box>
                <Box sx={styles.values}>
                  {usage > 0 ? units.usage.value.toFixed(1) + (units?.usage?.unit || '') : '-'}
                  <span>{usage > 0 && total > 0 ? '(' + ((usage / total) * 100)?.toFixed() + '%)' : '-'}</span>
                </Box>
              </Box>
              <Box display={'flex'} gap='15px' alignItems={'center'} mt={'4px'}>
                <Box sx={styles.keys}>Request</Box>
                <Box sx={styles.values}>
                  {request > 0 ? units.request.value.toFixed(1) + (units?.request?.unit || '') : '-'}{' '}
                  <span>{request > 0 && total > 0 ? '(' + ((request / total) * 100)?.toFixed() + '%)' : '-'}</span>
                </Box>
              </Box>
              <Box display={'flex'} gap='15px' alignItems={'center'} mt={'4px'}>
                <Box sx={styles.keys}>Limit</Box>
                <Box sx={styles.values}>
                  {limit > 0 ? units.limit.value.toFixed(1) + (units?.limit?.unit || '') : '-'}{' '}
                  <span>{limit > 0 && total > 0 ? '(' + ((limit / total) * 100)?.toFixed() + '%)' : '-'}</span>
                </Box>
              </Box>
              {showUsage && (
                <Box display={'flex'} gap='15px' alignItems={'center'} mt={'4px'}>
                  <Box sx={styles.keys}>P50 Usage</Box>
                  <Box sx={styles.values}>
                    {p50usage > 0 ? units.p50usage.value?.toFixed(1) + (units.p50usage?.unit || '') : '-'}{' '}
                    <span>{p50usage > 0 && total > 0 ? '(' + ((p50usage / total) * 100).toFixed() + '%)' : '-'}</span>
                  </Box>
                </Box>
              )}
              {showUsage && (
                <Box display={'flex'} gap='15px' alignItems={'center'} mt={'4px'}>
                  <Box sx={styles.keys}>P90 Usage</Box>
                  <Box sx={styles.values}>
                    {p90usage > 0 ? units.p90usage.value?.toFixed(1) + (units?.p90usage?.unit || '') : '-'}{' '}
                    <span>{p90usage > 0 && total > 0 ? '(' + ((p90usage / total) * 100).toFixed() + '%)' : '-'}</span>
                  </Box>
                </Box>
              )}
              {showUsage && (
                <Box display={'flex'} gap='15px' alignItems={'center'} mt={'4px'}>
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
              top: '-16px',
              left: '-80px',
              width: clusterSummary ? '120px' : '85px',
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
                valueLabel: { style: { fontSize: '30px', fontWeight: 500, fill: '#374151', textShadow: 'none', transform: 'translateY(-20px)' } },
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
                width: '99px',
                height: '72px',
                borderRadius: '50%',
                cursor: 'pointer',
              }}
              onMouseEnter={() => setShowTooltip(true)}
              onMouseLeave={() => setShowTooltip(false)}
            />
            <Typography sx={{ position: 'absolute', bottom: '12px', left: '99px', color: '#B9B9B9', fontSize: '12px', fontWeight: 400 }}>
              Usage
            </Typography>
            {requiredTooltip && (
              <ClusterCustomTooltip showTooltip={showTooltip} usage={usage} available={total} limit={limit} request={request} title={title} />
            )}
          </Box>
          {showUpdatedUi ? (
            <Box sx={{ marginTop: '20px' }}>
              {unit && (
                <Typography position={'relative'} right={'-59px'} fontSize={'10px'} color={'#B9B9B9'}>
                  {unit}
                </Typography>
              )}
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography sx={{ color: '#737373', fontSize: '11px', fontWeight: 500, alignItems: 'end', minWidth: '56px' }}>Total:</Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: '65px', justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative' }}>
                    <Typography sx={{ color: '#374151', fontSize: '11px', fontWeight: 500 }}>
                      {total > 0 ? units.total.value.toFixed(1) + (units?.total?.unit || '') : ''}
                    </Typography>
                  </Box>
                </Box>
              </Box>
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography sx={{ color: '#737373', fontSize: '11px', fontWeight: 500, alignItems: 'end', minWidth: '56px' }}>Usage: </Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: '65px', justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'row' }}>
                    <Typography sx={{ color: '#374151', fontSize: '11px', fontWeight: 500, mr: '4px' }}>
                      {usage > 0 ? units.usage.value.toFixed(1) + (units?.usage?.unit || '') : '-'}
                    </Typography>
                    <Typography sx={{ color: '#939393', fontSize: '11px', fontWeight: 400 }}>
                      {usage > 0 && total > 0 ? '(' + ((usage / total) * 100)?.toFixed() + '%' + ')' : '-'}
                    </Typography>
                  </Box>
                </Box>
              </Box>
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography sx={{ color: '#737373', fontSize: '11px', fontWeight: 500, alignItems: 'end', minWidth: '56px' }}>Limit: </Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: '65px', justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'row' }}>
                    <Typography sx={{ color: '#374151', fontSize: '11px', fontWeight: 500, mr: '4px' }}>
                      {limit > 0 ? units.limit.value.toFixed(1) + (units?.limit?.unit || '') : '-'}
                    </Typography>

                    <Typography sx={{ color: '#939393', fontSize: '11px', fontWeight: 400 }}>
                      {limit > 0 && total > 0 ? '(' + ((limit / total) * 100)?.toFixed() + '%' + ')' : '-'}
                    </Typography>
                  </Box>
                </Box>
              </Box>
              <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
                <Box display={'flex'} alignItems={'center'} gap={1}>
                  <Typography sx={{ color: '#737373', fontSize: '11px', fontWeight: 500, alignItems: 'end', minWidth: '56px' }}>Request: </Typography>
                </Box>
                <Box sx={{ display: 'flex', minWidth: '65px', justifyContent: 'space-between' }}>
                  <Box sx={{ position: 'relative', display: 'flex', flexDirection: 'row' }}>
                    <Typography sx={{ color: '#374151', fontSize: '11px', fontWeight: 500, mr: '4px' }}>
                      {request > 0 ? units.request.value.toFixed(1) + (units?.request?.unit || '') : '-'}
                    </Typography>
                    <Typography sx={{ color: '#939393', fontSize: '11px', fontWeight: 400 }}>
                      {request > 0 && total > 0 ? '(' + ((request / total) * 100)?.toFixed() + '%' + ')' : '-'}
                    </Typography>
                  </Box>
                </Box>
              </Box>
            </Box>
          ) : (
            <Box sx={{ display: 'flex', gap: '2px', marginTop: '36px', flexDirection: 'column', textAlign: 'left' }}>
              <Box sx={{ width: '52px' }}>
                <Typography sx={{ color: '#737373', fontSize: '14px', fontWeight: 500, alignItems: 'end' }}>Usage</Typography>
              </Box>
              <Box sx={{ display: 'flex', gap: '2px', alignItems: 'center' }}>
                <Box sx={{ position: 'relative', textAlign: 'right' }}>
                  <Typography sx={{ color: '#374151', fontSize: '18px', fontWeight: 500 }}>
                    {usage > 0 ? units.usage.value.toFixed(1) + (units?.usage?.unit || '') : '-'}
                  </Typography>
                </Box>
                <Box sx={{ marginLeft: '3px' }}>
                  <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 500 }}>
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
