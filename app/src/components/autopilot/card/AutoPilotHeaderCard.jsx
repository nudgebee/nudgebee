import React from 'react';
import { Box, Typography } from '@mui/material';
import { Divider } from '@ui/Divider';
import { useData } from '@context/DataContext';
import Currency from '@shared/format/Currency';
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
            minHeight: ds.space.mul(1, 22),
            borderRadius: ds.radius.md,
            padding: `${ds.space[3]} ${ds.space[4]}`,
            background: ds.background[100],
            border: updatedCard && `0.5px solid ${ds.blue[400]}`,
            boxShadow: updatedCard
              ? `0px ${ds.space[0]} 7px 0px color-mix(in srgb, ${ds.blue[500]} 6%, transparent), 0px ${ds.space[1]} ${ds.space.mul(
                  0,
                  3
                )} -1px color-mix(in srgb, ${ds.blue[500]} 12%, transparent)`
              : `0px 0px ${ds.space.mul(0, 3)} -1px color-mix(in srgb, ${ds.blue[500]} 40%, transparent), 0px ${ds.space[0]} 10.5px ${ds.space.mul(
                  0,
                  -1
                )} ${ds.gray.alpha[100]}`,
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
              <Divider orientation='vertical' sx={{ height: ds.space.mul(1, 15) }} />
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
              <Divider orientation='vertical' sx={{ height: ds.space.mul(1, 15) }} />
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
              <Divider orientation='vertical' sx={{ height: ds.space.mul(1, 15) }} />
              <Box sx={{ gap: ds.space[1], display: 'flex' }}>
                <Box>
                  <TextWithValue
                    title='Cluster'
                    value={clusterName}
                    valueSize={ds.text.small}
                    valueColor={ds.gray[700]}
                    sx={{
                      '& .title': {
                        width: ds.space.mul(1, 22.5),
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
                        width: ds.space.mul(1, 22.5),
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
                          width: ds.space.mul(1, 22.5),
                        },
                      }}
                    />
                  )}
                </Box>
              </Box>
              <Divider orientation='vertical' sx={{ height: ds.space.mul(1, 15) }} />

              <Box>
                <TextWithValue
                  title='Pods'
                  value={data?.data?.cloud_resourse?.meta?.total_pods ?? data?.data?.resource?.meta?.total_pods}
                  valueSize={ds.text.body}
                  valueColor={ds.gray[700]}
                  sx={{
                    '& .title': {
                      width: ds.space.mul(1, 22.5),
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
                      width: ds.space.mul(1, 22.5),
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
              minHeight: ds.space.mul(1, 22),
              borderRadius: ds.radius.md,
              padding: `${ds.space[3]} ${ds.space[4]}`,
              background: ds.background[100],
              border: `0.5px solid ${ds.green[400]}`,
              boxShadow: `0px ${ds.space[0]} 7px 0px color-mix(in srgb, ${ds.green[500]} 6%, transparent), 0px ${ds.space[1]} ${ds.space.mul(
                0,
                3
              )} -1px color-mix(in srgb, ${ds.green[500]} 12%, transparent)`,
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
                    fontSize: ds.text.display,
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
