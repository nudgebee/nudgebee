import { SessionProvider } from 'next-auth/react';
import './_app.css';
import './index.css';
import '../components/common/charts/heatmapStyles.css';
import '../styles/theme-tokens.css';
import '../styles/globaltext.css';
import { GlobalFilterContextProvider } from '@lib/contexts';
import type { AppProps } from 'next/app';
import { useRouter } from 'next/router';
import type { Session } from 'next-auth';
import PageLayout from '@shared/layout';
import { ThemeProvider } from '@mui/material/styles';
import { AppErrorBoundary } from '@shared/ErrorBoundary';
import { DataProvider } from '@context/DataContext';
import { Toast as SnackbarComponent } from '@ui/Toast';
import { TourProvider } from '@components/common/tour';
import { NubiGlobalChatProvider } from '@context/NubiGlobalChatContext';
import NubiGlobalChat from '@components/llm/NubiGlobalChat';
import 'swiper/css/bundle';
import '../styles/CustomSwiperCarousel.css';
import 'driver.js/dist/driver.css';
import '../styles/tour.css';
import '../styles/nubi-animation.css';
import { useThemeProvider } from '@hooks/useThemeProvider';
import { useEffect } from 'react';
import { reportClientError } from '@lib/clientErrorReporter';

// Use of the <SessionProvider> is mandatory to allow components that call
// `useSession()` anywhere in your application to access the `session` object.

export default function App({ Component, pageProps }: AppProps<{ session: Session }>) {
  const router = useRouter();
  const { theme } = useThemeProvider();

  // Catch uncaught JS errors and unhandled promise rejections that never reach
  // a React error boundary, and report them to Loki (see @lib/clientErrorReporter).
  useEffect(() => {
    const onError = (event: ErrorEvent) => {
      reportClientError({
        kind: 'js-error',
        message: event.message || 'window.onerror',
        stack: event.error?.stack,
        source: event.filename ? `${event.filename}:${event.lineno}:${event.colno}` : undefined,
      });
    };
    const onRejection = (event: PromiseRejectionEvent) => {
      const reason = event.reason as { message?: string; stack?: string } | undefined;
      reportClientError({
        kind: 'unhandled-rejection',
        message: reason?.message ?? String(event.reason),
        stack: reason?.stack,
      });
    };
    window.addEventListener('error', onError);
    window.addEventListener('unhandledrejection', onRejection);
    return () => {
      window.removeEventListener('error', onError);
      window.removeEventListener('unhandledrejection', onRejection);
    };
  }, []);

  return (
    <ThemeProvider theme={theme}>
      <AppErrorBoundary>
        <SessionProvider
          session={pageProps.session}
          // Keep an open tab's session alive. NextAuth (jwt strategy) only rolls
          // the session cookie's expiry when the client hits /api/auth/session;
          // ordinary /api/graphql traffic does NOT extend it. With no interval,
          // an open-but-idle tab is never refreshed and the JWT hard-expires at
          // session.maxAge, after which the next 401/invalid-jwt bounces the user
          // to /signin. Poll every 5 min (well under maxAge) so any live tab
          // renews the cookie; only when online, plus the default focus refetch.
          refetchInterval={5 * 60}
          refetchOnWindowFocus={true}
          refetchWhenOffline={false}
        >
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
                <TourProvider>
                  <NubiGlobalChatProvider>
                    <PageLayout>
                      <Component {...pageProps} />
                      <SnackbarComponent />
                    </PageLayout>
                    <NubiGlobalChat />
                  </NubiGlobalChatProvider>
                </TourProvider>
              </DataProvider>
            )}
          </GlobalFilterContextProvider>
        </SessionProvider>
      </AppErrorBoundary>
    </ThemeProvider>
  );
}
