import * as React from 'react';
import Head from 'next/head';
import { useRouter } from 'next/router';
import { useSession } from 'next-auth/react';
import Loader from '@shared/Loader';
import cache from '@lib/cache';

/**
 * Landing page for tenant invitation links (see api-server user/service.go
 * sendTenantInvitationEmail, which points the email CTA at
 * `/accept-invite?tenant=<name>`).
 *
 * The bug this fixes: a user already signed into tenant A who clicks an invite
 * to tenant B used to land on tenant A, because the invite link carried no
 * target and nothing switched the active tenant. Here we read the invited
 * tenant name and, when it differs from the session's current tenant, switch to
 * it using the exact same session-update path as the in-app tenant switcher
 * (SwitchTenant.jsx). The server resolves the name -> tenant and re-validates
 * membership; a stale/renamed name simply no-ops and the user falls back to
 * their default tenant (which the invite already set server-side).
 */
function AcceptInvite() {
  const router = useRouter();
  // `required: true` bounces unauthenticated users to sign-in with this full
  // URL (incl. the ?tenant= query) as callbackUrl, so they return here after
  // logging in.
  const { data: session, update } = useSession({ required: true });
  const handledRef = React.useRef(false);

  React.useEffect(() => {
    if (!router.isReady || !session?.user || handledRef.current) {
      return;
    }
    handledRef.current = true;

    const raw = router.query.tenant;
    const invitedTenant = (Array.isArray(raw) ? raw[0] : raw)?.trim();
    const currentTenant = (session as { tenant?: { name?: string } }).tenant?.name;

    async function go() {
      // Tenant names are unique + case-insensitive (citext); only switch when
      // the invite targets a genuinely different tenant than the active one.
      const needsSwitch = !!invitedTenant && (!currentTenant || invitedTenant.toLowerCase() !== currentTenant.toLowerCase());
      if (needsSwitch) {
        try {
          await update({ ...session, tenantName: invitedTenant });
          cache.clear();
        } catch {
          // Swallow: fall through to /home on the user's current/default tenant.
        }
      }
      // Full navigation (not router.push) so stale account scopes from the
      // previous tenant aren't carried over — mirrors SwitchTenant.jsx.
      window.location.href = '/home';
    }
    go();
  }, [router.isReady, router.query.tenant, session, update]);

  return (
    <>
      <Head>
        <title>Nudgebee: Accepting invitation</title>
      </Head>
      <Loader />
    </>
  );
}

export default AcceptInvite;
