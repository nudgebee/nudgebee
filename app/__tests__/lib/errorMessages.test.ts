jest.mock('@hooks/useTenantBranding', () => ({ getBrandTitle: () => 'Nudgebee' }));

import { getUpstreamStatus, mapUpstreamError } from '@lib/errorMessages';

describe('mapUpstreamError', () => {
  it('maps 429 to the branded budget message when the fallback mentions budget', () => {
    expect(mapUpstreamError(429, 'budget: monthly budget limit exceeded for your organization')).toBe(
      'Monthly Budget Limit exceeded for this account. Contact Nudgebee Support team.'
    );
  });

  it('preserves the upstream fallback for a non-budget 429 (plain rate-limit)', () => {
    expect(mapUpstreamError(429, 'too many requests, retry in 30s')).toBe('too many requests, retry in 30s');
  });

  it('returns a generic rate-limit message for 429 with no fallback', () => {
    expect(mapUpstreamError(429, '')).toBe('Rate limit exceeded. Please retry shortly.');
  });

  it('maps 403 to a generic permission-denied message', () => {
    expect(mapUpstreamError(403, 'fallback')).toBe('You do not have permission to perform this action.');
  });

  it('maps 401 to a sign-in message', () => {
    expect(mapUpstreamError(401, 'fallback')).toBe('Authentication required. Please sign in again.');
  });

  it.each([500, 502, 503, 504])('maps %s to the service-unavailable message', (status) => {
    expect(mapUpstreamError(status, 'fallback')).toBe('Service is temporarily unavailable. Please retry.');
  });

  it('returns the fallback for 4xx codes outside the mapped set', () => {
    expect(mapUpstreamError(400, 'bad request reason')).toBe('bad request reason');
    expect(mapUpstreamError(404, 'not found reason')).toBe('not found reason');
  });

  it('returns the fallback for undefined status', () => {
    expect(mapUpstreamError(undefined, 'fallback string')).toBe('fallback string');
  });
});

describe('getUpstreamStatus', () => {
  it('reads errors[0].extensions.upstream.status from a graphql response', () => {
    const resp = { errors: [{ extensions: { upstream: { status: 429 } } }] };
    expect(getUpstreamStatus(resp)).toBe(429);
  });

  it('returns undefined when the response has no errors array', () => {
    expect(getUpstreamStatus({ data: {} })).toBeUndefined();
    expect(getUpstreamStatus(undefined)).toBeUndefined();
    expect(getUpstreamStatus(null)).toBeUndefined();
  });

  it('returns undefined when extensions.upstream is missing', () => {
    expect(getUpstreamStatus({ errors: [{ message: 'x' }] })).toBeUndefined();
    expect(getUpstreamStatus({ errors: [{ extensions: { code: 'FORBIDDEN' } }] })).toBeUndefined();
  });

  it('returns undefined when status is not a number', () => {
    expect(getUpstreamStatus({ errors: [{ extensions: { upstream: { status: '429' } } }] })).toBeUndefined();
  });
});
