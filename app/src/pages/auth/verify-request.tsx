import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Typography from '@mui/material/Typography';
import Head from 'next/head';
import { useRouter } from 'next/router';
import { StarsIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { ds } from '@utils/colors';

const FALLBACK_LOGO = '/branding/default/logo.svg';
import 'swiper/css/bundle';
import { AuthTemplateV2 as AuthTemplate } from '@components/auth/AuthTemplateV2';

const BaseTemplate = ({ children }: any) => {
  const branding = useBrandingConfig();

  return (
    <>
      <Head>
        {!branding.loading && <link rel='icon' href={branding.faviconUrl} />}
        <title>{branding.title}: Check your email</title>
        <meta property='og:title' content={`${branding.title} - Check your email`} key='title' />
      </Head>
      <AuthTemplate>{children}</AuthTemplate>
    </>
  );
};

export default function VerifyRequest() {
  const branding = useBrandingConfig();
  const router = useRouter();

  return (
    <BaseTemplate>
      <Box display={'flex'} flexDirection={'column'} justifyContent='space-between' alignItems='flex-start'>
        <Box
          sx={{
            height: ds.space.mul(4, 5),
            width: '100%',
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            mb: ds.space[1],
            '& img': {
              width: ds.space.mul(2, 8),
              height: ds.space.mul(2, 8),
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
            marginBottom: ds.space[3],
          }}
        >
          <Typography fontSize={'var(--ds-text-display)'} fontWeight={'600'} color={ds.gray[700]} fontFamily={'Roboto'}>
            Check your email
          </Typography>
        </Box>
      </Box>
      <br />
      <div>
        <Box bgcolor={ds.blue[100]} p={`${ds.space[2]} ${ds.space.mul(0, 7)}`} borderRadius={ds.radius.sm} mb={ds.space[4]}>
          <Typography display={'flex'} alignItems={'center'} gap={ds.space[2]} fontSize={'var(--ds-text-small)'} color={ds.gray[700]}>
            <SafeIcon src={StarsIcon} alt='stars' />A sign-in link has been sent to your email address. Please check your inbox or spam folder.
          </Typography>
        </Box>
      </div>
      <Button
        data-testid='verify-request-back-to-sign-in-btn'
        fullWidth
        variant='contained'
        onClick={() => router.replace('/signin')}
        sx={{
          mt: 'var(--ds-space-2)',
          textTransform: 'none',
          backgroundColor: 'var(--ds-brand-700)',
          '&:hover': { backgroundColor: 'var(--ds-brand-700)' },
        }}
      >
        Back to sign in
      </Button>
    </BaseTemplate>
  );
}
