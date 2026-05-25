/**
 * Tabs — DS V2 of legacy CustomTabsForDrilldown + CustomTabs + ButtonTabs.
 * Spec:        app/design-system/primitives/navigation/tabs.html
 * Variants:    visual = 'underline' | 'segmented'
 *              size = 'sm' | 'md'
 *              tone = 'blue' | 'brand'   (underline only — selection axis)
 *              composition = 'text' | 'text+count' | 'icon+text' | 'icon-only' (auto from item shape)
 *              navigation = 'state' | 'router'
 *              overflow = 'scroll' | 'more-menu'
 *
 * Migration:   `import CustomTabsForDrilldown from '@common/CustomTabsForDrilldown'`
 *              `import CustomTabs from '@common/CustomTabs'`
 *              `import ButtonTabs from '@common/ButtonTabs'`
 *           →  `import { Tabs } from '@components1/ds/Tabs'`
 *
 *   ButtonTabs    →  Tabs visual="segmented"
 *   CustomTabs (router-aware)  →  Tabs navigation="router"
 *
 * Don't (per spec):
 *   - Don't use segmented for > 4 options. Switch to underline + overflow="more-menu".
 *   - Don't put a tab on the right edge for Settings. Use a header button.
 *   - Don't tone an entire tab. Apply tone to its count only.
 *   - Don't render tab content inside Tabs. Tabs emit onChange; page chooses what to render.
 *   - Don't use tone="brand" outside a PageTabs-owned page (e.g. inside a card or
 *     panel that isn't part of the page nav). Brand reads as page-level navigation.
 */
import * as React from 'react';
import { Box, Tabs as MuiTabs, Tab as MuiTab } from '@mui/material';
import { useRouter } from 'next/router';
import { Label, type LabelTone } from './Label';
import { DropdownMenu } from './DropdownMenu';
import MoreHorizIcon from '@mui/icons-material/MoreHoriz';

export type TabsVisual = 'underline' | 'segmented';
export type TabsSize = 'sm' | 'md';
export type TabsNavigation = 'state' | 'router';
export type TabsOverflow = 'scroll' | 'more-menu';
/** Selection-axis tone for the underline indicator + selected text.
 *  - 'blue'  (default) — the historical DS blue indicator.
 *  - 'brand' — brand-navy indicator, matched to PageTabs' sub-row so a Tabs
 *    rendered inside a PageTabs-driven page reads as one navigation system.
 *    Use brand inside row-accordion drilldowns under PageTabs.
 *  Segmented variant ignores `tone` (its selected fill is gray-100). */
export type TabsTone = 'blue' | 'brand';

export interface TabItem {
  id: string;
  label: React.ReactNode;
  /** Numeric badge rendered to the right of the label */
  count?: number;
  /** Tone applied to the count badge only (per spec: never tone the whole tab) */
  tone?: LabelTone;
  /** Optional left-aligned icon */
  icon?: React.ReactNode;
  disabled?: boolean;
}

export interface TabsProps {
  tabs: TabItem[];
  value: string;
  onChange: (next: string) => void;
  visual?: TabsVisual;
  size?: TabsSize;
  /** Selection-axis tone for the underline variant. Default 'blue'. */
  tone?: TabsTone;
  navigation?: TabsNavigation;
  overflow?: TabsOverflow;
  /** Query-param key used when navigation='router' (default 'tab') */
  routerParam?: string;
  /** When overflow='more-menu', show this many tabs inline before collapsing the rest */
  visibleTabCount?: number;
  ariaLabel?: string;
}

const SIZE_TOKENS: Record<TabsSize, { fontSize: string; padX: string; height: string }> = {
  sm: { fontSize: 'var(--ds-text-small)', padX: 'var(--ds-space-3)', height: '32px' },
  md: { fontSize: 'var(--ds-text-body)', padX: 'var(--ds-space-4)', height: '40px' },
};

function TabContent({ item, size }: { item: TabItem; size: TabsSize }) {
  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
      {item.icon && (
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center' }}>
          {item.icon}
        </Box>
      )}
      <Box component='span' sx={{ fontSize: SIZE_TOKENS[size].fontSize }}>
        {item.label}
      </Box>
      {item.count !== undefined && (
        <Label tone={item.tone ?? 'neutral'} size='sm'>
          {item.count}
        </Label>
      )}
    </Box>
  );
}

export function Tabs({
  tabs,
  value,
  onChange,
  visual = 'underline',
  size = 'md',
  tone = 'blue',
  navigation = 'state',
  overflow = 'scroll',
  routerParam = 'tab',
  visibleTabCount,
  ariaLabel,
}: TabsProps) {
  const router = useRouter();

  // Router sync: read initial from URL, push on change
  React.useEffect(() => {
    if (navigation !== 'router' || !router.isReady) return;
    const fromUrl = router.query[routerParam];
    if (typeof fromUrl === 'string' && fromUrl !== value) {
      onChange(fromUrl);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigation, router.isReady, router.query[routerParam]]);

  const handleChange = (_e: React.SyntheticEvent, next: string) => {
    onChange(next);
    if (navigation === 'router') {
      router.push({ pathname: router.pathname, query: { ...router.query, [routerParam]: next } }, undefined, { shallow: true });
    }
  };

  // Overflow handling: more-menu collapses tabs beyond visibleTabCount
  let inlineTabs = tabs;
  let overflowTabs: TabItem[] = [];
  if (overflow === 'more-menu' && visibleTabCount && tabs.length > visibleTabCount) {
    inlineTabs = tabs.slice(0, visibleTabCount);
    overflowTabs = tabs.slice(visibleTabCount);
  }

  // Ensure the controlled value resolves; if it falls into the overflow bucket,
  // pull that tab inline so MUI doesn't error on an unknown value.
  const isOverflowSelected = overflowTabs.some((t) => t.id === value);
  if (isOverflowSelected) {
    const selectedOverflow = overflowTabs.find((t) => t.id === value)!;
    inlineTabs = [...inlineTabs.slice(0, -1), selectedOverflow];
    overflowTabs = overflowTabs.filter((t) => t.id !== value);
    overflowTabs.push(tabs[(visibleTabCount as number) - 1]);
  }

  const isSegmented = visual === 'segmented';
  const isBrand = tone === 'brand' && !isSegmented;
  const indicatorColor = isBrand ? 'var(--ds-brand-700)' : 'var(--ds-blue-500)';
  const selectedTextColor = isBrand ? 'var(--ds-brand-700)' : 'var(--ds-blue-600)';

  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        ...(isSegmented && {
          backgroundColor: 'var(--ds-gray-100)',
          borderRadius: 'var(--ds-radius-md)',
          padding: '2px',
        }),
        ...(visual === 'underline' && {
          borderBottom: '1px solid var(--ds-gray-200)',
          width: '100%',
        }),
      }}
    >
      <MuiTabs
        value={value}
        onChange={handleChange}
        variant={overflow === 'scroll' ? 'scrollable' : 'standard'}
        scrollButtons={overflow === 'scroll' ? 'auto' : false}
        aria-label={ariaLabel}
        sx={{
          minHeight: SIZE_TOKENS[size].height,
          '& .MuiTabs-indicator': {
            display: isSegmented ? 'none' : 'block',
            height: 2,
            backgroundColor: indicatorColor,
          },
          '& .MuiTabs-flexContainer': {
            gap: isSegmented ? 0 : 'var(--ds-space-2)',
          },
        }}
      >
        {inlineTabs.map((tab) => (
          <MuiTab
            key={tab.id}
            value={tab.id}
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
                color: isSegmented ? 'var(--ds-gray-700)' : selectedTextColor,
                fontWeight: isBrand ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
                ...(isSegmented && {
                  backgroundColor: 'var(--ds-background-100)',
                  borderRadius: 'var(--ds-radius-sm)',
                  boxShadow: '0px 1px 3px var(--ds-gray-alpha-200)',
                }),
              },
              '&:hover': {
                color: isSegmented ? 'var(--ds-gray-700)' : selectedTextColor,
                backgroundColor: isSegmented ? 'transparent' : 'var(--ds-gray-100)',
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
                borderRadius: 'var(--ds-radius-sm)',
                '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
              }}
            >
              <MoreHorizIcon />
            </Box>
          }
          align='end'
          items={overflowTabs.map((tab) => ({
            label: tab.label,
            icon: tab.icon,
            disabled: tab.disabled,
            onSelect: () => handleChange({} as React.SyntheticEvent, tab.id),
          }))}
        />
      )}
    </Box>
  );
}

export default Tabs;
