import { SessionProvider } from 'next-auth/react';
import './_app.css';
import './index.css';
import '../components1/common/charts/heatmapStyles.css';
import '../styles/theme-tokens.css';
import '../styles/globaltext.css';
import { GlobalFilterContextProvider } from '@lib/contexts';
import type { AppProps } from 'next/app';
import { useRouter } from 'next/router';
import type { Session } from 'next-auth';
import PageLayout from '@common/layout';
import { ThemeProvider } from '@mui/material/styles';
import { AppErrorBoundary } from '@common/ErrorBoundary';
import { DataProvider } from '@context/DataContext';
import { Toast as SnackbarComponent } from '@components1/ds/Toast';
import 'swiper/css/bundle';
import '../styles/CustomSwiperCarousel.css';
import { useThemeProvider } from '@hooks/useThemeProvider';

// Use of the <SessionProvider> is mandatory to allow components that call
// `useSession()` anywhere in your application to access the `session` object.

export default function App({ Component, pageProps }: AppProps<{ session: Session }>) {
  const router = useRouter();
  const { theme } = useThemeProvider();

  return (
    <ThemeProvider theme={theme}>
      <AppErrorBoundary>
        <SessionProvider session={pageProps.session}>
          <GlobalFilterContextProvider>
            {router.pathname.indexOf('signin') >= 0 ||
            router.pathname.indexOf('signup') >= 0 ||
            router.pathname.indexOf('signup_verify') >= 0 ||
            router.pathname.indexOf('ready') >= 0 ||
            router.pathname.indexOf('no-tenant-access') >= 0 ||
            router.pathname.indexOf('verify-request') >= 0 ||
            router.pathname.indexOf('auth/error') >= 0 ? (
              <Component {...pageProps} />
            ) : (
              <DataProvider>
                <PageLayout>
                  <Component {...pageProps} />
                  <SnackbarComponent />
                </PageLayout>
              </DataProvider>
            )}
          </GlobalFilterContextProvider>
        </SessionProvider>
      </AppErrorBoundary>
    </ThemeProvider>
  );
}
