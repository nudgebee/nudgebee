import { Box, Typography } from '@mui/material';
import React from 'react';
import colors from '@lib/colors';
import CustomButton from '@common/NewCustomButton';
import { useRouter } from 'next/router';
import CopyableText from '@common/CopyableText';
import eksIcon from '@assets/amazon-eks-icon.svg';
import SafeIcon from '@components1/common/SafeIcon';
import Datetime from '@components1/common/format/Datetime';
import { KeyboardArrowDownRounded } from '@mui/icons-material';
import Link from 'next/link';

const KuberneteTitleBox = ({ rightComponent, marginTop = '24px', marginBottom = '16px', kubernete = {}, isWorkloadpage = false }) => {
  const navigate = useRouter();

  return (
    <Box
      sx={{
        position: 'relative',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        backgroundColor: 'white',
        p: '14px 20px 14px 35px',
        boxShadow: '0px 4px 4px 0px #0000001A',
        overflow: 'hidden',
        mt: marginTop,
        mb: marginBottom,
        borderRadius: '18px',
      }}
    >
      <Box
        sx={{
          left: 0,
          top: 0,
          position: 'absolute',
          display: 'flex',
          backgroundColor: colors.LIGHT_BLUE,
          height: '100%',
          width: '15px',
        }}
      />

      <Box display='flex' alignItems='flex-end' justifyContent='space-between' flexGrow={1} gap={'7.5px'}>
        <Box display='flex' flexDirection='column' gap={'7.5px'}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',

              '& img, & svg': {
                width: '21px',
                height: '24px',
                marginLeft: '20px',
              },
              '& button': { marginLeft: '20px' },
            }}
          >
            <Typography variant='h5' sx={{ fontSize: '20px', fontWeight: 600, color: '#22304B' }}>
              {kubernete?.account_name}
            </Typography>
            {!isWorkloadpage && <SafeIcon alt='' src={eksIcon} />}
          </Box>

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
            <CopyableText copyableText={kubernete?.id}>
              <Typography fontSize={'14px'}>ID: {kubernete?.id}</Typography>
            </CopyableText>
            <div className='line' />
            <Typography fontSize={'14px'}>Connected: </Typography>
            <Link href={`/agentHealth?accountId=${kubernete?.id}#agent`} passHref={true}>
              <Datetime value={kubernete?.agents?.[0]?.last_synced_at} />
            </Link>
            <div className='line' />
            <Typography fontSize={'14px'}>Status: {kubernete?.status}</Typography>
          </Box>
        </Box>

        <CustomButton
          variant='lightButton'
          text={
            <Box component='span' display='flex' alignItems='center' gap={'4px'} sx={{ '& svg': { width: '18px', height: '18px' } }}>
              Change {isWorkloadpage ? 'Workload' : 'Cluster'} <KeyboardArrowDownRounded />
            </Box>
          }
          onClick={() => (isWorkloadpage ? navigate(-1) : navigate.push('/kubernetes'))}
        />
      </Box>

      {!!rightComponent && rightComponent}
    </Box>
  );
};

export default KuberneteTitleBox;
