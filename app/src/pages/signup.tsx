import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import * as React from 'react';
import Head from 'next/head';
import { colors } from 'src/utils/colors';
import { StarsIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { isOnPremMode } from '@lib/license';

const FALLBACK_LOGO = '/branding/default/logo.svg';
import 'swiper/css/bundle';
import { Button, FormControl, FormHelperText, OutlinedInput } from '@mui/material';
import Link from 'next/link';
import { AuthTemplate } from './signin';
import { useRouter } from 'next/router';

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

export default function Register({ isOnPrem }: any) {
  const branding = useBrandingConfig();
  const [helperText, setHelperText] = React.useState('');
  const [helperTextFullName, setHelperTextFullName] = React.useState('');
  const [helperTextOrgName, setHelperTextOrgName] = React.useState('');
  const [isFormDisabled, setIsFormDisabled] = React.useState(false);

  const [email, setEmail] = React.useState('');
  const [fullname, setFullname] = React.useState('');
  const [orgname, setOrgname] = React.useState('');
  const router = useRouter();

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

  function handleMagicLink(data: any) {
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

    const disaplayNameError = verifyDisplayName(data.fullname);
    if (disaplayNameError) {
      setHelperTextFullName(disaplayNameError);
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

  if (isOnPrem) {
    return (
      <BaseTemplate>
        <Box display={'flex'} flexDirection={'row'} justifyContent='space-between' alignItems='flex-end'>
          <Typography fontSize={'28px'} fontWeight={'600'} color={'#374151'}>
            Sign up
          </Typography>
          <Typography fontSize={'14px'}>
            <Link href={'/signin'} style={{ color: 'gray' }}>
              Signin?
            </Link>
          </Typography>
        </Box>
        <br />
        <Typography fontSize={'14px'} color={'#374151'}>
          This is an on-premise version. Please contact your administrator to create an account.
        </Typography>
      </BaseTemplate>
    );
  }

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
          <Typography fontSize={'14px'} color={'#737373'} fontFamily={'Roboto'} mb='10px'>
            Already have an account?
            <Link href={'/signin'} style={{ color: '#3B82F6', paddingLeft: '5px', textDecoration: 'none' }}>
              Sign in
            </Link>
          </Typography>
        </Box>
      </Box>
      <br />
      <div>
        <FormControl
          sx={{
            borderRadius: '4px',
            boxShadow: 0,
            color: colors.text.signinDark,
            borderWidth: '0.5px',
            borderColor: '#D0D0D0',
            fontSize: '12px',
            fontWeight: 600,
            width: '100%',
            mb: '10px',
            '& .MuiInputBase-root input': {
              padding: '12px',
            },
          }}
          variant='outlined'
          disabled={isFormDisabled}
        >
          <Typography
            component='label'
            htmlFor='outlined-adornment-email'
            sx={{
              display: 'block',
              mb: 1,
              fontSize: '14px',
              fontWeight: 500,
              textAlign: 'left',
              fontFamily: 'Roboto',
              color: '#374151',
            }}
          >
            Business Email
          </Typography>
          <OutlinedInput
            id='outlined-adornment-email'
            type='text'
            notched={false}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            label='*Business Email'
            placeholder='Enter Email'
            sx={{
              height: '44px',
              '& input::placeholder': {
                fontSize: '14px',
                opacity: 0.5,
                color: 'grey.600',
              },
            }}
          />
          {helperText && (
            <FormHelperText error id='email-error'>
              {helperText}
            </FormHelperText>
          )}
        </FormControl>
        <Box bgcolor={'#EFF6FF'} p={'8px 14px'} borderRadius={'4px'} mb={2}>
          <Typography display={'flex'} alignItems={'center'} gap={1} fontSize={'12px'} color={'#374151'}>
            <SafeIcon src={StarsIcon} alt='stars' />
            We will send a verification email to your email address.
          </Typography>
        </Box>
        <FormControl
          sx={{
            borderRadius: '4px',
            boxShadow: 0,
            color: colors.text.signinDark,
            borderWidth: '0.5px',
            borderColor: '#D0D0D0',
            fontSize: '12px',
            fontWeight: 600,
            width: '100%',
            mb: '16px',
            '& .MuiInputBase-root input': {
              padding: '12px',
            },
          }}
          variant='outlined'
          disabled={isFormDisabled}
        >
          <Typography
            component='label'
            htmlFor='outlined-adornment-fullname'
            sx={{
              display: 'block',
              mb: 1,
              fontSize: '14px',
              fontWeight: 500,
              textAlign: 'left',
              fontFamily: 'Roboto',
              color: '#374151',
            }}
          >
            Full Name
          </Typography>
          <OutlinedInput
            id='outlined-adornment-fullname'
            notched={false}
            value={fullname}
            onChange={(e) => setFullname(e.target.value)}
            type={'text'}
            placeholder='Enter Full Name'
            sx={{
              height: '44px',
              '& input::placeholder': {
                fontSize: '14px',
                opacity: 0.5,
                color: 'grey.600',
              },
            }}
          />
          {helperTextFullName && (
            <FormHelperText error id='fullname-error'>
              {helperTextFullName}
            </FormHelperText>
          )}
        </FormControl>

        <FormControl
          sx={{
            borderRadius: '4px',
            boxShadow: 0,
            color: colors.text.signinDark,
            borderWidth: '0.5px',
            borderColor: '#D0D0D0',
            fontSize: '12px',
            fontWeight: 600,
            width: '100%',
            mb: '16px',
            '& .MuiInputBase-root input': {
              padding: '12px',
            },
          }}
          variant='outlined'
          disabled={isFormDisabled}
        >
          <Typography
            component='label'
            htmlFor='outlined-adornment-orgname'
            sx={{
              display: 'block',
              mb: 1,
              fontSize: '14px',
              fontWeight: 500,
              textAlign: 'left',
              fontFamily: 'Roboto',
              color: '#374151',
            }}
          >
            Organization Name
          </Typography>
          <OutlinedInput
            id='outlined-adornment-orgname'
            notched={false}
            value={orgname}
            onChange={(e) => setOrgname(e.target.value)}
            type={'text'}
            placeholder='Enter Organization Name'
            sx={{
              height: '44px',
              '& input::placeholder': {
                fontSize: '14px',
                opacity: 0.5,
                color: 'grey.600',
              },
            }}
          />
          {helperTextOrgName && (
            <FormHelperText error id='orgname-error'>
              {helperTextOrgName}
            </FormHelperText>
          )}
        </FormControl>
        <FormControl sx={{ width: '100%', textAlign: 'center' }}>
          <Button
            variant='contained'
            id='credsSubmit'
            disabled={isFormDisabled}
            onClick={(ev) => {
              ev.preventDefault();
              handleMagicLink({
                email,
                fullname,
                orgname,
              });
            }}
            sx={{
              borderRadius: '4px',
              boxShadow: 0,
              backgroundColor: colors.background.brandButton,
              color: '#374151',
              fontSize: '14px',
              fontWeight: 600,
              textTransform: 'none',
              fontFamily: 'Roboto',
              ':hover': {
                backgroundColor: colors.background.brandButtonHover, // theme.palette.primary.main
                color: '#1B2D4A',
                boxShadow: 0,
              },
              width: '100%',
              p: '10px 12px',
            }}
          >
            Sign Up
          </Button>
        </FormControl>
      </div>
    </BaseTemplate>
  );
}

export async function getServerSideProps(_context: any) {
  return {
    props: { isOnPrem: isOnPremMode() },
  };
}
