import { getProviders, signIn } from 'next-auth/react';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import Box from '@mui/material/Box';
import Typography from '@mui/material/Typography';
import * as React from 'react';
import Button from '@mui/material/Button';
import { Input } from '@components1/ds/Input';
import Head from 'next/head';
import { googleAuth, oktaAuth, oneloginAuth, azureAuth, auth0Auth, NBIconSignIn } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { EmailRegEx } from '@lib/validation';
import { Divider } from '@mui/material';
import { useRouter } from 'next/router';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import { SignInProviderExtraSlot } from '@common/auth/SignInProviderExtraSlot';
import { AuthTemplateV2 } from '@components/auth/AuthTemplateV2';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { getLicenseDetails } from '@lib/license';
import { renderSlot } from '@lib/slots';

// View states for the sign-in flow
type AuthView = 'main' | 'magic-link' | 'ldap' | 'credentials';

// Auth Method Button Component
interface AuthMethodButtonProps {
  title: string;
  subtitle?: string;
  onClick: () => void;
  icon?: React.ReactNode;
}

const AuthMethodButton: React.FC<AuthMethodButtonProps> = ({ title, subtitle, onClick, icon }) => {
  return (
    <Button
      variant='outlined'
      fullWidth
      onClick={onClick}
      sx={{
        borderRadius: '8px',
        border: `1px solid ${colors.border.nudgebeeSuggestion}`,
        backgroundColor: colors.background.white,
        color: colors.text.secondary,
        fontSize: '14px',
        fontWeight: 500,
        textTransform: 'none',
        boxShadow: '0 6px 10px rgba(0, 0, 0, 0.06)',
        fontFamily: 'Roboto, sans-serif',
        padding: '14px 16px',
        justifyContent: 'flex-start',
        gap: 1.5,
        transition: 'all 0.15s ease',
        '&:hover': {
          backgroundColor: 'white',
          border: `1px solid ${colors.border.secondary}`,
          transform: 'translateY(-1.5px)',
          boxShadow: '0 6px 8px rgba(0, 0, 0, 0.08)',
        },
      }}
    >
      {icon && (
        <Box
          sx={{
            width: '36px',
            height: '36px',
            borderRadius: '8px',
            backgroundColor: colors.background.suggestionCardHover,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}
        >
          {icon}
        </Box>
      )}
      <Box sx={{ textAlign: 'left' }}>
        <Typography sx={{ fontSize: '14px', fontWeight: 500, color: colors.text.secondary }}>{title}</Typography>
        {subtitle && <Typography sx={{ fontSize: '12px', color: colors.text.quaternary }}>{subtitle}</Typography>}
      </Box>
    </Button>
  );
};

// Back Button Component
interface BackButtonProps {
  onClick: () => void;
  label?: string;
}

const BackButton: React.FC<BackButtonProps> = ({ onClick, label = 'Back to Sign in options' }) => {
  return (
    <Button
      onClick={onClick}
      startIcon={<ArrowBackIcon sx={{ fontSize: '18px' }} />}
      sx={{
        color: colors.text.secondary,
        fontSize: '14px',
        fontWeight: 400,
        textTransform: 'none',
        fontFamily: 'Roboto, sans-serif',
        padding: '8px 0',
        justifyContent: 'flex-start',
        mb: 3,
        '&:hover': {
          backgroundColor: 'transparent',
          color: colors.text.secondary,
        },
      }}
    >
      {label}
    </Button>
  );
};

export default function SignInV2({ providers, samlEnabled, tier }: any) {
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

  // Current view state - determines which screen to show
  const [currentView, setCurrentView] = React.useState<AuthView>('main');

  // Form state
  const [helperText, setHelperText] = React.useState({
    ldapUsername: '',
    ldapPassword: '',
    magicLinkEmail: '',
    credsEmail: '',
    credsPassword: '',
  });
  const [ldapCredsUsername, setLdapCredsUsername] = React.useState('');
  const [ldapCredsPassword, setLdapCredsPassword] = React.useState('');
  const [magicLinkEmail, setMagicLinkEmail] = React.useState('');
  const [magicLinkSending, setMagicLinkSending] = React.useState(false);
  const [credsUsername, setCredsUsername] = React.useState('');
  const [credsPassword, setCredsPassword] = React.useState('');

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

  // Handle going back to main view
  const handleBack = () => {
    setCurrentView('main');
    // Clear any form errors when going back
    setHelperText({
      ldapUsername: '',
      ldapPassword: '',
      magicLinkEmail: '',
      credsEmail: '',
      credsPassword: '',
    });
  };

  // Handle LDAP form submission
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
    const result = await signIn(provider.id, { username: ldapCredsUsername.trim().toLowerCase(), password: ldapCredsPassword, redirect: false });
    if (result?.error) {
      setHelperText((prev) => ({
        ...prev,
        ldapPassword: 'Invalid credentials. Please try again.',
      }));
    } else if (result?.url) {
      router.push(result.url);
    }
  };

  // Handle Magic Link submission
  async function handleMagicLink(value: string, provider: any, ev: any) {
    if (EmailRegEx.test(value)) {
      ev.preventDefault();
      setMagicLinkSending(true);
      const result = await signIn(provider.id, { email: value.trim().toLowerCase(), redirect: false }).finally(() => setMagicLinkSending(false));
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

  // Handle Teleport login
  function handleTeleportLink(provider: any) {
    signIn(provider.id, {});
  }

  // Handle Credentials (Admin) form submission
  const handleCredentialsSubmit = async (provider: any, ev: any) => {
    ev.preventDefault();

    setHelperText((prev) => ({
      ...prev,
      credsEmail: '',
      credsPassword: '',
    }));

    const emailPattern = new RegExp('[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+.[a-zA-Z]{2,4}$');
    let hasError = false;
    const trimmedUsername = credsUsername.trim();

    if (!emailPattern.test(trimmedUsername)) {
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

    const result = await signIn(provider.id, { username: trimmedUsername.toLowerCase(), password: credsPassword, redirect: false });
    if (result?.error) {
      setHelperText((prev) => ({
        ...prev,
        credsPassword: 'Invalid credentials. Please try again.',
      }));
    } else if (result?.url) {
      router.push(result.url);
    }
  };

  // Get provider image
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

  // Provider filters
  const iconProviders = ['Google', 'Okta', 'OneLogin', 'Auth0', 'Azure Active Directory', 'Azure Active Directory B2C'];

  // Handle case when no providers available
  if (providers == undefined || providers == null || Object.values(providers).length == 0) {
    return (
      <>
        <Head>
          {!brandingLoading && brandFaviconUrl && <link rel='icon' href={brandFaviconUrl} />}
          <title>{brandTitle || 'Nudgebee'}: Login</title>
          <meta property='og:title' content={`${brandTitle || 'Nudgebee'} - Signin`} key='title' />
        </Head>
        <Box
          sx={{
            height: '100vh',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <Typography variant='h4' sx={{ color: colors.text.signinDark, fontSize: '14px', fontWeight: 600 }}>
            No authentication providers are enabled. Please contact the administrator.
          </Typography>
        </Box>
      </>
    );
  }

  // Separate providers by type
  const emailProvider = Object.values(providers).find((p: any) => p.name === 'Email');
  const ldapProvider = Object.values(providers).find((p: any) => p.id === 'ldap');
  const teleportProvider = Object.values(providers).find((p: any) => p.name === 'Teleport');
  const credentialsProvider = Object.values(providers).find((p: any) => p.name === 'Credentials');
  const otherIconProviders = Object.values(providers).filter((provider: any) => iconProviders.includes(provider.name));

  // Render Magic Link Form View
  const renderMagicLinkView = () => (
    <Box
      sx={{
        animation: 'slideIn 0.3s ease-out',
        '@keyframes slideIn': {
          from: { opacity: 0, transform: 'translateX(20px)' },
          to: { opacity: 1, transform: 'translateX(0)' },
        },
      }}
    >
      <BackButton onClick={handleBack} />

      <Typography
        sx={{
          fontSize: '22px',
          fontWeight: 600,
          color: colors.text.secondary,
          fontFamily: 'Poppins, sans-serif',
          letterSpacing: '-0.6px',
        }}
      >
        Sign in with Magic Link
      </Typography>
      <Typography
        sx={{
          fontSize: '14px',
          color: colors.text.tertiary,
          fontFamily: 'Roboto, sans-serif',
          mb: 3,
        }}
      >
        We&apos;ll send you a secure link to sign in
      </Typography>

      <Box sx={{ mb: 3 }}>
        <Input
          label='Email address'
          id='magicEmail'
          type='email'
          placeholder='Enter your email'
          value={magicLinkEmail}
          disabled={magicLinkSending}
          error={helperText.magicLinkEmail || undefined}
          onChange={(next) => {
            setMagicLinkEmail(next);
            if (next?.length === 0) {
              setHelperText((prev) => ({ ...prev, magicLinkEmail: '' }));
            }
          }}
          onKeyDown={(ev) => {
            if (ev.key === 'Enter' && emailProvider) {
              handleMagicLink((ev.target as HTMLInputElement)?.value, emailProvider, ev);
            }
          }}
        />
      </Box>
      <Button
        variant='contained'
        fullWidth
        id='magic-link-submit'
        disabled={magicLinkSending}
        onClick={(e) => emailProvider && handleMagicLink(magicLinkEmail, emailProvider, e)}
        sx={{
          borderRadius: '8px',
          backgroundColor: colors.background.brandButton,
          color: colors.text.secondary,
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
        {magicLinkSending ? 'Sending...' : 'Get magic link'}
      </Button>
    </Box>
  );

  // Render LDAP Form View
  const renderLdapView = () => (
    <Box
      sx={{
        animation: 'slideIn 0.3s ease-out',
        '@keyframes slideIn': {
          from: { opacity: 0, transform: 'translateX(20px)' },
          to: { opacity: 1, transform: 'translateX(0)' },
        },
      }}
    >
      <BackButton onClick={handleBack} />

      <Typography
        sx={{
          fontSize: '22px',
          fontWeight: 600,
          color: colors.text.secondary,
          fontFamily: 'Poppins, sans-serif',
          letterSpacing: '-0.6px',
        }}
      >
        Sign in with LDAP
      </Typography>
      <Typography
        sx={{
          fontSize: '14px',
          color: colors.text.tertiary,
          fontFamily: 'Roboto, sans-serif',
          mb: 3,
        }}
      >
        Enter your LDAP credentials to continue
      </Typography>

      <Box sx={{ mb: 2 }}>
        <Input
          label='LDAP Username'
          id='ldapUsername'
          placeholder='Enter LDAP username'
          value={ldapCredsUsername}
          error={helperText.ldapUsername || undefined}
          onChange={(next) => {
            setLdapCredsUsername(next);
            if (next) {
              setHelperText((prev) => ({ ...prev, ldapUsername: '' }));
            }
          }}
        />
      </Box>

      <Box sx={{ mb: 3 }}>
        <Input
          label='LDAP Password'
          id='ldapPassword'
          type='password'
          placeholder='Enter LDAP password'
          value={ldapCredsPassword}
          error={helperText.ldapPassword || undefined}
          onChange={(next) => {
            setLdapCredsPassword(next);
            if (next) {
              setHelperText((prev) => ({ ...prev, ldapPassword: '' }));
            }
          }}
        />
      </Box>

      <Button
        variant='contained'
        fullWidth
        onClick={(ev) => ldapProvider && handleLdapSubmit(ldapProvider, ev)}
        sx={{
          borderRadius: '8px',
          backgroundColor: colors.background.brandButton,
          color: colors.text.secondary,
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
        }}
      >
        Sign in
      </Button>
    </Box>
  );

  // Render Credentials (Admin) Form View
  const renderCredentialsView = () => (
    <Box
      sx={{
        animation: 'slideIn 0.3s ease-out',
        '@keyframes slideIn': {
          from: { opacity: 0, transform: 'translateX(20px)' },
          to: { opacity: 1, transform: 'translateX(0)' },
        },
      }}
    >
      <BackButton onClick={handleBack} />

      <Typography
        sx={{
          fontSize: '22px',
          fontWeight: 600,
          color: colors.text.secondary,
          fontFamily: 'Poppins, sans-serif',
          letterSpacing: '-0.6px',
        }}
      >
        Admin Sign in
      </Typography>
      <Typography
        sx={{
          fontSize: '14px',
          color: colors.text.tertiary,
          fontFamily: 'Roboto, sans-serif',
          mb: 3,
        }}
      >
        Enter your admin credentials to continue
      </Typography>

      <Box sx={{ mb: 2 }}>
        <Input
          label='Admin Email'
          id='credsEmail'
          type='email'
          placeholder='Enter admin email'
          value={credsUsername}
          error={helperText.credsEmail || undefined}
          onChange={(next) => {
            setCredsUsername(next);
            if (next) {
              setHelperText((prev) => ({ ...prev, credsEmail: '' }));
            }
          }}
        />
      </Box>

      <Box sx={{ mb: 3 }}>
        <Input
          label='Admin Password'
          id='credsPassword'
          type='password'
          placeholder='Enter admin password'
          value={credsPassword}
          error={helperText.credsPassword || undefined}
          onChange={(next) => {
            setCredsPassword(next);
            if (next) {
              setHelperText((prev) => ({ ...prev, credsPassword: '' }));
            }
          }}
        />
      </Box>

      <Button
        variant='contained'
        fullWidth
        onClick={(ev) => credentialsProvider && handleCredentialsSubmit(credentialsProvider, ev)}
        sx={{
          borderRadius: '8px',
          backgroundColor: colors.background.brandButton,
          color: colors.text.secondary,
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
        }}
      >
        Sign in
      </Button>
    </Box>
  );

  // Render Main View with all options
  const renderMainView = () => (
    <Box
      sx={{
        animation: 'fadeIn 0.3s ease-out',
        '@keyframes fadeIn': {
          from: { opacity: 0 },
          to: { opacity: 1 },
        },
      }}
    >
      {/* Welcome Text */}
      <Box sx={{ textAlign: 'center', mb: '40px' }}>
        <Typography
          sx={{
            fontSize: '28px',
            fontWeight: 600,
            color: colors.text.secondary,
            fontFamily: 'Poppins, sans-serif',
            letterSpacing: -1,
          }}
        >
          Hey! Welcome back
        </Typography>
        {renderSlot('SignInBelowTitle', { variant: 'v2', tier })}
      </Box>

      {/* 1. Third-party OAuth Providers in a 2-column flex layout */}
      {otherIconProviders.length > 0 && (
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: '12px',
          }}
        >
          {otherIconProviders.map((provider: any) => (
            <Button
              key={provider.name}
              variant='outlined'
              data-testid={`sso-${provider.name.toLowerCase().replace(/\s+/g, '-')}-btn`}
              onClick={() => signIn(provider.id)}
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 1,
                color: colors.text.secondary,
                fontSize: '14px',
                fontWeight: 500,
                border: '1px solid #E5E7EB',
                borderRadius: '8px',
                boxShadow: '0 6px 8px rgba(0, 0, 0, 0.06)',
                padding: '12px',
                backgroundColor: colors.background.white,
                transition: 'all 0.15s ease',
                fontFamily: 'Roboto, sans-serif',
                textTransform: 'none',
                flex: '1 1 calc(50% - 6px)',
                minWidth: '120px',
                boxSizing: 'border-box',
                '&:hover': {
                  backgroundColor: 'white',
                  border: '1px solid #D1D5DB',
                  transform: 'translateY(-1px)',
                  boxShadow: '0 6px 8px rgba(0, 0, 0, 0.1)',
                },
              }}
            >
              <SafeIcon width={18} height={18} src={getProviderImage(provider)} alt={provider.name} title={'Sign in with ' + provider.name} />
              {provider.name === 'Google' && 'Google'}
              {provider.name === 'Okta' && 'Okta'}
              {provider.name === 'OneLogin' && 'OneLogin'}
              {provider.name === 'Auth0' && 'Auth0'}
              {(provider.name === 'Azure Active Directory' || provider.name === 'Azure Active Directory B2C') && 'Azure'}
            </Button>
          ))}
        </Box>
      )}

      {samlEnabled && <SignInProviderExtraSlot variant='v2' hasOtherProviders={otherIconProviders.length > 0} />}

      {/* Divider between providers and Magic Link */}
      {(otherIconProviders.length > 0 || samlEnabled) && emailProvider && (
        <Box sx={{ my: 3 }}>
          <Divider
            sx={{ color: colors.text.tertiarymedium, fontSize: '12px', '&::before, &::after': { borderColor: colors.border.nudgebeeSuggestion } }}
          >
            OR
          </Divider>
        </Box>
      )}

      {/* 2. Magic Link Button */}
      {emailProvider && (
        <Box sx={{ mb: 2 }}>
          <AuthMethodButton
            title='Login via Magic Link'
            subtitle='Get a secure link sent to your email'
            onClick={() => setCurrentView('magic-link')}
            icon={
              <svg
                width='18'
                height='18'
                viewBox='0 0 24 24'
                fill='none'
                xmlns='http://www.w3.org/2000/svg'
                style={{ color: colors.text.primaryLight }}
              >
                <path
                  d='M4 4H20C21.1 4 22 4.9 22 6V18C22 19.1 21.1 20 20 20H4C2.9 20 2 19.1 2 18V6C2 4.9 2.9 4 4 4Z'
                  stroke='currentColor'
                  strokeWidth='2'
                  strokeLinecap='round'
                  strokeLinejoin='round'
                />
                <path d='M22 6L12 13L2 6' stroke='currentColor' strokeWidth='2' strokeLinecap='round' strokeLinejoin='round' />
              </svg>
            }
          />
        </Box>
      )}

      {/* Divider between Magic Link and LDAP */}
      {emailProvider && ldapProvider && (
        <Box sx={{ my: 3 }}>
          <Divider
            sx={{ color: colors.text.tertiarymedium, fontSize: '12px', '&::before, &::after': { borderColor: colors.border.nudgebeeSuggestion } }}
          >
            OR
          </Divider>
        </Box>
      )}

      {/* 3. LDAP Button */}
      {ldapProvider && (
        <Box sx={{ mb: 2 }}>
          <AuthMethodButton
            title='Login via LDAP'
            subtitle='Use your organization credentials'
            onClick={() => setCurrentView('ldap')}
            icon={
              <svg
                width='18'
                height='18'
                viewBox='0 0 24 24'
                fill='none'
                xmlns='http://www.w3.org/2000/svg'
                style={{ color: colors.text.primaryLight }}
              >
                <rect x='3' y='11' width='18' height='11' rx='2' stroke='currentColor' strokeWidth='2' />
                <path d='M7 11V7C7 4.23858 9.23858 2 12 2C14.7614 2 17 4.23858 17 7V11' stroke='currentColor' strokeWidth='2' strokeLinecap='round' />
                <circle cx='12' cy='16' r='1' fill='currentColor' />
              </svg>
            }
          />
        </Box>
      )}

      {/* Divider before Teleport */}
      {ldapProvider && teleportProvider && (
        <Box sx={{ my: 3 }}>
          <Divider
            sx={{ color: colors.text.tertiarymedium, fontSize: '12px', '&::before, &::after': { borderColor: colors.border.nudgebeeSuggestion } }}
          >
            OR
          </Divider>
        </Box>
      )}

      {/* 4. Teleport Button */}
      {teleportProvider && (
        <Box sx={{ mb: 2 }}>
          <AuthMethodButton
            title='Login via Teleport'
            subtitle='Use Teleport SSO authentication'
            onClick={() => handleTeleportLink(teleportProvider)}
            icon={
              <svg
                width='18'
                height='18'
                viewBox='0 0 24 24'
                fill='none'
                xmlns='http://www.w3.org/2000/svg'
                style={{ color: colors.text.primaryLight }}
              >
                <circle cx='12' cy='12' r='10' stroke='currentColor' strokeWidth='2' />
                <path d='M12 6V12L16 14' stroke='currentColor' strokeWidth='2' strokeLinecap='round' strokeLinejoin='round' />
              </svg>
            }
          />
        </Box>
      )}

      {/* Divider before Credentials */}
      {(ldapProvider || teleportProvider || emailProvider) && credentialsProvider && (
        <Box sx={{ my: 3 }}>
          <Divider
            sx={{ color: colors.text.tertiarymedium, fontSize: '12px', '&::before, &::after': { borderColor: colors.border.nudgebeeSuggestion } }}
          >
            OR
          </Divider>
        </Box>
      )}

      {/* 5. Credentials (Admin) Button */}
      {credentialsProvider && (
        <Box sx={{ mb: 2 }}>
          <AuthMethodButton
            title='Admin Login'
            subtitle='Sign in with admin credentials'
            onClick={() => setCurrentView('credentials')}
            icon={
              <svg
                width='18'
                height='18'
                viewBox='0 0 24 24'
                fill='none'
                xmlns='http://www.w3.org/2000/svg'
                style={{ color: colors.text.primaryLight }}
              >
                <path
                  d='M20 21V19C20 17.9391 19.5786 16.9217 18.8284 16.1716C18.0783 15.4214 17.0609 15 16 15H8C6.93913 15 5.92172 15.4214 5.17157 16.1716C4.42143 16.9217 4 17.9391 4 19V21'
                  stroke='currentColor'
                  strokeWidth='2'
                  strokeLinecap='round'
                  strokeLinejoin='round'
                />
                <circle cx='12' cy='7' r='4' stroke='currentColor' strokeWidth='2' strokeLinecap='round' strokeLinejoin='round' />
              </svg>
            }
          />
        </Box>
      )}
    </Box>
  );

  return (
    <>
      <Head>
        {!brandingLoading && brandFaviconUrl && <link rel='icon' href={brandFaviconUrl} />}
        <title>{brandTitle || 'Nudgebee'}: Login</title>
        <meta property='og:title' content={`${brandTitle || 'Nudgebee'} - Signin`} key='title' />
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

        {/* Render current view based on state */}
        {currentView === 'main' && renderMainView()}
        {currentView === 'magic-link' && renderMagicLinkView()}
        {currentView === 'ldap' && renderLdapView()}
        {currentView === 'credentials' && renderCredentialsView()}
      </AuthTemplateV2>
    </>
  );
}

// Build the client-safe providers map directly from authOptions. Avoids the
// HTTP round-trip via getProviders() which can fail transiently (e.g.
// services-server unreachable at app boot — getProviders returns null,
// signin renders "No authentication providers are enabled", and the empty
// state latches per-process until app restart). authOptions.providers is
// built synchronously at module load from env vars, so this path always
// reflects the configured providers regardless of services-server health.
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
  // Prefer getProviders() (matches the public NextAuth shape); fall back to
  // a direct read of authOptions when getProviders returns null/empty so a
  // transient internal /api/auth/providers failure doesn't latch the signin
  // page into the "no authentication providers" state.
  let providers = await getProviders();
  if (!providers || Object.keys(providers).length === 0) {
    providers = providersFromAuthOptions() as any;
  }
  const samlEnabled = process.env.SAML_ENABLED === 'true';

  // Pass tier into the SignInBelowTitle slot; see signin.tsx for rationale.
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
