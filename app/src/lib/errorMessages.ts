import { getBrandTitle } from '@hooks/useTenantBranding';

// 429 is gated on the upstream message mentioning "budget" so future
// rate-limit emitters don't accidentally surface the budget copy. The
// `fallback` arg is expected to carry the upstream's clean message via
// `parseHttpResponseBodyMessage` / `errors[0].message`.
export function mapUpstreamError(status: number | undefined, fallback: string): string {
  if (status === 429) {
    if (fallback.toLowerCase().includes('budget')) {
      return `Monthly Budget Limit exceeded for this account. Contact ${getBrandTitle()} Support team.`;
    }
    return fallback || 'Rate limit exceeded. Please retry shortly.';
  }
  if (status === 403) return 'You do not have permission to perform this action.';
  if (status === 401) return 'Authentication required. Please sign in again.';
  if (typeof status === 'number' && status >= 500) {
    return 'Service is temporarily unavailable. Please retry.';
  }
  return fallback;
}

export function getUpstreamStatus(graphqlResponse: unknown): number | undefined {
  const errors = (graphqlResponse as { errors?: Array<{ extensions?: { upstream?: { status?: unknown } } }> })?.errors;
  if (!Array.isArray(errors) || errors.length === 0) return undefined;
  const status = errors[0]?.extensions?.upstream?.status;
  return typeof status === 'number' ? status : undefined;
}
