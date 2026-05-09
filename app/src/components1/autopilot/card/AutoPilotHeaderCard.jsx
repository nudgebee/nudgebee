import React from 'react';
import { Box, Divider, Typography } from '@mui/material';
import { useData } from '@context/DataContext';
import Currency from '@components1/common/format/Currency';
import PropTypes from 'prop-types';

const TextWithValue = ({ title, value, valueSize = '12px', valueColor = '#737373', direction = 'row', updatedCard = false, sx = {} }) => {
  return (
    <Box sx={{ ...sx, display: 'flex', flexDirection: direction, alignItems: 'baseline' }}>
      <Typography sx={{ fontSize: '12px', fontWeight: 400, color: updatedCard ? '#9F9F9F' : '#737373', marginRight: '8px' }} className='title'>
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

const AutoPilotHeaderCard = ({ header = '-', data = {}, children, updatedCard = true }) => {
  const { selectedCluster } = useData();
  const cloudResource = data?.data?.cloud_resourse ?? data?.data?.resource;
  const workloadName = cloudResource?.meta?.controller ?? cloudResource?.name;
  const clusterName = selectedCluster?.cluster_name ?? selectedCluster?.account_name ?? data?.clusterName ?? '-';
  return (
    <Box sx={{ display: 'flex', gap: updatedCard ? '22px' : '52px', flexDirection: 'column' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: updatedCard ? '2.5fr 0.5fr' : '1fr', gap: '10px' }}>
        <Box
          sx={{
            width: 'auto',
            minHeight: '88px',
            borderRadius: '6px',
            padding: '12px 16px',
            background: '#FFFFFF',
            border: updatedCard && '0.5px solid #60A5FA',
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
                  <TextWithValue title='Workload' value={workloadName} valueSize='14px' valueColor='#374151' direction='column' />
                  <Box>
                    <TextWithValue title='Cluster' value={clusterName} valueSize='14px' valueColor='#374151' />
                    <TextWithValue
                      title='Namespace'
                      value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.resource?.meta?.namespace}
                      valueSize='14px'
                      valueColor='#374151'
                    />
                    {data?.containerName && <TextWithValue title='Container' value={data.containerName} valueSize='14px' valueColor='#374151' />}
                  </Box>
                </Box>
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />
              <Box>
                <TextWithValue
                  title='Pods'
                  value={data?.data?.cloud_resourse?.meta?.total_pods ?? data?.data?.resource?.meta?.total_pods}
                  valueSize='14px'
                  valueColor='#374151'
                />
                <TextWithValue
                  title='Kind'
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.resource?.meta?.controllerKind}
                  valueSize='14px'
                  valueColor='#374151'
                />
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />
            </Box>
          )}
          {updatedCard && (
            <Box sx={{ display: 'flex', justifyContent: 'space-between', width: '100%' }}>
              <TextWithValue
                title='Workload'
                value={workloadName}
                valueSize='16px'
                valueColor='#374151'
                direction='column'
                updatedCard={updatedCard}
              />
              <Divider orientation='vertical' sx={{ height: '60px' }} />
              <Box sx={{ gap: '4px', display: 'flex' }}>
                <Box>
                  <TextWithValue
                    title='Cluster'
                    value={clusterName}
                    valueSize='12px'
                    valueColor='#374151'
                    sx={{
                      '& .title': {
                        width: '90px',
                      },
                    }}
                  />
                  <TextWithValue
                    title='Namespace'
                    value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.resource?.meta?.namespace}
                    valueSize='14px'
                    valueColor='#374151'
                    sx={{
                      '& .title': {
                        width: '90px',
                      },
                    }}
                  />
                  {data?.containerName && (
                    <TextWithValue
                      title='Container'
                      value={data.containerName}
                      valueSize='14px'
                      valueColor='#374151'
                      sx={{
                        '& .title': {
                          width: '90px',
                        },
                      }}
                    />
                  )}
                </Box>
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />

              <Box>
                <TextWithValue
                  title='Pods'
                  value={data?.data?.cloud_resourse?.meta?.total_pods ?? data?.data?.resource?.meta?.total_pods}
                  valueSize='14px'
                  valueColor='#374151'
                  sx={{
                    '& .title': {
                      width: '90px',
                    },
                  }}
                />
                <TextWithValue
                  title='Kind'
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.resource?.meta?.controllerKind}
                  valueSize='14px'
                  valueColor='#374151'
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
              background: '#FFFFFF',
              border: '0.5px solid #4ADE80',
              boxShadow: '0px 2px 7px 0px #22C55E0F, 0px 4px 6px -1px #22C55E1F',
            }}
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', height: '100%' }}>
              <Typography sx={{ fontSize: '12px', color: '#9F9F9F', fontWeight: 400, textAlign: 'right' }}>Savings</Typography>
              <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Currency
                  value={data.saving}
                  precison={1}
                  sx={{
                    color: '#22C55E',
                    fontSize: '24px',
                    fontWeight: 500,
                  }}
                  sxSuffix={{
                    color: '#9F9F9F',
                    fontSize: '12px',
                    fontWeight: 400,
                  }}
                  sxPrefix={{
                    color: '#9F9F9F',
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
        <Box sx={{ borderRadius: '4px 4px 0px 0px', borderTop: '1px solid #DBEAFE)', background: '#EFF6FF', padding: '8px 16px' }}>
          <Typography sx={{ color: '#374151', fontSize: '16px', fontWeight: 600 }}>{header}</Typography>
        </Box>
      )}
    </Box>
  );
};

export default AutoPilotHeaderCard;

AutoPilotHeaderCard.propTypes = {
  header: PropTypes.any,
  data: PropTypes.any,
  children: PropTypes.any,
  updatedCard: PropTypes.bool,
};
