import { error404Image } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { Button } from '@ui/Button';
import PropTypes from 'prop-types';
import { useRouter } from 'next/router';
import { ds } from '@utils/colors';

// Shared "403" visual rendered in place of a disabled tab/sub-tab's content, so
// a permission-gated section reads the same everywhere instead of each page
// rolling its own copy. Always carries its own "Go to Homepage" button so
// every caller gets it for free instead of re-passing the same action.
const PermissionDeniedContent = ({ message }) => {
  const router = useRouter();

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
          padding: `${ds.space[2]} 0`,
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
          Permission Denied
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
          {message || "You don't have permission to view this page."}
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
          alt='Permission Denied Illustration'
          style={{
            width: '450px',
            height: 'auto',
          }}
        />
        <Button tone='secondary' size='md' onClick={() => router.push('/home')}>
          Go to Homepage
        </Button>
      </div>
    </div>
  );
};

PermissionDeniedContent.propTypes = {
  message: PropTypes.string,
};

export default PermissionDeniedContent;
