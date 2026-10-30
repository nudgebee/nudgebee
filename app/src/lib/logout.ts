import { getSession, signOut } from 'next-auth/react';
import { getUserSession } from '@lib/auth';

/**
 * Sign the user out, and out of their identity provider too when the session
 * says the IdP must be told (`session.idpLogoutPath`).
 *
 * Always one NextAuth sign-out that navigates, as before; only its callback
 * changes. Signing out without navigating would mark the session gone while
 * the page is still open, and the page's own "session lost" redirect would
 * then race the browser's trip to the IdP.
 */
export async function logout(callbackUrl = '/'): Promise<void> {
  const idpCallback = await idpLogoutCallback();
  await signOut({ callbackUrl: idpCallback ?? callbackUrl });
}

async function idpLogoutCallback(): Promise<string | null> {
  try {
    // The session the page already holds; fetched only off an authenticated page.
    const session = ((getUserSession() as { idpLogoutPath?: string } | undefined) ?? (await getSession())) as {
      idpLogoutPath?: string;
    } | null;
    if (!session?.idpLogoutPath) {
      return null;
    }
    const res = await fetch(session.idpLogoutPath, { method: 'POST' });
    if (!res.ok) {
      console.warn(`[logout] IdP logout skipped — logout URL request returned ${res.status}`);
      return null;
    }
    const body = await res.json();
    return typeof body?.url === 'string' ? body.url : null;
  } catch (err) {
    console.warn('[logout] IdP logout skipped — could not fetch the logout URL', err);
    return null;
  }
}
