import { Box, Typography } from '@mui/material';
import React from 'react';
import { ds } from '@utils/colors';
import { Button as DsButton } from '@components1/ds/Button';
import { useRouter } from 'next/router';
import CopyButton from '@common-new/CopyButton';
import eksIcon from '@assets/amazon-eks-icon.svg';
import SafeIcon from '@components1/common/SafeIcon';
import Datetime from '@common-new/format/Datetime';
import { KeyboardArrowDownRounded } from '@mui/icons-material';
import { Link } from '@components1/ds/Link';

const KuberneteTitleBox = ({ rightComponent, marginTop = ds.space[5], marginBottom = ds.space[4], kubernete = {}, isWorkloadpage = false }) => {
  const navigate = useRouter();

  return (
    <Box
      sx={{
        position: 'relative',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        backgroundColor: 'white',
        p: 'var(--ds-space-3) var(--ds-space-4) var(--ds-space-3) var(--ds-space-6)',
        boxShadow: '0px 4px 4px 0px #0000001A',
        overflow: 'hidden',
        mt: marginTop,
        mb: marginBottom,
        borderRadius: 'var(--ds-radius-xl)',
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
          width: ds.space.mul(0, 7),
        }}
      />

      <Box display='flex' alignItems='flex-end' justifyContent='space-between' flexGrow={1} gap={ds.space[2]}>
        <Box display='flex' flexDirection='column' gap={ds.space[2]}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',

              '& img, & svg': {
                width: ds.space.mul(0, 10),
                height: ds.space[5],
                marginLeft: 'var(--ds-space-4)',
              },
              '& button': { marginLeft: 'var(--ds-space-4)' },
            }}
          >
            <Typography
              variant='h5'
              sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-600)' }}
            >
              {kubernete?.account_name}
            </Typography>
            {!isWorkloadpage && <SafeIcon alt='' src={eksIcon} />}
          </Box>

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
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <Typography fontSize={ds.text.bodyLg}>ID: {kubernete?.id}</Typography>
              <CopyButton text={kubernete?.id} size='sm' />
            </Box>
            <div className='line' />
            <Typography fontSize={ds.text.bodyLg}>Connected: </Typography>
            <Link href={`/agentHealth?accountId=${kubernete?.id}#agent`}>
              <Datetime value={kubernete?.agents?.[0]?.last_synced_at} />
            </Link>
            <div className='line' />
            <Typography fontSize={ds.text.bodyLg}>Status: {kubernete?.status}</Typography>
          </Box>
        </Box>

        <DsButton
          tone='secondary'
          size='sm'
          icon={<KeyboardArrowDownRounded />}
          iconPlacement='end'
          onClick={() => (isWorkloadpage ? navigate(-1) : navigate.push('/kubernetes'))}
        >
          Change {isWorkloadpage ? 'Workload' : 'Cluster'}
        </DsButton>
      </Box>

      {!!rightComponent && rightComponent}
    </Box>
  );
};

export default KuberneteTitleBox;
