import { Html, Head, Main, NextScript, type DocumentContext } from 'next/document';
import { getCriticalCssTokens } from '@hooks/useThemeProvider';
import { awaitServerBrandingReady, brandHostFromHeaders, resolvePwaThemeColor, resolveServerBranding } from '@lib/serverBranding';

type DocumentProps = { brandHost?: string | null };

export default function Document({ brandHost }: DocumentProps) {
  const criticalCss = getCriticalCssTokens(brandHost);
  const branding = resolveServerBranding(brandHost);
  // No house-brand fallbacks here, for the same reason the page titles lost
  // theirs. On a full page load this head carries the ONLY <link rel='icon'> in
  // the document: the client's next/head icon tag is gated on the branding
  // config and does not exist until /api/public/app_config answers (verified by
  // curling /signin — one icon link in the SSR HTML, this one). So a default
  // here is not a placeholder that something later corrects, it is the favicon
  // a partner's users see for the whole load.
  //
  // Omitting the tag is better than defaulting it: the browser then requests
  // /favicon.ico on its own, which is the same file OSS and house deployments
  // want, without this document asserting a brand for a host it could not
  // resolve. A configured deployment always resolves a value, because every
  // theme.json — including branding/default — supplies faviconUrl.
  const faviconUrl = branding?.faviconUrl;
  const title = branding?.title;
  const themeColor = resolvePwaThemeColor(branding);

  return (
    <Html lang='en'>
      <Head>
        {faviconUrl && <link rel='icon' href={faviconUrl} />}
        {title && <meta property='og:title' content={title} key='title' />}
        {title && (
          <meta name='description' content={`${title} — Kubernetes and cloud observability, incident troubleshooting, and cost optimization.`} />
        )}
        {!branding?.isWhiteLabel && (process.env.ENABLE_PWA === 'true' || process.env.NEXT_PUBLIC_ENABLE_PWA === 'true') && (
          <>
            <link rel='manifest' href='/manifest.json' />
            <link rel='apple-touch-icon' href='/logo192.png' />
            <meta name='theme-color' content={themeColor} />
          </>
        )}
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

// Carries the request host into the head so favicon / og:title / critical-CSS
// tokens follow the partner brand for that hostname.
//
// Only pages rendered per request get a host here — `signin`, `signup` and the
// other getServerSideProps pages. Pages Next.js statically optimizes are
// prerendered at build time, where there is no request: `brandHost` is null,
// the head carries the house brand, and the client repaints from
// /api/public/app_config after hydration. Signin
// being request-rendered is what matters: it is the first page a partner's users
// see.
Document.getInitialProps = async (ctx: DocumentContext) => {
  // getInitialProps is the one place in the document that CAN await, and the
  // render body below cannot: `getCriticalCssTokens` and `resolveServerBranding`
  // are both synchronous. Waiting here means a cold server does not emit the
  // house-brand favicon, og:title and colour tokens into the first response and
  // then repaint. No-op once the provider is warm, and in OSS, where nothing
  // registers one.
  await awaitServerBrandingReady();
  const initialProps = await ctx.defaultGetInitialProps(ctx);
  return { ...initialProps, brandHost: brandHostFromHeaders(ctx.req?.headers) };
};
