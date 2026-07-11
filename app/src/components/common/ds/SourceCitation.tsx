/**
 * SourceCitation — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/agentic/source-citation.html
 *
 * Inline attribution for any agent-generated claim. Always clickable (opens an
 * Inspector with the full query / response). Never dismissible — provenance is
 * part of the claim, not an annotation.
 *
 * Per D10: source citations render in two places per response — numbered
 * footnotes inline in the prose ([1], [2]…) PLUS a "Sources" footer below the
 * response. Footer shows the first four chips; the rest collapse into "+ N more".
 *
 * Variants per spec:
 *   source      = 'prometheus' | 'loki' | 'k8s' | 'aws' | 'gcp' | 'github' |
 *                 'grafana' | 'runbook' | string  (open-ended; registry below)
 *   composition = 'name' | 'name+timestamp' | 'icon+name' | 'icon+name+timestamp' | 'number'
 *                 (auto from `timestamp` + `number` props presence)
 *   size        = 'xs' | 'sm'
 *
 * Don't (per spec):
 *   - Don't render an unsourced agent claim. If the data has no provenance,
 *     the agent shouldn't say it.
 *   - Don't tone the citation by source health. Citation marks where the answer
 *     came from, not its quality. A failed source belongs in a Banner.
 *   - Don't deduplicate citations across the response — Loki at 10:24 and Loki
 *     at 10:31 are two different reads.
 */
import * as React from 'react';
import { Box, ButtonBase } from '@mui/material';

export type SourceKey = 'prometheus' | 'loki' | 'k8s' | 'aws' | 'gcp' | 'github' | 'grafana' | 'runbook' | string;
export type SourceCitationComposition = 'name' | 'name+timestamp' | 'icon+name' | 'icon+name+timestamp' | 'number';
export type SourceCitationSize = 'xs' | 'sm';

export interface SourceCitationProps {
  source: SourceKey;
  /** Override the display label. Defaults to a registry value derived from `source`. */
  label?: string;
  /** Optional timestamp. Renders as relative ("2 m ago") via `formatRelativeTime` if a Date/number. */
  timestamp?: Date | number | string;
  /** For composition='number': the footnote number ([1], [2], etc.). */
  number?: number;
  composition?: SourceCitationComposition;
  size?: SourceCitationSize;
  /** Click target — opens an Inspector with the full query/response per spec. */
  href?: string;
  onClick?: (e: React.MouseEvent<HTMLElement>) => void;
  className?: string;
  id?: string;
}

interface SourceMeta {
  label: string;
  marker: string; // single-letter token shown in the marker square
  markerBg: string; // marker background color
  markerFg: string; // marker foreground color
}

// Source registry. Open-ended — unknown sources fall back to a neutral marker
// derived from the first letter of the source key.
const SOURCE_REGISTRY: Record<string, SourceMeta> = {
  prometheus: { label: 'Prometheus', marker: 'P', markerBg: 'var(--ds-red-200)', markerFg: 'var(--ds-red-700)' },
  loki: { label: 'Loki', marker: 'L', markerBg: 'var(--ds-blue-200)', markerFg: 'var(--ds-blue-700)' },
  k8s: { label: 'K8s API', marker: 'K', markerBg: 'var(--ds-blue-200)', markerFg: 'var(--ds-blue-700)' },
  aws: { label: 'AWS', marker: 'A', markerBg: 'var(--ds-amber-200)', markerFg: 'var(--ds-amber-700)' },
  gcp: { label: 'GCP', marker: 'G', markerBg: 'var(--ds-blue-200)', markerFg: 'var(--ds-blue-700)' },
  github: { label: 'GitHub', marker: 'G', markerBg: 'var(--ds-gray-200)', markerFg: 'var(--ds-gray-700)' },
  grafana: { label: 'Grafana', marker: 'G', markerBg: 'var(--ds-amber-200)', markerFg: 'var(--ds-amber-700)' },
  runbook: { label: 'Runbook', marker: 'R', markerBg: 'var(--ds-gray-200)', markerFg: 'var(--ds-gray-700)' },
};

function metaFor(source: SourceKey, labelOverride?: string): SourceMeta {
  const known = SOURCE_REGISTRY[source];
  if (known) return labelOverride ? { ...known, label: labelOverride } : known;
  // Fallback for unknown sources — first letter as marker, neutral colors
  const first = (source || '?').slice(0, 1).toUpperCase();
  return {
    label: labelOverride ?? source,
    marker: first,
    markerBg: 'var(--ds-gray-200)',
    markerFg: 'var(--ds-gray-700)',
  };
}

function formatRelativeTime(ts: Date | number | string): string {
  const d = ts instanceof Date ? ts : new Date(ts);
  if (Number.isNaN(d.getTime())) return '';
  const diffMs = Date.now() - d.getTime();
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return `${sec} s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} h ago`;
  const day = Math.floor(hr / 24);
  return `${day} d ago`;
}

const SIZE_TOKENS: Record<SourceCitationSize, { fontSize: string; padX: string; padY: string; markerSize: number; gap: string }> = {
  xs: { fontSize: 'var(--ds-text-caption)', padX: '6px', padY: '1px', markerSize: 14, gap: '4px' },
  sm: { fontSize: 'var(--ds-text-small)', padX: '8px', padY: '2px', markerSize: 16, gap: '6px' },
};

function deriveComposition(p: SourceCitationProps): SourceCitationComposition {
  if (p.composition) return p.composition;
  if (p.number !== undefined) return 'number';
  if (p.timestamp !== undefined) return 'icon+name+timestamp';
  return 'icon+name';
}

export function SourceCitation(props: SourceCitationProps) {
  const { source, label, timestamp, number, size = 'sm', href, onClick, className, id } = props;
  const composition = deriveComposition(props);
  const tokens = SIZE_TOKENS[size];
  const meta = metaFor(source, label);

  const showIcon = composition === 'icon+name' || composition === 'icon+name+timestamp';
  const showName = composition !== 'number';
  const showTimestamp = composition === 'name+timestamp' || composition === 'icon+name+timestamp';
  const showNumber = composition === 'number';

  const timestampText = timestamp !== undefined ? formatRelativeTime(timestamp) : '';

  const baseSx = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: tokens.gap,
    paddingLeft: tokens.padX,
    paddingRight: tokens.padX,
    paddingTop: tokens.padY,
    paddingBottom: tokens.padY,
    fontSize: tokens.fontSize,
    fontWeight: 'var(--ds-font-weight-medium)',
    lineHeight: 1.4,
    color: 'var(--ds-gray-700)',
    backgroundColor: 'var(--ds-background-100)',
    border: '1px solid var(--ds-gray-200)',
    borderRadius: 'var(--ds-radius-pill)',
    textDecoration: 'none',
    cursor: 'pointer',
    transition: 'border-color var(--ds-motion-micro) var(--ds-motion-ease), background-color var(--ds-motion-micro) var(--ds-motion-ease)',
    '&:hover': {
      borderColor: 'var(--ds-gray-400)',
      backgroundColor: 'var(--ds-gray-100)',
    },
    '&.Mui-focusVisible': {
      outline: '2px solid var(--ds-blue-500)',
      outlineOffset: '1px',
    },
    ...(showNumber && {
      fontFamily: 'var(--ds-font-mono)',
      paddingLeft: '6px',
      paddingRight: '6px',
    }),
  };

  const content = (
    <>
      {showIcon && (
        <Box
          component='span'
          aria-hidden='true'
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            width: tokens.markerSize,
            height: tokens.markerSize,
            borderRadius: 'var(--ds-radius-sm)',
            backgroundColor: meta.markerBg,
            color: meta.markerFg,
            fontSize: '10px',
            fontWeight: 'var(--ds-font-weight-semibold)',
            flexShrink: 0,
          }}
        >
          {meta.marker}
        </Box>
      )}
      {showName && (
        <Box component='span' sx={{ color: 'var(--ds-gray-700)' }}>
          {meta.label}
        </Box>
      )}
      {showTimestamp && timestampText && (
        <Box component='span' sx={{ color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-regular)' }}>
          · {timestampText}
        </Box>
      )}
      {showNumber && number !== undefined && (
        <Box component='span' sx={{ color: 'var(--ds-gray-700)' }}>
          [{number}]
        </Box>
      )}
    </>
  );

  // Always clickable per spec; render as anchor if href, otherwise button
  if (href) {
    return (
      <ButtonBase component='a' href={href} id={id} className={className} onClick={onClick} sx={baseSx}>
        {content}
      </ButtonBase>
    );
  }

  return (
    <ButtonBase id={id} className={className} onClick={onClick} sx={baseSx}>
      {content}
    </ButtonBase>
  );
}

export default SourceCitation;
