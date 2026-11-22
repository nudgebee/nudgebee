import fs from 'fs';
import path from 'path';
import type { NextApiRequest, NextApiResponse } from 'next';
import { awaitServerBrandingReady, brandHostFromHeaders, resolveServerBranding } from '@lib/serverBranding';

// The legal pages, answered per request host.
//
// public/privacy.html and public/terms_of_use.html are the house brand's own
// documents: they name its legal entity, so they cannot be re-titled for a
// partner. A partner host instead points at the partner's own pages: when the
// host's branding sets `privacyUrl` / `termsUrl`, the visitor is redirected
// there. A host that sets neither is served the file exactly as before.
//
// The rewrites in next.config.js send the two public URLs here ahead of the
// static files, which is the only way to answer them differently per host.
// This route lives in OSS and carries no branding logic of its own: with no
// branding provider registered it always serves the file. The OSS snapshot
// ships no house pages (.oss-exclude), so there the answer is 404.
const DOCUMENTS = {
  privacy: { link: 'privacyUrl', file: 'privacy.html' },
  terms: { link: 'termsUrl', file: 'terms_of_use.html' },
} as const;

// null: the build ships no such page.
const housePages = new Map<string, string | null>();

function housePage(file: string): string | null {
  let html = housePages.get(file);
  if (html === undefined) {
    try {
      html = fs.readFileSync(path.join(process.cwd(), 'public', file), 'utf8');
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'ENOENT') throw err;
      html = null;
    }
    housePages.set(file, html);
  }
  return html;
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const name = String(req.query.doc);
  if (!Object.hasOwn(DOCUMENTS, name)) {
    res.status(404).end();
    return;
  }
  const document = DOCUMENTS[name as keyof typeof DOCUMENTS];

  // Same wait as /api/public/app_config: answering from a cold provider would
  // show a partner's visitor the house page.
  await awaitServerBrandingReady();
  const partnerUrl = resolveServerBranding(brandHostFromHeaders(req.headers))?.[document.link];

  if (partnerUrl) {
    // Not cached, so clearing or changing the link takes effect at once.
    res.setHeader('Cache-Control', 'no-store');
    res.redirect(307, partnerUrl);
    return;
  }
  const html = housePage(document.file);
  if (html === null) {
    res.status(404).end();
    return;
  }
  // The headers the static handler gave these files.
  res.setHeader('Content-Type', 'text/html; charset=utf-8');
  res.setHeader('Cache-Control', 'public, max-age=0');
  res.status(200).send(html);
}
