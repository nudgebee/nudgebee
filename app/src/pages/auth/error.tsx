import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Typography from '@mui/material/Typography';
import Head from 'next/head';
import { useRouter } from 'next/router';
import { useMemo } from 'react';

import SafeIcon from '@shared/icons/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { AuthTemplateV2 as AuthTemplate } from '@components/auth/AuthTemplateV2';
import { ds } from '@utils/colors';

const FALLBACK_LOGO = '/branding/default/logo.svg';

// NextAuth's built-in error keys (`AccessDenied`, `Verification`, `Configuration`,
// `Default`) plus the message strings thrown by our Credentials providers and the
// signIn callback ([...nextauth].ts). Anything not in this map falls through to
// the generic title/message at the bottom.
const ERROR_COPY: Record<string, { title: string; message: string }> = {
  AccessDenied: {
    title: 'Access denied',
    message: 'You do not have permission to sign in. Contact your administrator if you believe this is a mistake.',
  },
  NO_TENANT_ACCESS: {
    title: 'No tenant access',
    message: 'Your account is not provisioned for this workspace yet. Ask your administrator to invite you, then try again.',
  },
  Verification: {
    title: 'Sign-in link expired',
    message: 'This sign-in link is no longer valid. It may have already been used or expired. Request a new one and try again.',
  },
  EmailSignin: {
    title: 'Could not send sign-in email',
    message:
      'We were not able to deliver your sign-in link. The mail service may be unreachable or your address may be invalid. Please try again in a moment, or contact your administrator if this keeps happening.',
  },
  Configuration: {
    title: 'Sign-in is misconfigured',
    message: 'The authentication provider is misconfigured. Please contact your administrator.',
  },
  CredentialsSignin: {
    title: 'Sign-in failed',
    message: 'The credentials you entered did not match our records. Double-check and try again.',
  },
  'Invalid Credentials': {
    title: 'Invalid credentials',
    message: 'The username or password is incorrect.',
  },
  'Invalid Username': {
    title: 'Invalid username',
    message: 'The username you entered is not valid.',
  },
  'Invalid Password': {
    title: 'Invalid password',
    message: 'The password you entered is not valid.',
  },
  'Invalid Email format': {
    title: 'Invalid email',
    message: 'Enter a valid email address and try again.',
  },
  'User Account is suspended': {
    title: 'Account suspended',
    message: 'This account has been suspended. Please contact your administrator.',
  },
  Default: {
    title: 'Something went wrong',
    message: 'We could not complete sign-in. Please try again, or contact your administrator if this keeps happening.',
  },
};

export default function AuthError() {
  const router = useRouter();
  const branding = useBrandingConfig();

  const { title, message } = useMemo(() => {
    const raw = router.query.error;
    const key = Array.isArray(raw) ? raw[0] : raw ?? '';
    return ERROR_COPY[key] ?? ERROR_COPY.Default;
  }, [router.query.error]);

  return (
    <>
      <Head>
        {!branding.loading && <link rel='icon' href={branding.faviconUrl} />}
        <title>{`${branding.title}: ${title}`}</title>
        <meta property='og:title' content={`${branding.title} - ${title}`} key='title' />
      </Head>
      <AuthTemplate>
        <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='flex-start'>
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
              {title}
            </Typography>
          </Box>
        </Box>
        <Box bgcolor={ds.red[100]} p={`${ds.space.mul(0, 5)} ${ds.space.mul(0, 7)}`} borderRadius={ds.radius.sm} mb={ds.space[4]}>
          <Typography fontSize={'var(--ds-text-body)'} color={ds.red[700]} lineHeight={1.5} textAlign='left'>
            {message}
          </Typography>
        </Box>
        <Button
          data-testid='auth-error-sign-in-btn'
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
      </AuthTemplate>
    </>
  );
}
