/**
 * DEPRECATED ROUTE - backward-compat shim
 * ────────────────────────────────────────────────────────────────────
 *
 * @deprecated  Use /api/integrations/ms-teams/callback instead.
 *
 * MS Teams OAuth callback shim kept so external systems (OAuth providers, cloud
 * marketplaces, etc.) that have the OLD URL registered in their
 * dashboard configs continue to work during the migration window.
 *
 * Forwards to the new handler with HTTP `Deprecation` + `Link`
 * headers (RFC 8594 / RFC 9745). Each call emits a
 * `[deprecated-endpoint]` warn log so ops can confirm the shim is
 * safe to remove (~30 days of zero traffic).
 *
 * See PR #31378 (umbrella #31377) for the /api/* surface restructure.
 */
import { createDeprecatedShim } from '@lib/deprecatedRouteShim';
import handler from '@pages/api/integrations/ms-teams/callback';

export default createDeprecatedShim(handler, '/api/integrations/ms-teams/callback');
