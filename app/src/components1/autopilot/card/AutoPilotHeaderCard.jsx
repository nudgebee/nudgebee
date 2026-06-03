import React from 'react';
import { Box, Divider, Typography } from '@mui/material';
import { useData } from '@context/DataContext';
import Currency from '@components1/common/format/Currency';
import PropTypes from 'prop-types';
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

const AutoPilotHeaderCard = ({ header = '-', data = {}, children, updatedCard = true }) => {
  const { selectedCluster } = useData();
  const cloudResource = data?.data?.cloud_resourse ?? data?.data?.resource;
  const workloadName = cloudResource?.meta?.controller ?? cloudResource?.name;
  const clusterName = selectedCluster?.cluster_name ?? selectedCluster?.account_name ?? data?.clusterName ?? '-';
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
            border: updatedCard && `0.5px solid ${ds.blue[400]}`,
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
                  <TextWithValue title='Workload' value={workloadName} valueSize={ds.text.body} valueColor={ds.gray[700]} direction='column' />
                  <Box>
                    <TextWithValue title='Cluster' value={clusterName} valueSize={ds.text.body} valueColor={ds.gray[700]} />
                    <TextWithValue
                      title='Namespace'
                      value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.resource?.meta?.namespace}
                      valueSize={ds.text.body}
                      valueColor={ds.gray[700]}
                    />
                    {data?.containerName && (
                      <TextWithValue title='Container' value={data.containerName} valueSize={ds.text.body} valueColor={ds.gray[700]} />
                    )}
                  </Box>
                </Box>
              </Box>
              <Divider orientation='vertical' sx={{ height: '60px' }} />
              <Box>
                <TextWithValue
                  title='Pods'
                  value={data?.data?.cloud_resourse?.meta?.total_pods ?? data?.data?.resource?.meta?.total_pods}
                  valueSize={ds.text.body}
                  valueColor={ds.gray[700]}
                />
                <TextWithValue
                  title='Kind'
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.resource?.meta?.controllerKind}
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
                value={workloadName}
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
                    value={clusterName}
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
                    value={data?.data?.cloud_resourse?.meta?.namespace ?? data?.data?.resource?.meta?.namespace}
                    valueSize={ds.text.body}
                    valueColor={ds.gray[700]}
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
                      valueSize={ds.text.body}
                      valueColor={ds.gray[700]}
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
                  value={data?.data?.cloud_resourse?.meta?.controllerKind ?? data?.data?.resource?.meta?.controllerKind}
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
                  value={data.saving}
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
};

export default AutoPilotHeaderCard;

AutoPilotHeaderCard.propTypes = {
  header: PropTypes.any,
  data: PropTypes.any,
  children: PropTypes.any,
  updatedCard: PropTypes.bool,
};
