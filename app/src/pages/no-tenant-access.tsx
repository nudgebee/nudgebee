import { error404Image } from '@assets';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { signOut } from 'next-auth/react';
import { useRouter } from 'next/router';
import { ds } from '@utils/colors';

export default function NoTenantAccess() {
  const router = useRouter();
  const { message } = router.query;

  const errorMessage = Array.isArray(message) ? message[0] : message;

  const handleSignOut = async () => {
    await signOut({ redirect: false });
    router.push('/signin');
  };

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        alignItems: 'center',
        height: 'auto',
        textAlign: 'center',
        marginTop: 'var(--ds-space-7)',
        gap: 'var(--ds-space-6)',
        padding: '0 var(--ds-space-4)',
      }}
    >
      <div
        style={{
          height: 'auto',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          padding: 'var(--ds-space-2) auto',
          textAlign: 'center',
          margin: '0px',
        }}
      >
        <h1
          style={{
            fontSize: '120px',
            fontWeight: 'bold',
            margin: '0px',
            color: 'var(--ds-brand-600)',
          }}
        >
          403
        </h1>
        <p
          style={{
            fontSize: 'var(--ds-text-heading)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            margin: 'var(--ds-space-2) 0',
            color: 'var(--ds-brand-600)',
          }}
        >
          Access Denied
        </p>
        <p
          style={{
            fontSize: 'var(--ds-text-title)',
            fontWeight: 'var(--ds-font-weight-regular)',
            margin: 'var(--ds-space-2) 0',
            color: 'var(--ds-gray-600)',
            maxWidth: ds.space.mul(2, 75),
          }}
        >
          {errorMessage || 'You do not have an account or tenant access in this system.'}
        </p>
        <p
          style={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-regular)',
            margin: 'var(--ds-space-1) 0',
            color: 'var(--ds-gray-600)',
          }}
        >
          Please contact your administrator to request access.
        </p>
      </div>
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          gap: 'var(--ds-space-4)',
        }}
      >
        <SafeIcon
          src={error404Image}
          alt='Access Denied Illustration'
          style={{
            width: '450px',
            height: 'auto',
          }}
        />
        <div style={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
          <Button tone='secondary' size='md' onClick={handleSignOut}>
            Sign Out
          </Button>
          <Button
            tone='primary'
            size='md'
            onClick={() => {
              router.push('/signin');
            }}
          >
            Try Again
          </Button>
        </div>
      </div>
    </div>
  );
}
