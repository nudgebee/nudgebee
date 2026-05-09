import CustomButton from '@components1/common/NewCustomButton';
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
        marginTop: '80px',
        gap: '40px',
      }}
    >
      <div
        style={{
          height: 'auto',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          padding: '10px 0',
          textAlign: 'center',
          margin: '0px',
        }}
      >
        <h1
          style={{
            fontSize: '170px',
            fontWeight: 'bold',
            margin: '0px',
            color: '#1B2D4A',
          }}
        >
          {title}
        </h1>
        <p
          style={{
            fontSize: '15px',
            fontWeight: 500,
            margin: '0px',
            color: '#1B2D4A',
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
          gap: '24px',
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
        <CustomButton
          variant='tertiary'
          size='Medium'
          text={'Go to Homepage'}
          onClick={() => {
            router.push(`/home`);
          }}
        />
      </div>
    </div>
  );
}

Error.getInitialProps = ({ res, err }) => {
  const statusCode = res ? res.statusCode : err ? err.statusCode : 404;
  return { statusCode };
};

export default Error;
