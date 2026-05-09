import { CPUIcon, FlashIcon, LatencyClockIcon, MemoryCardIcon, RocketIcon, StashIcon, SLOInspectionBlackIcon } from '@assets';
import { Box, Divider, Tooltip, Typography } from '@mui/material';
import Link from 'next/link';
import { formatBytes, formatSeconds, truncateText, type ApplicationStats } from 'src/utils/common';
import MonitoringCustomTooltip from './MonitoringCustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';

interface KubernetesMonitoringCardProps {
  data: ApplicationStats;
}

const styles = {
  listItemWidth: { width: '100%', maxWidth: '185px', pr: '5px' },
  listItem: {
    display: 'flex',
    justifyContent: 'space-between',
    p: '6px 4px 6px 0px',
    width: '100%',
  },
  iconContainer: {
    mr: '4px',
    display: 'flex',
    alignItems: 'center',
  },
};

const KubernetesMonitoringCard: React.FC<KubernetesMonitoringCardProps> = ({ data }) => {
  function createData(matrics: string, data: any) {
    return { matrics, data };
  }

  const cpuData = [
    createData('Request', data?.maxCPUReq ?? '-'),
    createData('Limit', data?.max_cpu_limit ?? '-'),
    createData('p50', data?.cpu_p50 ?? '-'),
    createData('p99', data?.cpu ?? '-'),
    createData('Max', data?.maxCPUReq ?? '-'),
  ];
  const memoryData = [
    createData('Request', data?.maxMemoryReq ? formatBytes(data?.maxMemoryReq, false) : '-'),
    createData('Limit', data?.max_memory_limit ? formatBytes(data?.max_memory_limit, false) : '-'),
    createData('p50', data?.memory_p50 ? formatBytes(data.memory_p50, false) : '-'),
    createData('p99', data?.memoryp99 ? formatBytes(data.memoryp99, false) : '-'),
    createData('Max', data?.maxMemoryUsage ? formatBytes(data.maxMemoryUsage, false) : '-'),
  ];

  const getColor = (data: any, type: string) => {
    if (type == 'memory') {
      if (data?.memoryp99 && data?.maxMemoryReq) {
        const memPercentage = (data.memoryp99 / data.maxMemoryReq) * 100;
        if (memPercentage < 20 || memPercentage > 90) {
          return '#EF4444';
        }
      }
    }
    if (type == 'cpu') {
      if (data?.cpu && data?.maxCPUReq) {
        const cpuPercentage = (data.cpu / data.maxCPUReq) * 100;
        if (cpuPercentage < 20 || cpuPercentage > 90) {
          return '#EF4444';
        }
      }
    }
    return '#EF4444';
  };

  return (
    <Box
      className='monitoringCard'
      sx={{
        background: '#fff',
        m: '6px',
        height: '155px',
        borderRadius: '0px 0px 4px 4px',
        borderTop: '4px solid',
        borderColor: (data?.nevents ?? 0) > 0 ? '#F87171' : '#4ADE80',
        p: '10px 12px',
        boxShadow: (data?.nevents ?? 0) > 0 ? '0px 2px 7px 0px #FF8D8DB2' : '',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'justify-between',
      }}
    >
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: '8px' }}>
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          <Tooltip title={data.name}>
            <Typography sx={{ fontSize: '16px', fontWeight: 500, lineHeight: '18px' }}>{truncateText(data.name, 27)}</Typography>
          </Tooltip>
          <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>
            cl:{' '}
            <Link href={`/kubernetes/details/${data.accountId}`} className='link'>
              {data.accountName}
            </Link>{' '}
            | ns: {data.namespace}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end' }}>
          <Tooltip title={'Replica'}>
            <Box
              sx={{
                background: data?.readyPods == data?.totalPods ? '#9F9F9F' : '#F87171',
                height: '16px',
                borderRadius: '2px',
                display: 'flex',
                alignItems: 'center',
                p: '2px 5px',
              }}
            >
              <SafeIcon src={StashIcon} alt='stash' width={14} height={14} />
              <Typography
                sx={{ paddingLeft: '4px', fontSize: '12px', fontWeight: 400, color: '#FFF' }}
              >{`${data?.readyPods} / ${data?.totalPods}`}</Typography>
            </Box>
          </Tooltip>
          <Typography sx={{ fontSize: '12px', fontWeight: 400, color: '#9F9F9F' }}>{data?.nrequests ?? '-'} req</Typography>
        </Box>
      </Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', gap: '5px' }}>
        <Box sx={styles.listItemWidth} className='cardItem'>
          <MonitoringCustomTooltip rows={cpuData} type='cpu'>
            <Box sx={styles.listItem}>
              <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, display: 'flex', alignItems: 'center' }}>
                <Box sx={styles.iconContainer}>
                  <SafeIcon src={CPUIcon} alt='cpu icon' width={14} height={14} />
                </Box>
                cpu
              </Typography>
              <Box display={'flex'}>
                <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, mr: '4px' }}>p99: </Typography>
                <Typography sx={{ color: getColor(data, 'cpu'), fontSize: '12px', fontWeight: 600, cursor: 'pointer' }}>
                  {`${data?.cpu ? data.cpu : '--'}/${data?.maxCPUReq ? data.maxCPUReq : '--'}`}
                </Typography>
              </Box>
            </Box>
          </MonitoringCustomTooltip>
          <Divider />
          <Box sx={styles.listItem}>
            <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, display: 'flex', alignItems: 'center' }}>
              <Box sx={styles.iconContainer}>
                <SafeIcon src={LatencyClockIcon} alt='latency' width={14} height={14} />
              </Box>
              latency
            </Typography>
            <Box display={'flex'}>
              <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, mr: '4px' }}>p99: </Typography>

              <Typography sx={{ color: '#EF4444', fontSize: '12px', fontWeight: 600 }}>
                <div title={data?.latency ? data.latency + 's' : '--'}>{data?.latency ? formatSeconds(data.latency) : '--'}</div>
              </Typography>
            </Box>
          </Box>
          <Divider />
          <Box sx={styles.listItem}>
            <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, display: 'flex', alignItems: 'center' }}>
              <Box sx={styles.iconContainer}>
                <SafeIcon src={RocketIcon} alt='rocket' width={14} height={14} />
              </Box>
              optimize
            </Typography>
            <Box display={'flex'}>
              <Typography sx={{ color: '#737373', fontSize: '12px', fontWeight: 600 }}>
                {data.optimize ? (
                  <Link
                    rel='noopener noreferrer'
                    target='_blank'
                    href={`/kubernetes/details/${data.accountId}?accountId=${data.accountId}#optimize/right-sizing`}
                    className='link'
                  >
                    {data.optimize}
                  </Link>
                ) : (
                  '-'
                )}
              </Typography>
            </Box>
          </Box>
        </Box>
        <Divider orientation='vertical' />
        <Box sx={styles.listItemWidth} className='cardItem'>
          <Box sx={styles.listItem}>
            <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, display: 'flex', alignItems: 'center' }}>
              <Box sx={styles.iconContainer}>
                <SafeIcon src={FlashIcon} alt='flash' width={14} height={14} />
              </Box>
              events
            </Typography>
            <Box display={'flex'}>
              <Typography sx={{ color: '#737373', fontSize: '12px', fontWeight: 600, mr: '4px' }}>
                {data.nevents ? (
                  <Link
                    rel='noopener noreferrer'
                    target='_blank'
                    href={`/kubernetes/details/${data.accountId}?accountId=${data.accountId}#events`}
                    className='link'
                  >
                    {data.nevents}
                  </Link>
                ) : (
                  '-'
                )}
              </Typography>
              <Typography sx={{ color: '#B9B9B9', fontSize: '12px', mr: '4px', fontWeight: '800' }}>{data.pod_error_count}</Typography>
              <Typography sx={{ color: '#B9B9B9', fontWeight: '800', fontSize: '12px' }}>{data.application_error_count}</Typography>
            </Box>
          </Box>
          <Divider />
          <MonitoringCustomTooltip rows={memoryData} type='memory'>
            <Box sx={styles.listItem}>
              <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, display: 'flex', alignItems: 'center' }}>
                <Box sx={styles.iconContainer}>
                  <SafeIcon src={MemoryCardIcon} alt='memory icon' width={14} height={14} />
                </Box>
                mem
              </Typography>
              <Box display={'flex'} alignItems={'center'}>
                <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, mr: '4px' }}>p99: </Typography>
                <Typography sx={{ color: getColor(data, 'memory'), fontSize: '12px', fontWeight: 600, cursor: 'pointer' }}>
                  {`${data?.memoryp99 ? formatBytes(data.memoryp99, false) : '--'}/${
                    data?.maxMemoryReq ? formatBytes(data.maxMemoryReq, false) : '--'
                  }`}
                </Typography>
              </Box>
            </Box>
          </MonitoringCustomTooltip>
          <Divider />
          <Box sx={styles.listItem}>
            <Typography sx={{ color: '#B9B9B9', fontSize: '12px', fontWeight: 400, flex: 1, display: 'flex', alignItems: 'center' }}>
              <Box sx={styles.iconContainer}>
                <SafeIcon
                  src={SLOInspectionBlackIcon}
                  alt='sla clock'
                  width={14}
                  height={14}
                  style={{
                    filter: 'brightness(0) saturate(100%) invert(45%) sepia(0%) saturate(0%) hue-rotate(136deg) brightness(95%) contrast(89%)',
                  }}
                />
              </Box>
              slo
            </Typography>
            <Box display={'flex'}>
              <Typography
                sx={{
                  color: '#737373',
                  fontSize: '12px',
                  fontWeight: 600,
                }}
              >
                {data?.failed_slo_count ?? '-'}/{data?.total_slo_count ?? '-'}
              </Typography>

              <Typography sx={{ color: '#EF4444', fontSize: '12px', fontWeight: 400 }} />
            </Box>
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default KubernetesMonitoringCard;
