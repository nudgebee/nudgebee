import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
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
            color: colors.background.sideBar,
          }}
        >
          500
        </h1>
        <p
          style={{
            fontSize: '15px',
            fontWeight: 500,
            margin: '0px',
            color: colors.background.sideBar,
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
          gap: '24px',
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
