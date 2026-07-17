import { isRenderedInIframe } from 'src/utils/common';
import { ds } from 'src/utils/colors';
import { Button } from '@ui/Button';
import { error404Image } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { useRouter } from 'next/router';

export default function Custom404() {
  // let url = '#';

  const router = useRouter();

  if (isRenderedInIframe()) {
    const match = RegExp(/\/([\w-]+)$/).exec(window.parent.location.pathname);
    const accountId = match ? match[1] : null;
    if (accountId) {
      // url = `${window.location.origin}/api/proxy/grafana/gr-${accountId}?orgId=1`;
    }
  }
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
            fontSize: '170px',
            fontWeight: 'bold',
            margin: '0px',
            color: ds.brand[600],
          }}
        >
          404
        </h1>
        <p
          style={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            margin: '0px',
            color: ds.brand[600],
          }}
        >
          Oops! The page you&lsquo;re looking for doesn&lsquo;t exist.
        </p>
      </div>
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
        }}
      >
        <SafeIcon
          src={error404Image}
          alt='404 Illustration'
          style={{
            width: '450px',
            height: 'auto',
          }}
        />
        <Button
          tone='secondary'
          size='md'
          onClick={() => {
            router.push(`/home`);
          }}
        >
          Go to Homepage
        </Button>
      </div>
    </div>
  );
}
