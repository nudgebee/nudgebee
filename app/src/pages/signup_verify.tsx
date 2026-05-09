import Grid from '@mui/material/Grid';
import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import * as React from 'react';
import Head from 'next/head';
import {
  WelcomeSlideOne,
  // WelcomeSlideTwo,
} from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';

const FALLBACK_LOGO = '/branding/default/logo.svg';
import { Swiper, SwiperSlide } from 'swiper/react';
import 'swiper/css/bundle';

import { Autoplay, EffectFade, Navigation, Pagination } from 'swiper/modules';
import { useRouter } from 'next/router';
import Loader from '@components1/common/Loader';
import Link from 'next/link';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';

const BaseTemplate = ({ children }: { children: React.ReactNode }): React.ReactElement => {
  const branding = useBrandingConfig();

  return (
    <>
      <Head>
        {!branding.loading && <link rel='icon' href={branding.faviconUrl} />}
        <title>{branding.title}: Register</title>
        <meta property='og:title' content={`${branding.title} - Register`} key='title' />
      </Head>
      <Grid container>
        <Grid item sm={12} md={6}>
          <Grid
            sx={{
              background: colors.background.pages,
              color: 'white',
              height: 'calc(100vh - 20px - 20px - 8px)',
              borderRadius: '24px',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              flexGrow: 1,
              padding: '20px',
              border: `1px solid ${colors.border.tertiary}`,
              margin: '24px',
            }}
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            {!branding.loading && (
              <SafeIcon
                src={branding.logoUrl}
                alt={branding.title}
                width={216}
                height={76}
                onError={(e: any) => {
                  (e.target as HTMLImageElement).src = FALLBACK_LOGO;
                }}
                style={{ maxWidth: '216px', maxHeight: '76px', objectFit: 'contain', margin: '40px 0px' }}
              />
            )}
            <Swiper
              effect={'fade'}
              loop={true}
              speed={800}
              fadeEffect={{ crossFade: true }}
              pagination={{ clickable: true }}
              autoplay={{ delay: 3000, disableOnInteraction: false }}
              modules={[Autoplay, EffectFade, Navigation, Pagination]}
              className='mySwiper'
              style={{ height: 'max-content' }}
            >
              <SwiperSlide>
                <SafeIcon src={WelcomeSlideOne} alt='register slide one' />
              </SwiperSlide>
            </Swiper>
          </Grid>
        </Grid>

        <Grid item sm={12} md={6} height={{ md: '100vh', sm: 'auto' }} flexGrow={1} p={'20px'} position={'relative'}>
          <Box
            display='flex'
            alignItems='center'
            justifyContent='center'
            flexDirection='column'
            flexGrow={1}
            height='100%'
            gap={1}
            maxWidth={'360px'}
            margin={'auto'}
          >
            <Box mt={4} textAlign='center'>
              {children}
            </Box>
          </Box>
        </Grid>
      </Grid>
    </>
  );
};
export default function RegisterVerify(): React.ReactElement {
  const router = useRouter();

  const [verificationMessage, setVerificationMessage] = React.useState<string | null>(null);

  const messageMap: Record<string, React.ReactElement> = {
    'invalid token': (
      <Typography fontSize={20}>
        Invalid email verification token. Please click <Link href='/signup'>Here</Link> to register Again.
      </Typography>
    ),
    'token expired': (
      <Typography fontSize={20}>
        Email verification token expired. Please click <Link href='/signup'>Here</Link> to register Again.
      </Typography>
    ),
    'already verified': (
      <Typography fontSize={20}>
        Email verification token is already verified. Please click <Link href='/signin'>Here</Link> to Login.
      </Typography>
    ),
    success: (
      <Typography fontSize={20}>
        Email Validation Successful, Please click <Link href='/signin'>Here</Link> to login.
      </Typography>
    ),
    'internal error': <Typography fontSize={20}>Unable to validate Email, Please connect with support or try again after some time.</Typography>,
  };

  const getVerificationMessageComponent = (message: string): React.ReactElement => {
    message = message.toLowerCase();
    return messageMap[message] || <Typography fontSize={20}>{message}</Typography>;
  };

  React.useEffect(() => {
    if (router.query.token) {
      setVerificationMessage(null);
      fetch('/api/auth/signup_verify', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ token: router.query.token }),
      }).then(async (res) => {
        const resJson: { message: string } = await res.json();
        if (res.status === 200) {
          snackbar.success('User verified successfully');
          setVerificationMessage('success');
          setTimeout(() => {
            router.push('/signin');
          }, 2000);
        } else {
          snackbar.error(resJson.message);
          setVerificationMessage(resJson.message);
        }
      });
    } else {
      setVerificationMessage('invalid token');
    }
  }, [router.query.token]);

  return (
    <BaseTemplate>
      <br />
      <br />
      <Typography textAlign={'center'} fontSize={'28px'} fontWeight={'600'} color={colors.text.secondary}>
        Verifying Registration Token
      </Typography>
      {verificationMessage ? getVerificationMessageComponent(verificationMessage) : <Loader />}
    </BaseTemplate>
  );
}
