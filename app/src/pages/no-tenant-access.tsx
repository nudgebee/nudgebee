import { error404Image } from '@assets';
import CustomButton from '@components1/common/NewCustomButton';
import SafeIcon from '@components1/common/SafeIcon';
import { signOut } from 'next-auth/react';
import { useRouter } from 'next/router';

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
        marginTop: '80px',
        gap: '40px',
        padding: '0 20px',
      }}
    >
      <div
        style={{
          height: 'auto',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          padding: '10px auto',
          textAlign: 'center',
          margin: '0px',
        }}
      >
        <h1
          style={{
            fontSize: '120px',
            fontWeight: 'bold',
            margin: '0px',
            color: '#1B2D4A',
          }}
        >
          403
        </h1>
        <p
          style={{
            fontSize: '24px',
            fontWeight: 600,
            margin: '10px 0',
            color: '#1B2D4A',
          }}
        >
          Access Denied
        </p>
        <p
          style={{
            fontSize: '16px',
            fontWeight: 400,
            margin: '10px 0',
            color: '#5A6C7D',
            maxWidth: '600px',
          }}
        >
          {errorMessage || 'You do not have an account or tenant access in this system.'}
        </p>
        <p
          style={{
            fontSize: '15px',
            fontWeight: 400,
            margin: '5px 0',
            color: '#5A6C7D',
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
          gap: '20px',
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
        <div style={{ display: 'flex', gap: '10px' }}>
          <CustomButton variant='tertiary' size='Medium' text={'Sign Out'} onClick={handleSignOut} />
          <CustomButton
            variant='secondary'
            size='Medium'
            text={'Try Again'}
            onClick={() => {
              router.push('/signin');
            }}
          />
        </div>
      </div>
    </div>
  );
}
