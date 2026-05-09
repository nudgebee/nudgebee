import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import * as React from 'react';
import Head from 'next/head';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';
import { NBIconSignIn } from '@assets';
import { Button } from '@mui/material';
import CustomTextField from 'src/components1/common/CustomTextField';
import Link from 'next/link';
import { useRouter } from 'next/router';
import { AuthTemplateV2 } from '@components/auth/AuthTemplateV2';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { isOnPremMode } from '@lib/license';

export default function SignUpV2({ isOnPrem }: any) {
  const router = useRouter();
  const { title: brandTitle, logoUrl: brandLogoUrl, signinImageUrl: brandSigninImageUrl, loading: brandingLoading } = useBrandingConfig();
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
          <title>Nudgebee: Signup</title>
          <meta property='og:title' content={`NUDGEBEE - Signup`} key='title' />
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
          <Box sx={{ textAlign: 'center', mb: '40px' }}>
            <Typography
              sx={{
                fontSize: '28px',
                fontWeight: 600,
                color: '#1F2937',
                fontFamily: 'Poppins, sans-serif',
                mb: 1,
                letterSpacing: -1,
              }}
            >
              Sign up
            </Typography>
            <Typography
              sx={{
                fontSize: '14px',
                color: '#6B7280',
                fontFamily: 'Roboto, sans-serif',
              }}
            >
              Already have an account?{' '}
              <Link href='/signin' style={{ color: '#3B82F6', textDecoration: 'none', fontWeight: 500 }}>
                Sign in
              </Link>
            </Typography>
          </Box>

          <Typography
            sx={{
              fontSize: '14px',
              color: '#374151',
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
        <title>Nudgebee: Signup</title>
        <meta property='og:title' content={`NUDGEBEE - Signup`} key='title' />
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
        <Box sx={{ textAlign: 'center', mb: '40px' }}>
          <Typography
            sx={{
              fontSize: '28px',
              fontWeight: 600,
              color: '#1F2937',
              fontFamily: 'Poppins, sans-serif',
              mb: 0.5,
              letterSpacing: -1,
            }}
          >
            Create your account
          </Typography>
          <Typography
            sx={{
              fontSize: '14px',
              color: '#6B7280',
              fontFamily: 'Roboto, sans-serif',
            }}
          >
            Already have an account?{' '}
            <Link href='/signin' style={{ color: '#3B82F6', textDecoration: 'none', fontWeight: 500 }}>
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
            sx={{ mb: 2 }}
          />

          {/* Info Box */}
          <Box
            sx={{
              backgroundColor: '#EFF6FF',
              borderRadius: '8px',
              padding: '12px 16px',
              mb: 3,
              display: 'flex',
              alignItems: 'center',
              gap: 1,
            }}
          >
            <Typography
              sx={{
                fontSize: '12px',
                color: '#374151',
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
            sx={{ mb: 2 }}
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
            sx={{ mb: 3 }}
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
              borderRadius: '8px',
              backgroundColor: colors.background.brandButton,
              color: '#374151',
              fontSize: '14px',
              fontWeight: 600,
              textTransform: 'none',
              fontFamily: 'Roboto, sans-serif',
              padding: '12px',
              boxShadow: 'none',
              '&:hover': {
                backgroundColor: colors.background.brandButtonHover,
                boxShadow: 'none',
              },
              '&:disabled': {
                backgroundColor: '#E5E7EB',
                color: '#9CA3AF',
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
  return {
    props: { isOnPrem: isOnPremMode() },
  };
}
