import { isRenderedInIframe } from 'src/utils/common';
import { colors } from 'src/utils/colors';
import CustomButton from '@components1/common/NewCustomButton';
import { error404Image } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
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
          padding: '10px auto',
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
          404
        </h1>
        <p
          style={{
            fontSize: '15px',
            fontWeight: 500,
            margin: '0px',
            color: colors.background.sideBar,
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
