import { isSafeLinkHref } from './Link';

describe('isSafeLinkHref', () => {
  it.each(['/workflow/1', 'https://example.com/path', 'mailto:user@example.com', 'tel:+15551234567', '#details'])('accepts safe href %s', (href) => {
    expect(isSafeLinkHref(href)).toBe(true);
  });

  it.each(['', '   ', 'javascript:alert(1)', 'data:text/html,<script>alert(1)</script>', 'javascript&colon;alert(1)', 'java&#x73;cript:alert(1)'])(
    'rejects unsafe or unusable href %s',
    (href) => {
      expect(isSafeLinkHref(href)).toBe(false);
    }
  );

  it('allows ampersands in a query string', () => {
    expect(isSafeLinkHref('/search?q=one&sort=asc')).toBe(true);
  });
});
