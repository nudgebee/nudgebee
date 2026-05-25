import { Box, Typography, IconButton, Tooltip } from '@mui/material';
import React from 'react';
import { colors } from 'src/utils/colors';
import { useRouter } from 'next/router';
import CopyableText from '@common/CopyableText';
import KubernetesPodDebugger from '@components1/k8s/details/KubernetesPodDebugger';
import PropTypes from 'prop-types';
import TerminalIcon from '@assets/terminal.svg';
import SafeIcon from '@components1/common/SafeIcon';

const PodTitleBox = ({ rightComponent, marginBottom = '16px', pod = {} }) => {
  const _navigate = useRouter();
  const [open, setOpen] = React.useState(false);

  const handleClickOpen = () => {
    setOpen(true);
  };

  const handleClose = () => {
    setOpen(false);
  };

  const podData = (pod.cloud_resourses || [])[0];

  return (
    <Box
      sx={{
        position: 'relative',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        backgroundColor: 'white',
        borderRadius: '12px',
        p: '14px 20px 14px 28px',
        boxShadow: '0px 4px 8px 0px #00000008',
        overflow: 'hidden',
        mt: '20px',
        mb: marginBottom,
      }}
    >
      <Box
        sx={{
          left: 0,
          top: 0,
          position: 'absolute',
          display: 'flex',
          backgroundColor: colors.background.primaryLight,
          height: '100%',
          width: '8px',
        }}
      />

      <Box display='flex' flexDirection='column' gap={'6px'}>
        <Typography variant='h5' sx={{ fontSize: '20px', fontWeight: 600, color: '#374151', display: 'flex', alignItems: 'center', gap: '4px' }}>
          <Box component='span' sx={{ color: colors.text.secondaryDark, fontWeight: 500 }}>
            Pod name:
          </Box>
          {podData?.name}
          <CopyableText copyableText={podData?.name} />
        </Typography>
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '11px',
            color: '#22304B',
            '& .line': {
              width: '1px',
              height: '17px',
              backgroundColor: '#737373',
            },
          }}
        >
          <Typography fontSize={'12px'} sx={{ display: 'flex', gap: '6px' }}>
            <Box component='span' sx={{ color: colors.text.secondaryDark }}>
              ID:
            </Box>
            <Box component='span' sx={{ color: colors.text.secondary, display: 'flex', alignItems: 'center', gap: '4px' }}>
              {podData?.id}
              <CopyableText copyableText={podData?.id} height='12px' width='12px' />
            </Box>
          </Typography>
          <Box className='line' sx={{ opacity: 0.4 }} />
          <Typography fontSize={'12px'} sx={{ display: 'flex', gap: '6px' }}>
            <Box component='span' sx={{ color: colors.text.secondaryDark }}>
              Last seen:
            </Box>
            <Box component='span' sx={{ color: colors.text.secondary }}>
              {podData?.last_seen
                ? new Date(podData.last_seen)
                    .toLocaleString('en-GB', {
                      hour: '2-digit',
                      minute: '2-digit',
                      second: '2-digit',
                      day: '2-digit',
                      month: 'short',
                      year: 'numeric',
                    })
                    .replace(',', ',')
                : '-'}
            </Box>
          </Typography>
        </Box>
      </Box>
      <Tooltip title='Open Pod Debugger'>
        <IconButton size='small' onClick={handleClickOpen} aria-label='Open Pod Debugger'>
          <SafeIcon priority src={TerminalIcon} alt='container-debug-connection' />
        </IconButton>
      </Tooltip>

      {!!rightComponent && rightComponent}
      {open ? (
        <KubernetesPodDebugger
          accountId={podData?.account}
          debugPodOpen={open}
          selectedPodName={{
            namespace: podData?.meta?.namespace,
            id: podData?.id,
            name: podData?.name,
          }}
          closeDebugPod={handleClose}
        />
      ) : null}
    </Box>
  );
};

PodTitleBox.propTypes = {
  rightComponent: PropTypes.node,
  marginTop: PropTypes.string,
  marginBottom: PropTypes.string,
  pod: PropTypes.object,
  isWorkloadpage: PropTypes.bool,
};

export default PodTitleBox;
