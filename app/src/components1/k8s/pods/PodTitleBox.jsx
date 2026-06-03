import { Box, Typography } from '@mui/material';
import { Button as DsButton } from '@components1/ds/Button';
import React from 'react';
import { ds } from 'src/utils/colors';
import { useRouter } from 'next/router';
import CopyButton from '@common-new/CopyButton';
import KubernetesPodDebugger from '@components1/k8s/details/KubernetesPodDebugger';
import PropTypes from 'prop-types';
import TerminalIcon from '@assets/terminal.svg';
import SafeIcon from '@components1/common/SafeIcon';

const PodTitleBox = ({ rightComponent, marginBottom = ds.space[4], pod = {} }) => {
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
        borderRadius: 'var(--ds-radius-xl)',
        p: 'var(--ds-space-3) var(--ds-space-4) var(--ds-space-3) var(--ds-space-5)',
        boxShadow: '0px 4px 8px 0px #00000008',
        overflow: 'hidden',
        mt: 'var(--ds-space-4)',
        mb: marginBottom,
      }}
    >
      <Box
        sx={{
          left: 0,
          top: 0,
          position: 'absolute',
          display: 'flex',
          backgroundColor: ds.blue[300],
          height: '100%',
          width: ds.space[2],
        }}
      />

      <Box display='flex' flexDirection='column' gap={ds.space.mul(0, 3)}>
        <Typography
          variant='h5'
          sx={{
            fontSize: 'var(--ds-text-heading)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-brand-500)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-1)',
          }}
        >
          <Box component='span' sx={{ color: ds.gray[400], fontWeight: 'var(--ds-font-weight-medium)' }}>
            Pod name:
          </Box>
          {podData?.name}
          <CopyButton text={podData?.name} size='sm' />
        </Typography>
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-3)',
            color: 'var(--ds-brand-600)',
            '& .line': {
              width: '1px',
              height: ds.space[4],
              backgroundColor: 'var(--ds-gray-600)',
            },
          }}
        >
          <Typography fontSize={ds.text.small} sx={{ display: 'flex', gap: 'var(--ds-space-1)' }}>
            <Box component='span' sx={{ color: ds.gray[400] }}>
              ID:
            </Box>
            <Box component='span' sx={{ color: ds.brand[500], display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              {podData?.id}
              <CopyButton text={podData?.id} size='sm' />
            </Box>
          </Typography>
          <Box className='line' sx={{ opacity: 0.4 }} />
          <Typography fontSize={ds.text.small} sx={{ display: 'flex', gap: 'var(--ds-space-1)' }}>
            <Box component='span' sx={{ color: ds.gray[400] }}>
              Last seen:
            </Box>
            <Box component='span' sx={{ color: ds.brand[500] }}>
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
      <DsButton
        tone='ghost'
        size='sm'
        composition='icon-only'
        aria-label='Open Pod Debugger'
        tooltip='Open Pod Debugger'
        onClick={handleClickOpen}
        icon={<SafeIcon priority src={TerminalIcon} alt='container-debug-connection' />}
      />

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
