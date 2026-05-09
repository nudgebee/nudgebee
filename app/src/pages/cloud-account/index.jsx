import { useData } from '@context/DataContext';
import { useRouter } from 'next/router';
import { useEffect, useState } from 'react';
import apiHome from '@api1/home';
import { Box, CircularProgress, Typography, Stack } from '@mui/material';
import { CloudAccountIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import CustomButton from '@components1/common/NewCustomButton';
import { hasWriteAccess } from '@lib/auth';

const CloudAccount = () => {
  const router = useRouter();
  const { selectedCluster, setSelectedCluster } = useData();
  const [loading, setLoading] = useState(true);
  const [showNoAccountsMessage, setShowNoAccountsMessage] = useState(false);

  useEffect(() => {
    const navigateToCloudAccount = async () => {
      if (selectedCluster?.value && selectedCluster?.cloud_provider !== 'K8s') {
        setSelectedCluster({ ...selectedCluster });
        setTimeout(() => {
          router.push(`/cloud-account/details/${selectedCluster.value}#summary`);
        }, 100);
        return;
      }

      try {
        const cloudAccounts = await apiHome.getCloudAccounts();
        const validCloudAccounts = cloudAccounts.filter((acc) => acc.cloud_provider !== 'K8s' && acc.status === 'active');

        if (validCloudAccounts.length > 0) {
          const firstCloudAccount = validCloudAccounts[0];
          const newClusterData = {
            cluster_name: firstCloudAccount.account_name,
            cluster_id: firstCloudAccount.id,
            cloud_provider: firstCloudAccount.cloud_provider,
            status: firstCloudAccount.status,
          };
          setSelectedCluster(newClusterData);
          await router.push(`/cloud-account/details/${firstCloudAccount.id}#summary`);
        } else {
          // Show message instead of immediate redirect
          setLoading(false);
          setShowNoAccountsMessage(true);
        }
      } catch (error) {
        console.error(error);
        setLoading(false);
        setShowNoAccountsMessage(true);
      }
    };

    navigateToCloudAccount();
  }, [router.query.accountId]);

  const handleIntegrateClick = () => {
    router.push('/user-management#integrations');
  };

  if (loading) {
    return (
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          minHeight: '70vh',
        }}
      >
        <CircularProgress />
      </Box>
    );
  }

  if (showNoAccountsMessage) {
    return (
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '50px 32px',
          borderRadius: '12px',
          border: '1px solid #E4E4E4',
          background: '#FFF',
          mt: 2,
        }}
      >
        <Box
          sx={{
            width: 64,
            height: 64,
            borderRadius: '16px',
            background: 'linear-gradient(135deg, #EBF2FF 0%, #DBEAFE 100%)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            mb: 3,
            boxShadow: '0px 1px 3px rgba(16, 24, 40, 0.06)',
            '& img': {
              filter: 'brightness(0) saturate(100%) invert(33%) sepia(93%) saturate(1752%) hue-rotate(213deg) brightness(97%) contrast(93%)',
            },
          }}
        >
          <SafeIcon src={CloudAccountIcon} alt='Cloud Account' width={36} height={36} />
        </Box>

        <Typography sx={{ fontSize: '18px', fontWeight: 600, color: '#101828', mb: 1, fontFamily: 'Poppins' }}>
          Get started with Cloud monitoring
        </Typography>
        <Typography sx={{ fontSize: '14px', color: '#667085', mb: 4, textAlign: 'center', maxWidth: '460px', lineHeight: 1.6 }}>
          Connect your cloud account to start monitoring and optimizing your infrastructure with real-time visibility and actionable insights.
        </Typography>

        <Stack direction='row' spacing={4} sx={{ mb: 4 }}>
          {[
            {
              label: 'Resource monitoring',
              icon: 'M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z',
            },
            {
              label: 'Security insights',
              icon: 'M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z',
            },
            {
              label: 'Cost optimization',
              icon: 'M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
            },
          ].map((item) => (
            <Box key={item.label} sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Box
                sx={{
                  width: 32,
                  height: 32,
                  borderRadius: '8px',
                  background: '#F5F8FF',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  flexShrink: 0,
                }}
              >
                <svg
                  width='16'
                  height='16'
                  viewBox='0 0 24 24'
                  fill='none'
                  stroke='#2563EB'
                  strokeWidth='1.5'
                  strokeLinecap='round'
                  strokeLinejoin='round'
                >
                  <path d={item.icon} />
                </svg>
              </Box>
              <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#344054', whiteSpace: 'nowrap' }}>{item.label}</Typography>
            </Box>
          ))}
        </Stack>

        {hasWriteAccess() ? (
          <CustomButton variant='primary' size='Medium' text='Connect Cloud Account' onClick={handleIntegrateClick} />
        ) : (
          <Typography sx={{ fontSize: '13px', color: '#667085', fontStyle: 'italic' }}>Need admin permission to connect a cloud account</Typography>
        )}
      </Box>
    );
  }

  return null;
};

export default CloudAccount;
