import { Html, Head, Main, NextScript } from 'next/document';
import { getCriticalCssTokens } from '@hooks/useThemeProvider';
import loadBrandingFile from '@lib/loadBrandingFile';

export default function Document() {
  const criticalCss = getCriticalCssTokens();
  const brandingFile = loadBrandingFile();
  const faviconUrl = brandingFile?.faviconUrl || '/favicon.ico';
  const title = brandingFile?.title || 'Nudgebee';

  return (
    <Html lang='en'>
      <Head>
        <link rel='icon' href={faviconUrl} />
        <meta property='og:title' content={title} key='title' />
        <meta name='description' content={`${title} — Kubernetes and cloud observability, incident troubleshooting, and cost optimization.`} />
        {/* Fonts: warm the connection early, then load the stylesheet from the HTML head
            (discovered immediately, in parallel) instead of a render-blocking CSS @import.
            All families use display=swap, so font files never block first paint. */}
        <link rel='preconnect' href='https://fonts.googleapis.com' />
        <link rel='preconnect' href='https://fonts.gstatic.com' crossOrigin='anonymous' />
        <link
          rel='stylesheet'
          href='https://fonts.googleapis.com/css2?family=Open+Sans:wght@400;500;600;700&family=Poppins:wght@400;500;600&family=Roboto:wght@300;400;500;600;700&family=Roboto+Mono:wght@400;500&display=swap'
        />
        <style dangerouslySetInnerHTML={{ __html: criticalCss }} />
      </Head>
      <body>
        <Main />
        <NextScript />
      </body>
    </Html>
  );
}
