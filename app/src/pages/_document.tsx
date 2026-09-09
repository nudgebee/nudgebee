import { Html, Head, Main, NextScript, type DocumentContext } from 'next/document';
import { getCriticalCssTokens } from '@hooks/useThemeProvider';
import { brandHostFromHeaders, resolveServerBranding } from '@lib/serverBranding';

type DocumentProps = { brandHost?: string | null };

export default function Document({ brandHost }: DocumentProps) {
  const criticalCss = getCriticalCssTokens(brandHost);
  const branding = resolveServerBranding(brandHost);
  const faviconUrl = branding?.faviconUrl || '/favicon.ico';
  const title = branding?.title || 'Nudgebee';

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

// Carries the request host into the head so favicon / og:title / critical-CSS
// tokens follow the partner brand for that hostname.
//
// Only pages rendered per request get a host here — `signin`, `signup` and the
// other getServerSideProps pages. Pages Next.js statically optimizes are
// prerendered at build time, where there is no request: `brandHost` is null,
// branding falls back to the deployment-wide default, and the client repaints
// from /api/public/app_config after hydration. That is the same limit those
// pages already had with a single TENANT_BRANDING_FILE — the head has never
// varied per deployment on a prerendered page — so this adds no new gap. Signin
// being request-rendered is what matters: it is the first page a partner's users
// see.
Document.getInitialProps = async (ctx: DocumentContext) => {
  const initialProps = await ctx.defaultGetInitialProps(ctx);
  return { ...initialProps, brandHost: brandHostFromHeaders(ctx.req?.headers) };
};
