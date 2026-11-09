// Response headers for ticket attachment bytes proxied from a third party
// (ServiceNow, Jira, ...). The upstream Content-Type is attacker-controlled:
// anyone who can comment on the ticket in the external system picks it. Passing
// it through let an uploaded text/html or image/svg+xml file run script on the
// app's origin when opened. Only raster images render inline; everything else
// is served as an opaque download, and the response can never execute.
const INLINE_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/bmp']);

// The upstream filename is attacker-controlled too, so it is rebuilt from a
// conservative character set rather than passed through: no quotes, no
// separators, no control characters that could break out of the header.
function safeFilename(upstreamContentDisposition: string | null): string {
  const disposition = upstreamContentDisposition || '';
  let name = '';
  const extended = /filename\*\s*=\s*[^']*'[^']*'([^;]+)/i.exec(disposition);
  if (extended) {
    try {
      name = decodeURIComponent(extended[1].trim());
    } catch {
      name = '';
    }
  }
  if (!name) {
    const plain = /filename\s*=\s*(?:"([^"]*)"|([^;]+))/i.exec(disposition);
    name = (plain?.[1] ?? plain?.[2] ?? '').trim();
  }
  name = name
    .replace(/[^A-Za-z0-9._ ()-]/g, '_')
    .replace(/^[ .]+/, '')
    .slice(0, 150)
    .trim();
  return /[A-Za-z0-9]/.test(name) ? name : '';
}

export function attachmentResponseHeaders(
  upstreamContentType: string | null,
  upstreamContentDisposition: string | null = null
): Record<string, string> {
  const base = (upstreamContentType || '').split(';')[0].trim().toLowerCase();
  const inline = INLINE_IMAGE_TYPES.has(base);
  const filename = safeFilename(upstreamContentDisposition);
  const disposition = inline ? 'inline' : 'attachment';
  return {
    'Content-Type': inline ? base : 'application/octet-stream',
    'Content-Disposition': filename ? `${disposition}; filename="${filename}"` : disposition,
    'X-Content-Type-Options': 'nosniff',
    'Content-Security-Policy': "default-src 'none'; sandbox",
  };
}
