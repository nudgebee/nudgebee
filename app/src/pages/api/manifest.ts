import type { NextApiRequest, NextApiResponse } from 'next';
import { awaitServerBrandingReady, brandHostFromHeaders, resolvePwaThemeColor, resolveServerBranding } from '@lib/serverBranding';

function formatEnvTag(raw?: string | null): string {
  if (!raw) return '';
  const trimmed = raw.trim();
  const lower = trimmed.toLowerCase();
  if (lower === 'prod' || lower === 'production') return '';
  if (lower === 'dev' || lower === 'development') return 'Dev';
  if (lower === 'qa') return 'QA';
  if (lower === 'test') return 'Test';
  if (lower === 'stage' || lower === 'staging') return 'Staging';
  if (lower === 'local') return 'Local';
  return trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (process.env.ENABLE_PWA !== 'true' && process.env.NEXT_PUBLIC_ENABLE_PWA !== 'true') {
    return res.status(404).json({ error: 'PWA is not enabled' });
  }

  await awaitServerBrandingReady();

  const brandHost = brandHostFromHeaders(req.headers);
  const branding = resolveServerBranding(brandHost);

  // Disable PWA for custom/white-label partner brands
  if (branding?.isWhiteLabel) {
    return res.status(404).json({ error: 'PWA is not available for custom branding' });
  }

  const baseName = branding?.title || 'Nudgebee';
  const rawEnv = process.env.APP_ENV || process.env.NEXT_PUBLIC_APP_ENV;
  const envTag = rawEnv ? formatEnvTag(rawEnv) : process.env.NODE_ENV === 'development' ? 'Local' : '';
  const name = envTag ? `${baseName} (${envTag})` : baseName;
  const shortName = envTag ? `NB ${envTag}` : branding?.assistantName || baseName;

  const themeColor = resolvePwaThemeColor(branding);

  // High-resolution golden bee icons for Nudgebee house brand
  const icons: Array<{ src: string; sizes: string; type: string; purpose?: string }> = [
    {
      src: '/logo192.png',
      type: 'image/png',
      sizes: '192x192',
      purpose: 'any',
    },
    {
      src: '/logo192.png',
      type: 'image/png',
      sizes: '192x192',
      purpose: 'maskable',
    },
    {
      src: '/logo512.png',
      type: 'image/png',
      sizes: '512x512',
      purpose: 'any',
    },
    {
      src: '/logo512.png',
      type: 'image/png',
      sizes: '512x512',
      purpose: 'maskable',
    },
    {
      src: '/favicon.ico',
      sizes: '64x64 32x32 24x24 16x16',
      type: 'image/x-icon',
    },
  ];

  const manifest = {
    name,
    short_name: shortName,
    description: `${baseName} — Unified agentic AI platform for day-2 cloud operations`,
    start_url: '/',
    scope: '/',
    display_override: ['minimal-ui', 'standalone'],
    display: 'minimal-ui',
    theme_color: themeColor,
    background_color: '#ffffff',
    icons,
  };

  res.setHeader('Content-Type', 'application/manifest+json');
  res.setHeader('Cache-Control', 'private, max-age=60, stale-while-revalidate=300');
  return res.status(200).send(JSON.stringify(manifest));
}
