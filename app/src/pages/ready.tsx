import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import * as React from 'react';
import Head from 'next/head';
import { StarsIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';

const FALLBACK_LOGO = '/branding/default/logo.svg';
import 'swiper/css/bundle';
import { AuthTemplate } from './signin';

const BaseTemplate = ({ children }: any) => {
  const branding = useBrandingConfig();

  return (
    <>
      <Head>
        {!branding.loading && <link rel='icon' href={branding.faviconUrl} />}
        <title>{branding.title}: Signup</title>
        <meta property='og:title' content={`${branding.title} - Signup`} key='title' />
      </Head>
      <AuthTemplate>{children}</AuthTemplate>
    </>
  );
};

export default function Ready() {
  const branding = useBrandingConfig();

  return (
    <BaseTemplate>
      <Box display={'flex'} flexDirection={'column'} justifyContent='space-between' alignItems='flex-start'>
        <Box
          sx={{
            height: '80px',
            width: '100%',
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            mb: '4px',
            '& img': {
              width: '64px',
              height: '64px',
            },
          }}
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          {!branding.loading && (
            <SafeIcon
              src={branding.logoUrl}
              alt={branding.title}
              width={64}
              height={64}
              onError={(e: any) => {
                (e.target as HTMLImageElement).src = FALLBACK_LOGO;
              }}
              style={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }}
            />
          )}
        </Box>
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            flexDirection: 'column',
            width: '100%',
            marginBottom: '12px',
          }}
        >
          <Typography fontSize={'32px'} fontWeight={'600'} color={'#374151'} fontFamily={'Roboto'}>
            Welcome to {branding.title}!
          </Typography>
        </Box>
      </Box>
      <br />
      <div>
        <Box bgcolor={'#EFF6FF'} p={'8px 14px'} borderRadius={'4px'} mb={2}>
          <Typography display={'flex'} alignItems={'center'} gap={1} fontSize={'12px'} color={'#374151'}>
            <SafeIcon src={StarsIcon} alt='stars' />
            We have sent you an verification email. Please check your email&apos;s inbox or spam folder
          </Typography>
        </Box>
      </div>
    </BaseTemplate>
  );
}
