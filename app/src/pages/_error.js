import { Button } from '@ui/Button';
import { error404Image, ErrorIcon } from '@assets';
import Image from 'next/image';
import { useRouter } from 'next/router';

function Error({ statusCode }) {
  const router = useRouter();
  const is404 = statusCode === 404;

  const title = statusCode ? String(statusCode) : 'Error';
  let message;
  if (is404) {
    message = "Oops! The page you're looking for doesn't exist.";
  } else if (statusCode) {
    message = 'Oops! Something went wrong on our end. Please try again later.';
  } else {
    message = 'Oops! An unexpected error occurred on the client.';
  }
  const illustration = is404 ? error404Image : ErrorIcon;
  const illustrationWidth = is404 ? '450px' : '200px';

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
            color: 'var(--ds-brand-600)',
          }}
        >
          {title}
        </h1>
        <p
          style={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            margin: '0px',
            color: 'var(--ds-brand-600)',
          }}
        >
          {message}
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
          src={illustration}
          alt={`${title} Illustration`}
          style={{
            width: illustrationWidth,
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

Error.getInitialProps = ({ res, err }) => {
  const statusCode = res ? res.statusCode : err ? err.statusCode : 404;
  return { statusCode };
};

export default Error;
