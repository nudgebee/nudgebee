import { Button } from '@ui/Button';
import { ds } from 'src/utils/colors';
import { ErrorIcon } from '@assets';
import Image from 'next/image';
import { useRouter } from 'next/router';

export default function Custom500() {
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
      }}
    >
      <div
        style={{
          height: 'auto',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          padding: 'var(--ds-space-2) 0',
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
          500
        </h1>
        <p
          style={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            margin: '0px',
            color: ds.brand[600],
          }}
        >
          Oops! Something went wrong on our end. Please try again later.
        </p>
      </div>
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          gap: 'var(--ds-space-5)',
        }}
      >
        <Image
          src={ErrorIcon}
          alt='500 Illustration'
          style={{
            width: '200px',
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
