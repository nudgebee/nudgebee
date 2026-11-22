import { attachmentResponseHeaders } from '@lib/attachmentHeaders';

describe('attachmentResponseHeaders', () => {
  it.each([
    ['image/png', 'image/png'],
    ['IMAGE/JPEG; charset=binary', 'image/jpeg'],
    ['image/gif', 'image/gif'],
    ['image/webp', 'image/webp'],
  ])('serves raster image %s inline as %s', (upstream, served) => {
    const h = attachmentResponseHeaders(upstream);
    expect(h['Content-Type']).toBe(served);
    expect(h['Content-Disposition']).toBe('inline');
  });

  // Anything that can carry script, or an unknown type, must never render on the app origin.
  it.each([
    'text/html',
    'text/html; charset=utf-8',
    'image/svg+xml',
    'application/xhtml+xml',
    'text/xml',
    'application/javascript',
    'application/pdf',
    'image/png, text/html',
    '',
    null,
  ])('forces %p to download', (upstream) => {
    const h = attachmentResponseHeaders(upstream);
    expect(h['Content-Type']).toBe('application/octet-stream');
    expect(h['Content-Disposition']).toBe('attachment');
  });

  it('always sends nosniff and a sandboxing CSP', () => {
    for (const upstream of ['image/png', 'text/html', null]) {
      const h = attachmentResponseHeaders(upstream);
      expect(h['X-Content-Type-Options']).toBe('nosniff');
      expect(h['Content-Security-Policy']).toContain('sandbox');
    }
  });

  it.each([
    ['application/pdf', 'attachment; filename="invoice.pdf"', 'attachment; filename="invoice.pdf"'],
    ['image/png', 'inline; filename="screenshot.png"', 'inline; filename="screenshot.png"'],
    ['text/plain', "attachment; filename*=UTF-8''error%20log.txt", 'attachment; filename="error log.txt"'],
    ['text/plain', 'attachment; filename=notes.txt', 'attachment; filename="notes.txt"'],
  ])('keeps the upstream filename for %s (%s)', (type, disposition, expected) => {
    expect(attachmentResponseHeaders(type, disposition)['Content-Disposition']).toBe(expected);
  });

  // The filename is upstream-controlled: it must not break out of the quoted header value.
  it.each([
    ['attachment; filename="a\r\nSet-Cookie: x=1.txt"', 'attachment; filename="a__Set-Cookie_ x_1.txt"'],
    ["attachment; filename*=UTF-8''%22%3B%20x%3D.html", 'attachment; filename="__ x_.html"'],
    ['attachment; filename="../../etc/passwd"', 'attachment; filename="_.._etc_passwd"'],
    ['attachment; filename="..."', 'attachment'],
    ["attachment; filename*=UTF-8''%E0%A4%A", 'attachment'],
    [null, 'attachment'],
  ])('sanitises filename from %p', (disposition, expected) => {
    expect(attachmentResponseHeaders('text/html', disposition)['Content-Disposition']).toBe(expected);
  });
});
