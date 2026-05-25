/**
 * Markdown — DS V2 of legacy MarkDowns.
 * Spec: app/design-system/primitives/data-display/markdown.html
 *
 * Markdown renderer with KaTeX and code-highlight support. Trust boundary:
 * input is always treated as untrusted and sanitised; raw HTML is never
 * rendered.
 *
 * Variants per spec:
 *   surface       = 'chat' | 'docs' | 'inspector' | 'runbook'
 *   features      = subset of ['katex', 'code-highlight', 'task-list', 'footnotes']
 *   linkBehaviour = 'in-app' | 'new-tab'
 *
 * Don't (per spec):
 *   - Don't render trusted HTML through Markdown — that's an XSS vector.
 *   - Don't customise the styling per surface. The whole point of `surface` is
 *     that the design system controls density / typography / link affordances.
 *
 * Migration:
 *   `import MarkDowns from '@common/MarkDowns'`
 * → `import { Markdown } from '@components1/ds/Markdown'`
 *   Capitalisation fix; per-call CSS overrides removed in favour of the
 *   `surface` axis. Internally delegates to the legacy MarkDowns parser
 *   (KaTeX / code-highlight / charts already wired) but exposes only the V2
 *   prop shape and applies surface-based --ds-* tokens around the rendered
 *   output.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import LegacyMarkDowns from '@common/MarkDowns';

export type MarkdownSurface = 'chat' | 'docs' | 'inspector' | 'runbook';
export type MarkdownFeature = 'katex' | 'code-highlight' | 'task-list' | 'footnotes';
export type MarkdownLinkBehaviour = 'in-app' | 'new-tab';

export interface MarkdownProps {
  source: string;
  surface?: MarkdownSurface;
  features?: MarkdownFeature[];
  linkBehaviour?: MarkdownLinkBehaviour;
  className?: string;
  id?: string;
}

interface SurfaceTokens {
  fontSize: string;
  lineHeight: number;
  density: string;
  proseColor: string;
  codeBg: string;
}

const SURFACE_TOKENS: Record<MarkdownSurface, SurfaceTokens> = {
  chat: {
    fontSize: 'var(--ds-text-body)',
    lineHeight: 1.55,
    density: 'var(--ds-space-3)',
    proseColor: 'var(--ds-gray-800)',
    codeBg: 'var(--ds-background-200)',
  },
  docs: {
    fontSize: 'var(--ds-text-body)',
    lineHeight: 1.7,
    density: 'var(--ds-space-4)',
    proseColor: 'var(--ds-gray-800)',
    codeBg: 'var(--ds-background-200)',
  },
  inspector: {
    fontSize: 'var(--ds-text-small)',
    lineHeight: 1.5,
    density: 'var(--ds-space-2)',
    proseColor: 'var(--ds-gray-700)',
    codeBg: 'var(--ds-background-200)',
  },
  runbook: {
    fontSize: 'var(--ds-text-body)',
    lineHeight: 1.6,
    density: 'var(--ds-space-3)',
    proseColor: 'var(--ds-gray-800)',
    codeBg: 'var(--ds-background-200)',
  },
};

export function Markdown({
  source,
  surface = 'chat',
  // features and linkBehaviour are part of the V2 contract; the legacy parser
  // already enables KaTeX / code-highlight by default. We accept them for
  // forward-compat — once the legacy renderer accepts a feature flag, route
  // them through. For now they're documented + reserved.
  features,
  linkBehaviour = 'in-app',
  className,
  id,
}: MarkdownProps) {
  const tokens = SURFACE_TOKENS[surface];

  // Spec: linkBehaviour='new-tab' opens external links in a new tab; 'in-app'
  // delegates to a host-provided handler. The legacy parser exposes
  // `onLinkClick` for the in-app routing case.
  const handleLink = React.useCallback(
    (href: string, ev: React.MouseEvent) => {
      if (linkBehaviour === 'new-tab') return; // default <a target=_blank> behaviour kept by parser
      // in-app: prevent default + emit a navigation intent. Hosts can listen
      // via a custom event since component-new isn't wired into the router.
      ev.preventDefault();
      window.dispatchEvent(new CustomEvent('ds:markdown-link', { detail: { href } }));
    },
    [linkBehaviour]
  );

  return (
    <Box
      id={id}
      className={className}
      data-ds-surface={surface}
      data-ds-features={features?.join(' ')}
      sx={{
        fontFamily: 'var(--ds-font-sans)',
        fontSize: tokens.fontSize,
        lineHeight: tokens.lineHeight,
        color: tokens.proseColor,
        '& p': { margin: `0 0 ${tokens.density} 0` },
        '& p:last-child': { marginBottom: 0 },
        '& h1, & h2, & h3, & h4': {
          margin: `${tokens.density} 0 var(--ds-space-2) 0`,
          fontWeight: 'var(--ds-font-weight-semibold)',
          color: 'var(--ds-gray-900)',
        },
        '& code': {
          fontFamily: 'var(--ds-font-mono)',
          fontSize: '0.9em',
          padding: '1px 5px',
          borderRadius: 'var(--ds-radius-xs)',
          backgroundColor: tokens.codeBg,
          color: 'var(--ds-gray-800)',
        },
        '& pre': {
          fontFamily: 'var(--ds-font-mono)',
          fontSize: 'var(--ds-text-small)',
          lineHeight: 1.5,
          padding: 'var(--ds-space-3)',
          backgroundColor: tokens.codeBg,
          borderRadius: 'var(--ds-radius-sm)',
          overflowX: 'auto',
          margin: `0 0 ${tokens.density} 0`,
        },
        '& pre code': { padding: 0, background: 'transparent', borderRadius: 0 },
        '& a': {
          color: 'var(--ds-blue-600)',
          textDecoration: 'none',
          '&:hover': { textDecoration: 'underline' },
        },
        '& ul, & ol': { paddingLeft: 'var(--ds-space-5)', margin: `0 0 ${tokens.density} 0` },
        '& li': { marginBottom: 'var(--ds-space-1)' },
        '& blockquote': {
          borderLeft: '3px solid var(--ds-gray-300)',
          paddingLeft: 'var(--ds-space-3)',
          color: 'var(--ds-gray-600)',
          margin: `0 0 ${tokens.density} 0`,
        },
        '& table': {
          borderCollapse: 'collapse',
          fontSize: 'var(--ds-text-small)',
          margin: `0 0 ${tokens.density} 0`,
        },
        '& th, & td': { padding: 'var(--ds-space-2) var(--ds-space-3)', border: '1px solid var(--ds-gray-200)' },
        '& th': { backgroundColor: 'var(--ds-background-200)', fontWeight: 'var(--ds-font-weight-semibold)' },
      }}
    >
      <LegacyMarkDowns data={source} onLinkClick={handleLink} />
    </Box>
  );
}

export default Markdown;
