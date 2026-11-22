jest.mock('@hooks/useTenantBranding', () => ({ getBrandTitle: () => 'Acme' }));

import { getGraphqlErrorMessage, mapUpstreamError } from '@lib/errorMessages';

describe('mapUpstreamError budget rejections', () => {
  it('shows the limit the backend named', () => {
    expect(mapUpstreamError(429, 'budget: daily investigation count limit exceeded for this account')).toBe(
      'Daily investigation count limit exceeded for this account. Contact Acme Support team.'
    );
  });

  it('does not double the period or lead with one', () => {
    expect(mapUpstreamError(429, 'budget: monthly budget limit exceeded for this account.')).toBe(
      'Monthly budget limit exceeded for this account. Contact Acme Support team.'
    );
    expect(mapUpstreamError(429, 'budget:')).toBe('Budget limit exceeded. Contact Acme Support team.');
  });

  it('passes a non-budget 429 through', () => {
    expect(mapUpstreamError(429, 'slow down')).toBe('slow down');
  });

  it('keeps the generic copy for a 5xx', () => {
    expect(mapUpstreamError(500, 'sql: Scan error')).toBe('Service is temporarily unavailable. Please retry.');
  });
});

describe('getGraphqlErrorMessage', () => {
  it('is null when the response carries no errors', () => {
    expect(getGraphqlErrorMessage({ data: { data: { ok: true } } }, 'fallback')).toBeNull();
    expect(getGraphqlErrorMessage(null, 'fallback')).toBeNull();
  });

  it('names the limit for a budget 429 returned as a resolved response', () => {
    const res = {
      data: {
        errors: [
          {
            message: 'budget: daily investigation count limit exceeded for your organization',
            extensions: { upstream: { status: 429 } },
          },
        ],
      },
    };
    expect(getGraphqlErrorMessage(res, 'fallback')).toBe(
      'Daily investigation count limit exceeded for your organization. Contact Acme Support team.'
    );
  });

  it('uses the fallback when the error has no message and no status', () => {
    expect(getGraphqlErrorMessage({ data: { errors: [{}] } }, 'fallback')).toBe('fallback');
  });
});
