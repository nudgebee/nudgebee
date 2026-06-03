/**
 * Tabs — DS V2.
 *
 * Phase 1: functionality only. Visual styling is deferred to Phase 2.
 *
 * Consolidates `CustomTabs` and `CustomTabsForDrilldown` from
 * `@components1/common/`. `ButtonTabs` is out of scope for this phase.
 *
 * Migration plan: design-system/tabs-migration-plan.md
 *
 * Don't (per plan):
 *   - Don't tone the whole tab. Apply tone to its count only via `TabItem.countTone`.
 *   - Don't render tab content inside `Tabs`. Tabs emits `onChange`; pages render.
 *   - Don't add a dark-pill 'primary' variant. Page-level nav lives in PageTabs.
 */
import * as React from 'react';
import { Box, Tabs as MuiTabs, Tab as MuiTab } from '@mui/material';
import Image from 'next/image';
import { useRouter } from 'next/router';
import MoreHorizIcon from '@mui/icons-material/MoreHoriz';
import { Label, type LabelTone } from './Label';
import { DropdownMenu } from './DropdownMenu';
import { ds } from '@utils/colors';

export type TabId = string;
export type TabsSize = 'sm' | 'md';
export type TabsNavigation = 'state' | 'router';
export type TabsRouterMode = 'query' | 'hash';
export type TabsOverflow = 'scroll' | 'more-menu';

/** Static-import, URL string, or React-element icon. Matches `@assets` exports. */
export type IconLike = React.ReactNode | { src: string; height?: number; width?: number } | string;

export interface TabItem {
  /** Stable selection key. */
  id: TabId;
  /** Visible label. */
  label: React.ReactNode;
  /** Optional leading or trailing icon. */
  icon?: IconLike;
  /** Icon side. Default 'start'. */
  iconPosition?: 'start' | 'end';
  /** Numeric badge after the label. Clamps to '99+'. */
  count?: number;
  /** Tone for the count badge only. */
  countTone?: LabelTone;
  /** Renders a 'BETA' chip after the label. */
  beta?: boolean;
  disabled?: boolean;
  /** Filtered out before render. */
  hidden?: boolean;
}

export interface TabsProps {
  tabs: TabItem[];
  value: TabId;
  onChange: (next: TabId) => void;
  size?: TabsSize;
  /** URL sync mode. Default 'state'. */
  navigation?: TabsNavigation;
  /** Routing strategy when navigation='router'. Default 'query'. */
  routerMode?: TabsRouterMode;
  /** Query-param key when routerMode='query'. Default 'tab'. */
  routerParam?: string;
  /** Overflow handling. Default 'scroll'. */
  overflow?: TabsOverflow;
  /** Max number of tabs rendered inline when overflow='more-menu'. The rest
   *  collapse into a kebab dropdown (the kebab takes its own slot). */
  visibleTabCount?: number;
  /** Element rendered on the right edge of the strip. */
  rightSlot?: React.ReactNode;
  ariaLabel?: string;
}

const SIZE_TOKENS: Record<TabsSize, { fontSize: string; padX: string; height: string }> = {
  sm: { fontSize: 'var(--ds-text-body)', padX: 'var(--ds-space-3)', height: '32px' },
  md: { fontSize: 'var(--ds-text-body-lg)', padX: 'var(--ds-space-4)', height: '40px' },
};

/* -------------------------------------------------------------------------- */
/*  Icon rendering — supports next/image static imports, URL strings, and     */
/*  React nodes. No SafeIcon dependency.                                      */
/* -------------------------------------------------------------------------- */

function NavIcon({ src, alt, size = 16 }: { src: IconLike; alt: string; size?: number }) {
  if (src == null) return null;
  if (React.isValidElement(src)) {
    return (
      <Box
        component='span'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: size,
          height: size,
          flexShrink: 0,
          '& svg': { width: '100%', height: '100%' },
        }}
      >
        {src}
      </Box>
    );
  }
  const url = typeof src === 'string' ? src : (src as { src?: string })?.src;
  if (!url) return null;
  const isSvg = url.endsWith('.svg');
  return (
    <Box
      component='span'
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: size,
        height: size,
        flexShrink: 0,
      }}
    >
      <Image src={url} alt={alt} width={size} height={size} unoptimized={isSvg} style={{ objectFit: 'contain' }} />
    </Box>
  );
}

function TabContent({ item, size }: { item: TabItem; size: TabsSize }) {
  const iconSize = size === 'sm' ? 14 : 16;
  const iconEnd = item.iconPosition === 'end';
  const altText = typeof item.label === 'string' ? item.label : item.id;
  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
      {item.icon && !iconEnd && <NavIcon src={item.icon} alt={altText} size={iconSize} />}
      <Box component='span' sx={{ fontSize: SIZE_TOKENS[size].fontSize }}>
        {item.label}
      </Box>
      {item.icon && iconEnd && <NavIcon src={item.icon} alt={altText} size={iconSize} />}
      {item.count !== undefined && (
        <Label tone={item.countTone ?? 'neutral'} size='sm'>
          {item.count > 99 ? '99+' : item.count}
        </Label>
      )}
      {item.beta && (
        <Label tone='info' size='sm'>
          BETA
        </Label>
      )}
    </Box>
  );
}

/* -------------------------------------------------------------------------- */
/*  Tabs                                                                      */
/* -------------------------------------------------------------------------- */

export function Tabs({
  tabs,
  value,
  onChange,
  size = 'md',
  navigation = 'state',
  routerMode = 'query',
  routerParam = 'tab',
  overflow = 'scroll',
  visibleTabCount,
  rightSlot,
  ariaLabel,
}: TabsProps) {
  const router = useRouter();
  const isRouter = navigation === 'router';
  const isHashMode = isRouter && routerMode === 'hash';

  const visibleTabs = React.useMemo(() => tabs.filter((t) => !t.hidden), [tabs]);

  // ── A11y: deterministic per-tab ids derived from ariaLabel ────────────
  const ariaSlug = React.useMemo(
    () =>
      (ariaLabel || 'tabs')
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '') || 'tabs',
    [ariaLabel]
  );

  // ── URL → state sync (router mode) ────────────────────────────────────
  // Only syncs state TO URL on user click — never on mount — so the URL is
  // the source of truth on initial load (legacy "don't overwrite on load").
  React.useEffect(() => {
    if (!isRouter || !router.isReady) return;
    let nextFromUrl: string | undefined;
    if (isHashMode) {
      const raw = typeof window !== 'undefined' ? window.location.hash : '';
      const decoded = decodeURIComponent(raw.replace(/^#/, ''));
      nextFromUrl = decoded || undefined;
    } else {
      const fromQuery = router.query[routerParam];
      nextFromUrl = typeof fromQuery === 'string' ? fromQuery : undefined;
    }
    // Only sync if the URL value matches a known tab — otherwise MUI warns
    // and we'd silently mask the deep-link bug.
    if (nextFromUrl !== undefined && nextFromUrl !== value && visibleTabs.some((t) => t.id === nextFromUrl)) {
      onChange(nextFromUrl);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isRouter, isHashMode, router.isReady, router.asPath, routerParam]);

  // Build a hash-mode URL preserving the `accountId` query param (legacy parity).
  const buildHashUrl = (newId: TabId) => {
    const [pathAndQuery = ''] = (router.asPath || '').split('#');
    const [path = '', queryString = ''] = pathAndQuery.split('?');
    const searchParams = new URLSearchParams(queryString);
    if (router.query.accountId && !searchParams.has('accountId')) {
      searchParams.set('accountId', String(router.query.accountId));
    }
    const cleanQuery = searchParams.toString();
    return `${path}${cleanQuery ? `?${cleanQuery}` : ''}#${newId}`;
  };

  const commitChange = (next: TabId) => {
    onChange(next);
    if (!isRouter) return;
    if (isHashMode) {
      router.push(buildHashUrl(next), undefined, { shallow: true });
    } else {
      router.push({ pathname: router.pathname, query: { ...router.query, [routerParam]: next } }, undefined, { shallow: true });
    }
  };

  const handleMuiChange = (_e: React.SyntheticEvent, next: TabId) => commitChange(next);

  // ── Overflow handling ─────────────────────────────────────────────────
  let inlineTabs = visibleTabs;
  let overflowTabs: TabItem[] = [];
  if (overflow === 'more-menu' && visibleTabCount && visibleTabs.length > visibleTabCount) {
    inlineTabs = visibleTabs.slice(0, visibleTabCount);
    overflowTabs = visibleTabs.slice(visibleTabCount);
  }
  // If the controlled value lives in the overflow bucket, swap it into the
  // last inline slot so MUI doesn't error on an unknown value.
  if (overflowTabs.length > 0 && overflowTabs.some((t) => t.id === value)) {
    const selected = overflowTabs.find((t) => t.id === value)!;
    const displaced = inlineTabs[inlineTabs.length - 1];
    inlineTabs = [...inlineTabs.slice(0, -1), selected];
    overflowTabs = [displaced, ...overflowTabs.filter((t) => t.id !== value)];
  }

  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        width: '100%',
        borderBottom: '1px solid var(--ds-gray-200)',
      }}
    >
      <MuiTabs
        value={value}
        onChange={handleMuiChange}
        variant={overflow === 'scroll' ? 'scrollable' : 'standard'}
        scrollButtons={overflow === 'scroll' ? 'auto' : false}
        aria-label={ariaLabel}
        sx={{
          flex: 1,
          minHeight: SIZE_TOKENS[size].height,
          '& .MuiTabs-indicator': {
            display: 'block',
            height: 2,
            backgroundColor: 'var(--ds-blue-500)',
          },
          '& .MuiTabs-flexContainer': { gap: ds.space.mul(0, 3) },
        }}
      >
        {inlineTabs.map((tab) => (
          <MuiTab
            key={tab.id}
            value={tab.id}
            id={`tabs-${ariaSlug}-${tab.id}`}
            disabled={tab.disabled}
            label={<TabContent item={tab} size={size} />}
            sx={{
              minHeight: SIZE_TOKENS[size].height,
              height: SIZE_TOKENS[size].height,
              padding: `0 ${SIZE_TOKENS[size].padX}`,
              textTransform: 'none',
              fontSize: SIZE_TOKENS[size].fontSize,
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-600)',
              '&.Mui-selected': {
                color: 'var(--ds-blue-600)',
                fontWeight: 'var(--ds-font-weight-medium)',
              },
              '&:hover': {
                color: 'var(--ds-blue-600)',
                backgroundColor: 'var(--ds-gray-100)',
              },
            }}
          />
        ))}
      </MuiTabs>
      {overflowTabs.length > 0 && (
        <DropdownMenu
          trigger={
            <Box
              role='button'
              aria-label='More tabs'
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                height: SIZE_TOKENS[size].height,
                width: SIZE_TOKENS[size].height,
                color: 'var(--ds-gray-600)',
                cursor: 'pointer',
                borderRadius: ds.radius.sm,
                flexShrink: 0,
                '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
              }}
            >
              <MoreHorizIcon />
            </Box>
          }
          align='end'
          items={overflowTabs.map((tab) => ({
            label: tab.label,
            icon: React.isValidElement(tab.icon) ? tab.icon : undefined,
            disabled: tab.disabled,
            onSelect: () => commitChange(tab.id),
          }))}
        />
      )}
      {rightSlot && (
        <Box
          sx={{
            flexShrink: 0,
            display: 'inline-flex',
            alignItems: 'center',
            gap: ds.space.mul(0, 3),
            paddingLeft: ds.space[3],
          }}
        >
          {rightSlot}
        </Box>
      )}
    </Box>
  );
}

export default Tabs;
