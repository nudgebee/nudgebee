import { CPUIcon, FlashIcon, LatencyClockIcon, MemoryCardIcon, RocketIcon, StashIcon, SLOInspectionBlackIcon } from '@assets';
import { Box, Typography } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import { Divider } from '@components1/ds/Divider';
import { Link } from '@components1/ds/Link';
import { formatBytes, formatSeconds, truncateText, type ApplicationStats } from 'src/utils/common';
import MonitoringCustomTooltip from './MonitoringCustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import { ds } from '@utils/colors';

interface KubernetesMonitoringCardProps {
  data: ApplicationStats;
}

const styles = {
  listItemWidth: { width: '100%', maxWidth: ds.space.mul(0, 92), pr: 'var(--ds-space-1)' },
  listItem: {
    display: 'flex',
    justifyContent: 'space-between',
    p: 'var(--ds-space-1) var(--ds-space-1) var(--ds-space-1) 0px',
    width: '100%',
  },
  iconContainer: {
    mr: 'var(--ds-space-1)',
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
          return ds.red[500];
        }
      }
    }
    if (type == 'cpu') {
      if (data?.cpu && data?.maxCPUReq) {
        const cpuPercentage = (data.cpu / data.maxCPUReq) * 100;
        if (cpuPercentage < 20 || cpuPercentage > 90) {
          return ds.red[500];
        }
      }
    }
    return ds.red[500];
  };

  return (
    <Box
      className='monitoringCard'
      sx={{
        background: 'var(--ds-background-100)',
        m: 'var(--ds-space-1)',
        height: ds.space.mul(0, 77),
        borderRadius: '0px 0px var(--ds-radius-sm) var(--ds-radius-sm)',
        borderTop: '4px solid',
        borderColor: (data?.nevents ?? 0) > 0 ? ds.red[400] : ds.green[400],
        p: 'var(--ds-space-2) var(--ds-space-3)',
        boxShadow: (data?.nevents ?? 0) > 0 ? '0px 2px 7px 0px #FF8D8DB2' : '',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'justify-between',
      }}
    >
      <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: 'var(--ds-space-2)' }}>
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          <Tooltip title={data.name}>
            <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)', lineHeight: '18px' }}>
              {truncateText(data.name, 27)}
            </Typography>
          </Tooltip>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-400)' }}>
            cl: <Link href={`/kubernetes/details/${data.accountId}`}>{data.accountName}</Link> | ns: {data.namespace}
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end' }}>
          <Tooltip title={'Replica'}>
            <Box
              sx={{
                background: data?.readyPods == data?.totalPods ? ds.gray[400] : ds.red[400],
                height: ds.space[4],
                borderRadius: 'var(--ds-radius-sm)',
                display: 'flex',
                alignItems: 'center',
                p: 'var(--ds-space-1) var(--ds-space-1)',
              }}
            >
              <SafeIcon src={StashIcon} alt='stash' width={14} height={14} />
              <Typography
                sx={{
                  paddingLeft: 'var(--ds-space-1)',
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  color: 'var(--ds-background-100)',
                }}
              >{`${data?.readyPods} / ${data?.totalPods}`}</Typography>
            </Box>
          </Tooltip>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-400)' }}>
            {data?.nrequests ?? '-'} req
          </Typography>
        </Box>
      </Box>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--ds-space-1)' }}>
        <Box sx={styles.listItemWidth} className='cardItem'>
          <MonitoringCustomTooltip rows={cpuData} type='cpu'>
            <Box sx={styles.listItem}>
              <Typography
                sx={{
                  color: 'var(--ds-brand-300)',
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  flex: 1,
                  display: 'flex',
                  alignItems: 'center',
                }}
              >
                <Box sx={styles.iconContainer}>
                  <SafeIcon src={CPUIcon} alt='cpu icon' width={14} height={14} />
                </Box>
                cpu
              </Typography>
              <Box display={'flex'}>
                <Typography
                  sx={{
                    color: 'var(--ds-brand-300)',
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    flex: 1,
                    mr: 'var(--ds-space-1)',
                  }}
                >
                  p99:{' '}
                </Typography>
                <Typography
                  sx={{
                    color: getColor(data, 'cpu'),
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    cursor: 'pointer',
                  }}
                >
                  {`${data?.cpu ? data.cpu : '--'}/${data?.maxCPUReq ? data.maxCPUReq : '--'}`}
                </Typography>
              </Box>
            </Box>
          </MonitoringCustomTooltip>
          <Divider />
          <Box sx={styles.listItem}>
            <Typography
              sx={{
                color: 'var(--ds-brand-300)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                flex: 1,
                display: 'flex',
                alignItems: 'center',
              }}
            >
              <Box sx={styles.iconContainer}>
                <SafeIcon src={LatencyClockIcon} alt='latency' width={14} height={14} />
              </Box>
              latency
            </Typography>
            <Box display={'flex'}>
              <Typography
                sx={{
                  color: 'var(--ds-brand-300)',
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  flex: 1,
                  mr: 'var(--ds-space-1)',
                }}
              >
                p99:{' '}
              </Typography>

              <Typography sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                <div title={data?.latency ? data.latency + 's' : '--'}>{data?.latency ? formatSeconds(data.latency) : '--'}</div>
              </Typography>
            </Box>
          </Box>
          <Divider />
          <Box sx={styles.listItem}>
            <Typography
              sx={{
                color: 'var(--ds-brand-300)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                flex: 1,
                display: 'flex',
                alignItems: 'center',
              }}
            >
              <Box sx={styles.iconContainer}>
                <SafeIcon src={RocketIcon} alt='rocket' width={14} height={14} />
              </Box>
              optimize
            </Typography>
            <Box display={'flex'}>
              <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {data.optimize ? (
                  <Link target='_blank' href={`/kubernetes/details/${data.accountId}?accountId=${data.accountId}#optimize/right-sizing`}>
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
            <Typography
              sx={{
                color: 'var(--ds-brand-300)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                flex: 1,
                display: 'flex',
                alignItems: 'center',
              }}
            >
              <Box sx={styles.iconContainer}>
                <SafeIcon src={FlashIcon} alt='flash' width={14} height={14} />
              </Box>
              events
            </Typography>
            <Box display={'flex'}>
              <Typography
                sx={{
                  color: 'var(--ds-gray-600)',
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  mr: 'var(--ds-space-1)',
                }}
              >
                {data.nevents ? (
                  <Link target='_blank' href={`/kubernetes/details/${data.accountId}?accountId=${data.accountId}#events`}>
                    {data.nevents}
                  </Link>
                ) : (
                  '-'
                )}
              </Typography>
              <Typography
                sx={{
                  color: 'var(--ds-brand-300)',
                  fontSize: 'var(--ds-text-small)',
                  mr: 'var(--ds-space-1)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                }}
              >
                {data.pod_error_count}
              </Typography>
              <Typography sx={{ color: 'var(--ds-brand-300)', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-small)' }}>
                {data.application_error_count}
              </Typography>
            </Box>
          </Box>
          <Divider />
          <MonitoringCustomTooltip rows={memoryData} type='memory'>
            <Box sx={styles.listItem}>
              <Typography
                sx={{
                  color: 'var(--ds-brand-300)',
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  flex: 1,
                  display: 'flex',
                  alignItems: 'center',
                }}
              >
                <Box sx={styles.iconContainer}>
                  <SafeIcon src={MemoryCardIcon} alt='memory icon' width={14} height={14} />
                </Box>
                mem
              </Typography>
              <Box display={'flex'} alignItems={'center'}>
                <Typography
                  sx={{
                    color: 'var(--ds-brand-300)',
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    flex: 1,
                    mr: 'var(--ds-space-1)',
                  }}
                >
                  p99:{' '}
                </Typography>
                <Typography
                  sx={{
                    color: getColor(data, 'memory'),
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    cursor: 'pointer',
                  }}
                >
                  {`${data?.memoryp99 ? formatBytes(data.memoryp99, false) : '--'}/${
                    data?.maxMemoryReq ? formatBytes(data.maxMemoryReq, false) : '--'
                  }`}
                </Typography>
              </Box>
            </Box>
          </MonitoringCustomTooltip>
          <Divider />
          <Box sx={styles.listItem}>
            <Typography
              sx={{
                color: 'var(--ds-brand-300)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                flex: 1,
                display: 'flex',
                alignItems: 'center',
              }}
            >
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
                  color: 'var(--ds-gray-600)',
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                }}
              >
                {data?.failed_slo_count ?? '-'}/{data?.total_slo_count ?? '-'}
              </Typography>

              <Typography sx={{ color: 'var(--ds-red-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }} />
            </Box>
          </Box>
        </Box>
      </Box>
    </Box>
  );
};

export default KubernetesMonitoringCard;
