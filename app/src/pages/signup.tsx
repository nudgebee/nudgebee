import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import * as React from 'react';
import Head from 'next/head';
import { ds } from 'src/utils/colors';
import SafeIcon from '@shared/icons/SafeIcon';
import { NBIconSignIn } from '@assets';
import { Button } from '@mui/material';
import CustomTextField from '@shared/forms/CustomTextField';
import Link from 'next/link';
import { useRouter } from 'next/router';
import { AuthTemplateV2 } from '@components/auth/AuthTemplateV2';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { getLicenseDetails } from '@lib/license';

export default function SignUpV2({ isOnPrem }: any) {
  const router = useRouter();
  const {
    title: brandTitle,
    logoUrl: brandLogoUrl,
    signinImageUrl: brandSigninImageUrl,
    loading: brandingLoading,
    faviconUrl: brandFaviconUrl,
  } = useBrandingConfig();
  const isCustomBranding = brandLogoUrl && brandLogoUrl !== '/branding/default/logo.svg';
  const signinLogo = brandSigninImageUrl || (isCustomBranding ? brandLogoUrl : NBIconSignIn);

  // Form state
  const [helperText, setHelperText] = React.useState('');
  const [helperTextFullName, setHelperTextFullName] = React.useState('');
  const [helperTextOrgName, setHelperTextOrgName] = React.useState('');
  const [isFormDisabled, setIsFormDisabled] = React.useState(false);

  const [email, setEmail] = React.useState('');
  const [fullname, setFullname] = React.useState('');
  const [orgname, setOrgname] = React.useState('');

  function verifyDisplayName(displayName: string) {
    if (!displayName) {
      return 'Display Name required';
    }

    // should start with alphabet, can have spaces & min length 3 char & max 30 char
    // should not ends with space
    const displayNamePattern = /^[a-zA-Z][a-zA-Z\s]{1,28}[a-zA-Z]$/;
    if (!displayNamePattern.test(displayName)) {
      return 'Display Name is invalid (should start with alphabet, can have spaces & min length 3 char & max 30 char)';
    }

    return '';
  }

  function verifyOrgName(orgName: string) {
    if (!orgName) {
      return 'Organization Name is required';
    }
    // should start with alphabet, can have spaces & min length 3 char & max 30 char
    const displayNamePattern = /^[a-zA-Z][a-zA-Z0-9\s]{1,28}[a-zA-Z0-9]$/;
    if (!displayNamePattern.test(orgName)) {
      return 'Organization Name is invalid (should start with alphabet, can have spaces & min length 3 char & max 30 char)';
    }

    return '';
  }

  function handleSignUp(data: any) {
    setHelperText('');
    setHelperTextFullName('');
    setHelperTextOrgName('');
    setIsFormDisabled(true);

    const emailPattern = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
    if (!emailPattern.test(data.email)) {
      setHelperText('Please enter a valid email address');
      setIsFormDisabled(false);
      return;
    }

    const displayNameError = verifyDisplayName(data.fullname);
    if (displayNameError) {
      setHelperTextFullName(displayNameError);
      setIsFormDisabled(false);
      return;
    }

    const orgNameError = verifyOrgName(data.orgname);
    if (orgNameError) {
      setHelperTextOrgName(orgNameError);
      setIsFormDisabled(false);
      return;
    }

    fetch('/api/auth/signup', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(data),
    })
      .then(async (res) => {
        const resJson: any = await res.json();
        if (res.status === 200) {
          setEmail('');
          setFullname('');
          setOrgname('');
          router.push('/ready');
          setIsFormDisabled(false);
        } else {
          setHelperText(resJson.message);
          setIsFormDisabled(false);
        }
      })
      .catch(() => {
        setHelperText('Something went wrong. Please try again.');
        setIsFormDisabled(false);
      });
  }

  // Handle on-premise version
  if (isOnPrem) {
    return (
      <>
        <Head>
          {!brandingLoading && brandFaviconUrl && <link rel='icon' href={brandFaviconUrl} />}
          <title>{brandTitle || 'Nudgebee'}: Signup</title>
          <meta property='og:title' content={`${brandTitle || 'Nudgebee'} - Signup`} key='title' />
        </Head>

        <AuthTemplateV2>
          {/* Logo - rendered after branding resolves to show tenant logo */}
          {!brandingLoading && (
            <Box
              sx={{
                display: 'flex',
                justifyContent: 'center',
              }}
            >
              <SafeIcon src={signinLogo} width={280} height={90} alt={`${brandTitle} logo`} style={{ objectFit: 'contain' }} />
            </Box>
          )}

          {/* Welcome Text */}
          <Box sx={{ textAlign: 'center', mb: 'var(--ds-space-6)' }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-display)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-foreground)',
                fontFamily: 'Poppins, sans-serif',
                mb: ds.space[2],
                letterSpacing: -1,
              }}
            >
              Sign up
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-gray-600)',
                fontFamily: 'Roboto, sans-serif',
              }}
            >
              Already have an account?{' '}
              <Link href='/signin' style={{ color: 'var(--ds-blue-500)', textDecoration: 'none', fontWeight: 'var(--ds-font-weight-medium)' }}>
                Sign in
              </Link>
            </Typography>
          </Box>

          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              color: 'var(--ds-brand-500)',
              fontFamily: 'Roboto, sans-serif',
              textAlign: 'center',
            }}
          >
            This is an on-premise version. Please contact your administrator to create an account.
          </Typography>
        </AuthTemplateV2>
      </>
    );
  }

  return (
    <>
      <Head>
        {!brandingLoading && brandFaviconUrl && <link rel='icon' href={brandFaviconUrl} />}
        <title>{brandTitle || 'Nudgebee'}: Signup</title>
        <meta property='og:title' content={`${brandTitle || 'Nudgebee'} - Signup`} key='title' />
      </Head>

      <AuthTemplateV2>
        {/* Logo - rendered after branding resolves to show tenant logo */}
        {!brandingLoading && (
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'center',
            }}
          >
            <SafeIcon src={signinLogo} width={280} height={90} alt={`${brandTitle} logo`} style={{ objectFit: 'contain' }} />
          </Box>
        )}

        {/* Welcome Text */}
        <Box sx={{ textAlign: 'center', mb: 'var(--ds-space-6)' }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-display)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-foreground)',
              fontFamily: 'Poppins, sans-serif',
              mb: ds.space[1],
              letterSpacing: -1,
            }}
          >
            Create your account
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              color: 'var(--ds-gray-600)',
              fontFamily: 'Roboto, sans-serif',
            }}
          >
            Already have an account?{' '}
            <Link href='/signin' style={{ color: 'var(--ds-blue-500)', textDecoration: 'none', fontWeight: 'var(--ds-font-weight-medium)' }}>
              Sign in
            </Link>
          </Typography>
        </Box>

        {/* Sign Up Form */}
        <Box
          sx={{
            animation: 'fadeIn 0.3s ease-out',
            '@keyframes fadeIn': {
              from: { opacity: 0 },
              to: { opacity: 1 },
            },
          }}
        >
          {/* Business Email Field */}
          <CustomTextField
            label='Business Email'
            placeholder='Enter your business email'
            helperText={helperText}
            error={!!helperText}
            id='email'
            value={email}
            disabled={isFormDisabled}
            onChange={(ev) => {
              setEmail(ev.target.value);
              if (ev.target.value?.length === 0) {
                setHelperText('');
              }
            }}
            sx={{ mb: ds.space[4] }}
          />

          {/* Info Box */}
          <Box
            sx={{
              backgroundColor: 'var(--ds-blue-100)',
              borderRadius: ds.radius.lg,
              padding: `${ds.space[3]} ${ds.space[4]}`,
              mb: ds.space[5],
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[2],
            }}
          >
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: 'var(--ds-brand-500)',
                fontFamily: 'Roboto, sans-serif',
              }}
            >
              ✨ We will send a verification email to your email address.
            </Typography>
          </Box>

          {/* Full Name Field */}
          <CustomTextField
            label='Full Name'
            placeholder='Enter your full name'
            helperText={helperTextFullName}
            error={!!helperTextFullName}
            id='fullname'
            value={fullname}
            disabled={isFormDisabled}
            onChange={(ev) => {
              setFullname(ev.target.value);
              if (ev.target.value) {
                setHelperTextFullName('');
              }
            }}
            sx={{ mb: ds.space[4] }}
          />

          {/* Organization Name Field */}
          <CustomTextField
            label='Organization Name'
            placeholder='Enter your organization name'
            helperText={helperTextOrgName}
            error={!!helperTextOrgName}
            id='orgname'
            value={orgname}
            disabled={isFormDisabled}
            onChange={(ev) => {
              setOrgname(ev.target.value);
              if (ev.target.value) {
                setHelperTextOrgName('');
              }
            }}
            sx={{ mb: ds.space[5] }}
          />

          {/* Sign Up Button */}
          <Button
            variant='contained'
            fullWidth
            disabled={isFormDisabled}
            onClick={(ev) => {
              ev.preventDefault();
              handleSignUp({
                email,
                fullname,
                orgname,
              });
            }}
            sx={{
              borderRadius: ds.radius.lg,
              backgroundColor: ds.yellow[500],
              color: 'var(--ds-brand-500)',
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              textTransform: 'none',
              fontFamily: 'Roboto, sans-serif',
              padding: ds.space[3],
              boxShadow: 'none',
              '&:hover': {
                backgroundColor: ds.yellow[600],
                boxShadow: 'none',
              },
              '&:disabled': {
                backgroundColor: 'var(--ds-brand-150)',
                color: 'var(--ds-gray-400)',
              },
            }}
          >
            Sign Up
          </Button>
        </Box>
      </AuthTemplateV2>
    </>
  );
}

export async function getServerSideProps(_context: any) {
  // Self-signup is gated on the canonical license tier from services-server,
  // not on env-var heuristics (which can drift from the license endpoint
  // when NUDGEBEE_LICENSE is set on the app but services-server reports a
  // different tier). On fetch failure, fail closed → treat as on-prem.
  let isOnPrem = true;
  try {
    const license = await getLicenseDetails();
    isOnPrem = license.tier !== 'saas';
  } catch {
    isOnPrem = true;
  }
  return {
    props: { isOnPrem },
  };
}
