import { fireEvent, render, screen } from '@testing-library/react';
import { isSafeLinkHref, Link } from './Link';

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

  it.each(['#details&foo=bar', 'foo&bar'])('accepts safe relative href containing an ampersand: %s', (href) => {
    expect(isSafeLinkHref(href)).toBe(true);
  });

  it('prevents unsafe navigation without suppressing the click callback', () => {
    const onClick = jest.fn();
    render(
      <Link href='javascript:alert(1)' onClick={onClick}>
        Unsafe destination
      </Link>
    );

    const link = screen.getByRole('link', { name: 'Unsafe destination' });
    expect(link).toHaveAttribute('href', '#');
    expect(fireEvent.click(link)).toBe(false);
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
