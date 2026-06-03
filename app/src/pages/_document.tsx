import { Html, Head, Main, NextScript } from 'next/document';
import { getCriticalCssTokens } from '@hooks/useThemeProvider';
import loadBrandingFile from '@lib/loadBrandingFile';

export default function Document() {
  const criticalCss = getCriticalCssTokens();
  const brandingFile = loadBrandingFile();
  const faviconUrl = brandingFile?.faviconUrl || '/favicon.ico';
  const title = brandingFile?.title || 'Nudgebee';

  return (
    <Html>
      <Head>
        <link rel='icon' href={faviconUrl} />
        <meta property='og:title' content={title} key='title' />
        <style dangerouslySetInnerHTML={{ __html: criticalCss }} />
      </Head>
      <body>
        <Main />
        <NextScript />
      </body>
    </Html>
  );
}
