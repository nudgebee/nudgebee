import { getProviders, signIn } from 'next-auth/react';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import Grid from '@mui/material/Grid';
import Box from '@mui/material/Box';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import * as React from 'react';
import Button from '@mui/material/Button';
import Head from 'next/head';
import { googleAuth, oktaAuth, oneloginAuth, azureAuth, auth0Auth, WelcomeSlide } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { getLicenseDetails } from '@lib/license';
import { renderSlot } from '@lib/slots';

const FALLBACK_LOGO = '/branding/default/logo.svg';
import 'swiper/css/bundle';
import { colors } from 'src/utils/colors';
import { EmailRegEx } from '@lib/validation';

import { Divider, useMediaQuery } from '@mui/material';
import { SignInProviderExtraSlot } from '@common/auth/SignInProviderExtraSlot';
import { useRouter } from 'next/router';

export const AuthTemplate = ({ children }: any) => {
  const mobileView = useMediaQuery('(max-width:768px)');
  const branding = useBrandingConfig();

  return (
    <Grid container>
      {mobileView && (
        <Box sx={{ width: '100%', display: 'flex', justifyContent: 'center' }}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          {!branding.loading && (
            <SafeIcon
              src={branding.logoUrl}
              alt={branding.title}
              width={108}
              height={38}
              onError={(e: any) => {
                (e.target as HTMLImageElement).src = FALLBACK_LOGO;
              }}
              style={{ maxWidth: '108px', maxHeight: '38px', objectFit: 'contain', margin: '24px 0px 0px' }}
            />
          )}
        </Box>
      )}
      <Grid item sm={12} md={7}>
        <Box
          sx={{
            height: 'calc(100vh - 20px - 20px - 8px)',
            borderRadius: '24px',
            display: mobileView ? 'none' : 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            flexGrow: 1,
            margin: '24px',
            background: 'radial-gradient(50% 50% at 50% 50%, #21375A 0%, #18273F 100%)',
            position: 'sticky',

            top: '20px',
            '& .slide-image': {
              maxHeight: '70vh',
              height: '100%',
              width: '100%',
              mt: '160px',
              objectFit: 'contain',
            },
            '& .text-logo': {
              objectFit: 'contain',
              width: '220px',
              maxHeight: '72px',
              top: '40px',
              position: 'absolute',
            },
          }}
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          {!branding?.loading && (
            <SafeIcon
              src={branding?.signinLeftImageUrl || branding?.logoUrl}
              className='text-logo'
              alt={branding?.title}
              width={220}
              height={72}
              onError={(e: any) => {
                (e.target as HTMLImageElement).src = FALLBACK_LOGO;
              }}
            />
          )}
          <SafeIcon src={WelcomeSlide} className='slide-image' alt='register slide one' />
        </Box>
      </Grid>

      <Grid
        item
        sm={12}
        md={5}
        minHeight={{ md: '100vh', sm: 'auto' }}
        display='flex'
        flexDirection='column'
        flexGrow={1}
        p={'24px'}
        position={'relative'}
        mt={mobileView ? '70px' : 'auto'}
        overflow={'auto'}
        sx={{
          '::-webkit-scrollbar': {
            display: 'none',
          },
        }}
      >
        <Box
          display='flex'
          alignItems='center'
          justifyContent='center'
          flexDirection='column'
          flexGrow={1}
          height='100%'
          gap={1}
          maxWidth={'360px'}
          width={'100%'}
          marginX={'auto'}
        >
          <Box className='animated-box' textAlign='center' width={'100%'}>
            {children}
          </Box>
        </Box>
      </Grid>
    </Grid>
  );
};

const BaseTemplate = ({ children }: any) => {
  const branding = useBrandingConfig();

  return (
    <>
      <Head>
        {!branding.loading && <link rel='icon' href={branding.faviconUrl} />}
        <title>{branding.title}: Login</title>
        <meta property='og:title' content={`${branding.title} - Signin`} key='title' />
      </Head>
      <AuthTemplate>{children}</AuthTemplate>
    </>
  );
};

interface AuthProvider {
  id: string;
  name: string;
  displayName: string;
  type: 'icon' | 'magic-link' | 'teleport' | 'credentials' | 'ldap';
  image?: string;
  colors: {
    background: string;
    hoverBackground: string;
    border: string;
    text: string;
  };
  inputs?: {
    username?: boolean;
    password?: boolean;
    email?: boolean;
  };
}

export const authProvidersConfig: AuthProvider[] = [
  {
    id: 'onelogin',
    name: 'OneLogin',
    displayName: 'OneLogin',
    type: 'icon',
    image: oneloginAuth.default.src,
    colors: {
      background: '#ffffff',
      hoverBackground: '#e5e8ed',
      border: '#D0D0D0',
      text: '#344054',
    },
  },
  {
    id: 'auth0',
    name: 'Auth0',
    displayName: 'Auth',
    type: 'icon',
    image: auth0Auth.default.src,
    colors: {
      background: '#ffffff',
      hoverBackground: '#e5e8ed',
      border: '#D0D0D0',
      text: '#344054',
    },
  },
  {
    id: 'azure-ad',
    name: 'Azure Active Directory',
    displayName: 'Azure',
    type: 'icon',
    image: azureAuth.default.src,
    colors: {
      background: '#ffffff',
      hoverBackground: '#e5e8ed',
      border: '#D0D0D0',
      text: '#344054',
    },
  },
  {
    id: 'email',
    name: 'Email',
    displayName: 'Magic Link',
    type: 'magic-link',
    colors: {
      background: '#374151',
      hoverBackground: '#232222',
      border: '#D0D0D0',
      text: '#ffffff',
    },
    inputs: {
      email: true,
    },
  },

  {
    id: 'ldap',
    name: 'LDAP',
    displayName: 'LDAP Credentials',
    type: 'ldap',
    colors: {
      background: colors.background.brandButton,
      hoverBackground: colors.background.brandButtonHover,
      border: '#D0D0D0',
      text: colors.text.signinDark,
    },
    inputs: {
      username: true,
      password: true,
    },
  },
];
export default function SignIn({ providers, samlEnabled, tier }: any) {
  const router = useRouter();
  const branding = useBrandingConfig();
  const [helperText, setHelperText] = React.useState({
    ldapUsername: '',
    ldapPassword: '',
    magicLinkEmail: '',
    credsEmail: '',
    credsPassword: '',
  });
  const [credsUsername, setCredsUsername] = React.useState('');
  const [credsPassword, setCredsPassword] = React.useState('');

  const [ldapCredsUsername, setLdapCredsUsername] = React.useState('');
  const [ldapCredsPassword, setLdapCredsPassword] = React.useState('');
  const [magicLinkEmail, setMagicLinkEmail] = React.useState('');
  const [magicLinkSending, setMagicLinkSending] = React.useState(false);

  React.useEffect(() => {
    setMagicLinkEmail('');
  }, []);

  // Handle NO_TENANT_ACCESS error
  React.useEffect(() => {
    const { error } = router.query;
    if (error && typeof error === 'string' && error.includes('NO_TENANT_ACCESS')) {
      router.push('/no-tenant-access?message=You do not have a tenant assigned. Please contact your administrator for access.');
    }
  }, [router.query, router]);

  const handleLdapSubmit = async (provider: any, ev: any) => {
    ev.preventDefault();

    setHelperText((prev) => ({
      ...prev,
      ldapUsername: '',
      ldapPassword: '',
    }));

    let hasError = false;

    if (!ldapCredsUsername) {
      setHelperText((prev) => ({
        ...prev,
        ldapUsername: 'LDAP username is required.',
      }));
      hasError = true;
    }

    if (!ldapCredsPassword) {
      setHelperText((prev) => ({
        ...prev,
        ldapPassword: 'LDAP password is required.',
      }));
      hasError = true;
    }
    if (hasError) {
      return;
    }
    const result = await signIn(provider.id, { username: ldapCredsUsername.toLowerCase(), password: ldapCredsPassword, redirect: false });
    if (result?.error) {
      setHelperText((prev) => ({
        ...prev,
        ldapPassword: 'Invalid credentials. Please try again.',
      }));
    } else if (result?.url) {
      router.push(result.url);
    }
  };

  function getProviderImage(provider: any) {
    if (provider.name === 'Google') {
      return googleAuth.default.src;
    } else if (provider.name === 'Okta') {
      return oktaAuth.default.src;
    } else if (provider.name === 'OneLogin') {
      return oneloginAuth.default.src;
    } else if (provider.name === 'Auth0') {
      return auth0Auth.default.src;
    } else if (provider.name === 'Azure Active Directory' || provider.name === 'Azure Active Directory B2C') {
      return azureAuth.default.src;
    }
  }

  async function handleMagicLink(value: string, provider: any, ev: any) {
    if (EmailRegEx.test(value)) {
      setMagicLinkSending(true);
      ev.preventDefault();
      const result = await signIn(provider.id, { email: value.toLowerCase(), redirect: false });
      setMagicLinkSending(false);
      if (result?.error) {
        const errorMessage =
          result.error === 'AccessDenied' ? 'Unable to find user.' : 'Unable to send email. Please try again later or contact your administrator.';
        setHelperText((prev) => ({
          ...prev,
          magicLinkEmail: errorMessage,
        }));
      } else if (result?.url) {
        router.push(result.url);
      }
    } else {
      setHelperText((prev) => ({
        ...prev,
        magicLinkEmail: 'Please enter a valid email address.',
      }));
    }
  }

  function handleTeleportLink(provider: any, ev: any) {
    signIn(provider.id, {});
    ev.preventDefault();
  }

  const iconProviders = ['Google', 'Okta', 'OneLogin', 'Auth0', 'Azure Active Directory', 'Azure Active Directory B2C'];

  if (providers == undefined || providers == null || Object.values(providers).length == 0) {
    return (
      <BaseTemplate>
        <Typography variant='h4' sx={{ color: colors.text.signinDark, fontSize: '12px', fontWeight: 600 }}>
          No authentication providers are enabled. Please contact the administrator.
        </Typography>
      </BaseTemplate>
    );
  }

  const nonIconProviders = Object.values(providers).filter((provider: any) => !iconProviders.includes(provider.name));

  const otherProviders = Object.values(providers).filter((provider: any) => iconProviders.includes(provider.name));

  return (
    <BaseTemplate>
      <Box display={'flex'} flexDirection={'column'} alignItems='flex-start'>
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
              src={branding.signinImageUrl || branding.logoUrl}
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
          }}
        >
          <Typography fontSize={'32px'} fontWeight={'600'} color={'#374151'}>
            Welcome back!
          </Typography>
          {renderSlot('SignInBelowTitle', { tier })}
        </Box>
      </Box>
      <br />

      {nonIconProviders.map((provider: any, index: number) => (
        <div key={provider.name}>
          {index > 0 && (
            <Box sx={{ color: colors.text.secondaryDark, fontSize: '16px', fontWeight: 400, mb: '10px' }}>
              <Divider>or</Divider>
            </Box>
          )}
          {
            // @ts-ignore
            provider.name === 'Email' && (
              <>
                <Typography
                  component='label'
                  htmlFor='magicEmail'
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
                  Email Magic Link
                </Typography>
                <TextField
                  placeholder='Email'
                  helperText={helperText.magicLinkEmail}
                  size='medium'
                  id='magicEmail'
                  disabled={magicLinkSending}
                  value={magicLinkEmail}
                  onChange={(ev) => {
                    setMagicLinkEmail(ev.target.value);
                    if (ev.target.value?.length === 0) {
                      setHelperText((prev) => ({
                        ...prev,
                        magicLinkEmail: '',
                      }));
                    }
                  }}
                  onKeyDown={(ev) => {
                    setMagicLinkEmail((ev?.target as HTMLInputElement)?.value);
                    const value = (ev?.target as HTMLInputElement)?.value;
                    if (value?.length === 0) {
                      setHelperText((prev) => ({
                        ...prev,
                        magicLinkEmail: '',
                      }));
                    }
                    if (ev.key === 'Enter') {
                      handleMagicLink(value, provider, ev);
                    }
                  }}
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
                    '.css-1t8l2tu-MuiInputBase-input-MuiOutlinedInput-input': {
                      padding: '12px 14px',
                      height: '20px',
                      fontSize: '14px',
                      opacity: 1,
                      color: '#374151',
                    },
                    '.MuiFormHelperText-root': {
                      color: colors.errorText,
                    },
                  }}
                />

                <Button
                  variant='contained'
                  id='magic-link-submit'
                  disabled={magicLinkSending}
                  onClick={(e) => {
                    handleMagicLink((document.getElementById('magicEmail') as HTMLInputElement).value, provider, e);
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
                  Send me a Magic link via email
                </Button>
              </>
            )
          }
          {
            // @ts-ignore
            provider.name === 'Teleport' && (
              <>
                <Button
                  variant='contained'
                  id='teleport-link-submit'
                  onClick={(e) => {
                    handleTeleportLink(provider, e);
                  }}
                  sx={{
                    borderRadius: '4px',
                    boxShadow: 0,
                    backgroundColor: '#374151',
                    color: '#ffffff',
                    fontSize: '14px',
                    fontWeight: 600,
                    ':hover': {
                      backgroundColor: '#232222', // theme.palette.primary.main
                      color: '#ffffff',
                      boxShadow: 0,
                    },
                    width: '100%',
                    p: '10px 12px',
                  }}
                >
                  Sign in with Teleport
                </Button>
              </>
            )
          }
          {
            // @ts-ignore
            provider.name === 'Credentials' && (
              <>
                <Typography
                  component='label'
                  htmlFor='credsEmail'
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
                  Admin Email
                </Typography>
                <TextField
                  placeholder='Email'
                  helperText={helperText.credsEmail}
                  size='medium'
                  id='credsEmail'
                  sx={{
                    borderRadius: '4px',
                    boxShadow: 0,
                    color: colors.text.signinDark,
                    borderWidth: '0.5px',
                    borderColor: '#D0D0D0',
                    fontSize: '12px',
                    fontWeight: 600,
                    width: '100%',
                    '.css-1t8l2tu-MuiInputBase-input-MuiOutlinedInput-input': {
                      padding: '14px 14px 16px 14px',
                    },
                    '.MuiFormHelperText-root': {
                      color: colors.errorText,
                    },
                  }}
                  value={credsUsername}
                  onChange={(ev) => {
                    setCredsUsername(ev.target.value);
                    if (ev.target.value) {
                      setHelperText((prev) => ({
                        ...prev,
                        credsEmail: '',
                      }));
                    }
                  }}
                />
                <br />
                <br />
                <Typography
                  component='label'
                  htmlFor='credsEmail'
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
                  Admin Password
                </Typography>
                <TextField
                  placeholder='Password'
                  helperText={helperText.credsPassword}
                  size='medium'
                  type='password'
                  id='credsPassword'
                  sx={{
                    borderRadius: '4px',
                    boxShadow: 0,
                    color: colors.text.signinDark,
                    borderWidth: '0.5px',
                    borderColor: '#D0D0D0',
                    fontSize: '12px',
                    fontWeight: 600,
                    width: '100%',
                    '.css-1t8l2tu-MuiInputBase-input-MuiOutlinedInput-input': {
                      padding: '14px 14px 16px 14px',
                    },
                    '.MuiFormHelperText-root': {
                      color: colors.errorText,
                    },
                  }}
                  value={credsPassword}
                  onChange={(ev) => {
                    setCredsPassword(ev.target.value);
                    if (ev.target.value) {
                      setHelperText((prev) => ({
                        ...prev,
                        credsPassword: '',
                      }));
                    }
                  }}
                />
                <br />
                <br />
                <Button
                  variant='contained'
                  id='direct-creds-submit'
                  onClick={async (ev) => {
                    ev.preventDefault();
                    const emailPattern = new RegExp('[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+.[a-zA-Z]{2,4}$');
                    let hasError = false;

                    if (!emailPattern.test(credsUsername)) {
                      setHelperText((prev) => ({
                        ...prev,
                        credsEmail: 'Please enter a valid email address.',
                      }));
                      hasError = true;
                    }

                    if (!credsPassword) {
                      setHelperText((prev) => ({
                        ...prev,
                        credsPassword: 'Password is required.',
                      }));
                      hasError = true;
                    }

                    if (hasError) {
                      return;
                    }

                    const result = await signIn(provider.id, { username: credsUsername.toLowerCase(), password: credsPassword, redirect: false });
                    if (result?.error) {
                      setHelperText((prev) => ({
                        ...prev,
                        credsPassword: 'Invalid credentials. Please try again.',
                      }));
                    } else if (result?.url) {
                      router.push(result.url);
                    }
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
                  Submit
                </Button>
              </>
            )
          }
          {
            // @ts-ignore
            provider.id === 'ldap' && (
              <>
                <Typography
                  component='label'
                  htmlFor='credsLdapUsername'
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
                  LDAP Username
                </Typography>
                <TextField
                  label=''
                  placeholder='LDAP Username'
                  helperText={helperText.ldapUsername}
                  size='medium'
                  id='credsLdapUsername'
                  sx={{
                    borderRadius: '4px',
                    boxShadow: 0,
                    color: '#000',
                    borderWidth: '5px',
                    borderColor: helperText.ldapUsername ? colors.border.error : '#D0D0D0',
                    fontSize: '12px',
                    fontWeight: 600,
                    width: '100%',
                    '.MuiOutlinedInput-notchedOutline': {
                      borderColor: helperText.ldapUsername ? colors.border.error : '#D0D0D0',
                    },
                    '.css-1t8l2tu-MuiInputBase-input-MuiOutlinedInput-input': {
                      padding: '12px 14px',
                      height: '20px',
                      fontSize: '14px',
                      opacity: 1,
                      color: '#374151',
                    },
                    '.MuiFormHelperText-root': {
                      color: colors.errorText,
                    },
                  }}
                  value={ldapCredsUsername}
                  onChange={(ev) => {
                    setLdapCredsUsername(ev.target.value);
                    if (ev.target.value) {
                      setHelperText((prev) => ({
                        ...prev,
                        ldapUsername: '',
                      }));
                    }
                  }}
                />
                <br />
                <br />
                <Typography
                  component='label'
                  htmlFor='credsLdapPassword'
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
                  LDAP Password
                </Typography>
                <TextField
                  placeholder='LDAP Password'
                  helperText={helperText.ldapPassword}
                  size='medium'
                  type='password'
                  id='credsLdapPassword'
                  sx={{
                    borderRadius: '4px',
                    boxShadow: 0,
                    color: colors.text.signinDark,
                    borderWidth: '0.5px',
                    borderColor: helperText.ldapPassword ? colors.border.error : '#D0D0D0',
                    fontSize: '12px',
                    fontWeight: 600,
                    width: '100%',
                    '.MuiOutlinedInput-notchedOutline': {
                      borderColor: helperText.ldapPassword ? colors.border.error : '#D0D0D0',
                    },
                    '.css-1t8l2tu-MuiInputBase-input-MuiOutlinedInput-input': {
                      padding: '12px 14px',
                      height: '20px',
                      fontSize: '14px',
                      opacity: 1,
                      color: '#374151',
                    },
                    '.MuiFormHelperText-root': {
                      color: colors.errorText,
                    },
                  }}
                  value={ldapCredsPassword}
                  onChange={(ev) => {
                    setLdapCredsPassword(ev.target.value);
                    if (ev.target.value) {
                      setHelperText((prev) => ({
                        ...prev,
                        ldapPassword: '',
                      }));
                    }
                  }}
                />
                <br />
                <br />
                <Button
                  variant='contained'
                  id='ldap-creds-submit'
                  onClick={(ev) => handleLdapSubmit(provider, ev)}
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
                  Submit
                </Button>
              </>
            )
          }
          <br /> <br />
        </div>
      ))}

      {!!otherProviders.length && !!nonIconProviders.length && (
        <Box sx={{ color: colors.text.secondaryDark, fontSize: '16px', fontWeight: 400, mb: '20px' }}>
          <Divider>or</Divider>
        </Box>
      )}

      <Grid container spacing={'12px'}>
        {otherProviders.map((provider: any, _index: number) => {
          return (
            <Grid item xs={otherProviders.length <= 2 ? 12 : 6} key={provider.name}>
              <Button
                variant='outlined'
                data-testid={`sso-${provider.name.toLowerCase().replace(/\s+/g, '-')}-btn`}
                onClick={() => signIn(provider.id)}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: '6px',
                  color: '#344054',
                  fontSize: '14px',
                  fontWeight: '500',
                  border: '0.5px solid #D0D0D0',
                  borderRadius: '4px',
                  padding: '12px 0px',
                  textTransform: 'none',
                  width: '100%',
                  '&:hover': {
                    background: '#e5e8ed',
                    border: '0.5px solid #D0D0D0',
                  },
                }}
              >
                <SafeIcon width={16} height={16} src={getProviderImage(provider)} alt={provider.name} title={'Sign in with ' + provider.name} />
                {{
                  Google: 'Google',
                  Okta: 'Okta',
                  Auth0: 'Auth0',
                  'Azure Active Directory': 'Azure',
                  'Azure Active Directory B2C': 'Azure',
                }[provider.name as string] ?? null}
              </Button>
            </Grid>
          );
        })}
      </Grid>

      {samlEnabled && <SignInProviderExtraSlot variant='v1' hasOtherProviders={!!otherProviders.length || !!nonIconProviders.length} />}

      <br />
    </BaseTemplate>
  );
}

// See signin_v2.tsx for rationale — direct read of authOptions.providers
// avoids latching an empty providers list when /api/auth/providers fails
// transiently (e.g. services-server unreachable at app boot).
function providersFromAuthOptions(): Record<string, { id: string; name: string; type: string; signinUrl: string; callbackUrl: string }> {
  const out: Record<string, { id: string; name: string; type: string; signinUrl: string; callbackUrl: string }> = {};
  for (const p of authOptions.providers as Array<{ id: string; name: string; type: string }>) {
    if (!p?.id) continue;
    out[p.id] = {
      id: p.id,
      name: p.name,
      type: p.type,
      signinUrl: `/api/auth/signin/${p.id}`,
      callbackUrl: `/api/auth/callback/${p.id}`,
    };
  }
  return out;
}

export async function getServerSideProps(_context: any) {
  // Prefer getProviders(); fall back to authOptions when it returns
  // null/empty so transient internal-endpoint failures don't latch the
  // signin page into "no authentication providers" until app restart.
  let providers = await getProviders();
  if (!providers || Object.keys(providers).length === 0) {
    providers = providersFromAuthOptions() as any;
  }
  const samlEnabled = process.env.SAML_ENABLED === 'true';

  // Pass tier into the SignInBelowTitle slot so its EE-side renderer can
  // self-gate. Signin runs unauthenticated, so there's no session yet —
  // fetch the canonical license here. Fail closed: empty string → slot's
  // saas check returns false → link hidden.
  let tier = '';
  try {
    const license = await getLicenseDetails();
    tier = license.tier ?? '';
  } catch {
    tier = '';
  }

  return {
    props: { providers, samlEnabled, tier },
  };
}
