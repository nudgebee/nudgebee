/**
 * Link — DS V2. Replaces legacy CustomLink.
 * Spec: app/design-system/primitives/navigation/link.html
 *
 * A small Next.js `<Link>` wrapper with the DS primary text color and an
 * optional `OpenInNew` external-link icon. For the "Ticket - {id}" pattern
 * see `CustomTicketLink` (a domain composition built on this primitive).
 * For tabbed page navigation with hash-fragment routing, see `AnchorComponent`
 * (a separate page-navigation primitive — NOT a Link variant).
 *
 * Don't (per spec):
 *   - Don't use Link for actions. Actions are Buttons.
 *   - Don't use Link with onClick alone (no href). Use a `tone='link'` Button.
 *   - Don't introduce custom underline styles. The DS spec preserves the
 *     "no underline by default" convention for inline links inside dense UI.
 */
import * as React from 'react';
import NextLink from 'next/link';
import Tooltip from '@ui/Tooltip';
import { ds } from '@utils/colors';

export interface LinkProps {
  href: string;
  children: React.ReactNode;
  style?: React.CSSProperties;
  onClick?: (e: React.MouseEvent<HTMLAnchorElement>) => void;
  /** Forwarded to next/link as `prop` (legacy passthrough). */
  prop?: unknown;
  target?: string;
  /** When true, opens in a new tab and renders an external-link icon. */
  openInNew?: boolean;
  /** sx overrides for the trailing `OpenInNew` icon. */
  OpenInNewIconSx?: React.CSSProperties;
  /** Smaller font size — for inline links in captions / dense layouts. */
  secondaryText?: boolean;
  /** When set, truncates the link text with ellipsis and shows a tooltip with the full text on hover. */
  maxWidth?: string;
}

const SAFE_LINK_PROTOCOLS = new Set(['http:', 'https:', 'mailto:', 'tel:']);

export const isSafeLinkHref = (href: string): boolean => {
  if (typeof href !== 'string' || href.trim() === '') return false;

  const trimmed = href.trim();
  if (trimmed.startsWith('//') || trimmed.startsWith('/\\') || trimmed.startsWith('\\')) return false;
  if (trimmed.startsWith('/') || trimmed.startsWith('#') || trimmed.startsWith('?')) return true;

  const ampersandIndex = trimmed.indexOf('&');
  if (ampersandIndex !== -1) {
    const firstDelimiter = trimmed.search(/[?:/]/);
    if ((firstDelimiter === -1 || ampersandIndex < firstDelimiter) && /&[a-zA-Z0-9#]+;/.test(trimmed)) return false;
  }

  try {
    return SAFE_LINK_PROTOCOLS.has(new URL(trimmed, 'https://nudgebee.invalid').protocol);
  } catch {
    return false;
  }
};

export function Link({
  href,
  children,
  style,
  onClick,
  prop,
  target = '_self',
  secondaryText = false,
  openInNew = false,
  OpenInNewIconSx = {},
  maxWidth,
}: LinkProps) {
  const normalizedHref = typeof href === 'string' ? href.trim() : '';
  const hasSafeHref = isSafeLinkHref(normalizedHref);
  const safeHref = hasSafeHref ? normalizedHref : '#';

  const handleClick = (e: React.MouseEvent<HTMLAnchorElement>) => {
    e.stopPropagation();
    if (!hasSafeHref) {
      e.preventDefault();
    }
    onClick?.(e);
  };

  const link = (
    <NextLink
      passHref
      href={safeHref}
      onClick={handleClick}
      // @ts-expect-error legacy passthrough — preserved for any callers that depended on it
      prop={prop}
      target={openInNew ? '_blank' : target}
      rel={openInNew || target === '_blank' ? 'noopener noreferrer' : undefined}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: ds.space[0],
        fontSize: secondaryText ? 'var(--ds-text-caption)' : 'var(--ds-text-body)',
        fontWeight: 'var(--ds-font-weight-regular)',
        color: 'var(--ds-blue-600)',
        textDecoration: 'none',
        ...(maxWidth && { maxWidth, minWidth: 0 }),
        ...style,
      }}
    >
      <span style={maxWidth ? { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 } : undefined}>{children}</span>
      {openInNew && (
        <svg
          width='12'
          height='12'
          viewBox='0 0 24 24'
          fill='none'
          stroke='currentColor'
          strokeWidth='2'
          style={{ flexShrink: 0, ...OpenInNewIconSx }}
        >
          <path d='M7 17l9.2-9.2M17 17V7H7' />
        </svg>
      )}
    </NextLink>
  );

  if (maxWidth) {
    return <Tooltip title={children}>{link}</Tooltip>;
  }

  return link;
}

export default Link;
