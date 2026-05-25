/**
 * PageTabs — DS V2 of legacy AnchorComponent (parent row + hover-popover) +
 * the bottom sub-row (legacy CustomTabs) it owns. Composite primitive: emits
 * the page-level navigation experience as one unit so the URL hash routing,
 * the parent ↔ popover ↔ sub-row state, and the grouped-layout slice arithmetic
 * stay in one place.
 *
 * Spec:        app/design-system/primitives/navigation/page-tabs.html
 *
 * Variants:
 *   navigation = 'state' | 'router'   (router ⇒ syncs to URL hash `#parent/sub`)
 *   subOverflow = 'scroll' | 'group-window'  (group-window slices the sub-row
 *                                             based on the selected sub-value;
 *                                             preserves legacy AnchorComponent
 *                                             behaviour for the Monitoring +
 *                                             Apps & Infra parents).
 *
 * Migration:
 *   `import AnchorComponent from '@components1/common/AnchorComponent'`
 *   →
 *   `import { PageTabs } from '@components1/ds/PageTabs'`
 *
 *   filterOptions       → tabs
 *   onChangeFilter      → onChange
 *   showGroupedTabs     → subOverflow="group-window"
 *   manageRoute         → navigation="router"
 *
 * Don't (per spec):
 *   - Don't render page content inside PageTabs. PageTabs emits onChange; the
 *     page chooses what to render.
 *   - Don't put the brand action ("Settings", "Notifications") on the right
 *     edge of the parent row. Use the page header.
 *   - Don't tone an entire parent tab. Tone only the count Label.
 *   - Don't open the popover on click. Hover-with-intent is the contract; click
 *     navigates to the parent's first sub-tab.
 */
import * as React from 'react';
import { Box, Typography, MenuItem, ButtonBase, IconButton } from '@mui/material';
import Link from 'next/link';
import Image from 'next/image';
import { useRouter } from 'next/router';
import KeyboardArrowRightRoundedIcon from '@mui/icons-material/KeyboardArrowRightRounded';
import KeyboardArrowDownRoundedIcon from '@mui/icons-material/KeyboardArrowDownRounded';
import KeyboardArrowLeftRoundedIcon from '@mui/icons-material/KeyboardArrowLeftRounded';
import { Popover } from './Popover';
import { Label } from './Label';

export type PageTabsNavigation = 'state' | 'router';
export type PageTabsSubOverflow = 'scroll' | 'group-window';

/** Static-import or React-element icon. Matches what @assets exports + lucide. */
export type IconLike = React.ReactNode | { src: string; height?: number; width?: number } | string;

export interface PageTabSubItem {
  /** Stable id for testing + a11y. */
  id: string;
  /** Visible label. */
  text: string;
  /** Numeric value preserved from legacy callers (used as sub-tab key). */
  value: number;
  /** URL fragment after the parent fragment, e.g. `right-sizing` in `#optimize/right-sizing`. */
  fragment: string;
  icon?: IconLike;
  /** Place the icon before (default) or after the label. */
  iconPosition?: 'start' | 'end';
  betaIcon?: boolean;
  /** Group label for `subOverflow="group-window"` 2-column popover layout. */
  tabName?: string;
  /** Numeric badge rendered to the right of the sub-tab label. */
  count?: number;
  disabled?: boolean;
  hidden?: boolean;
  /** Override icon dimensions when needed (legacy compat). */
  height?: number;
  width?: number;
}

export interface PageTabItem {
  /** Stable id used for the parent button + popover anchor. */
  id?: string;
  /** Visible label. */
  name: string;
  /** Numeric value preserved from legacy callers (used as parent-tab key). */
  value: number;
  /** URL fragment for this parent (e.g. `optimize`). */
  fragment: string;
  icon?: IconLike;
  /** When true, keep the icon's original colour even when the parent is selected
   *  (default behaviour inverts monochrome icons to white). Use for full-colour
   *  brand logos (AWS EC2, RDS, GCP, Azure, …). */
  doNotInvertIcon?: boolean;
  betaIcon?: boolean;
  count?: number;
  disabled?: boolean;
  hidden?: boolean;
  /** When true, the popover renders sub-options grouped by `tabName` in 2 columns. */
  groupedTab?: boolean;
  /** Sub-tab list. When present, the parent shows a chevron + opens a popover on hover. */
  tabOptions?: PageTabSubItem[];
}

export interface PageTabsProps {
  /** Top-row navigation items. Each can declare its own sub-tab list. */
  tabs: PageTabItem[];
  /** Controlled parent value. */
  value: number;
  /** Controlled sub-tab value (within the active parent). */
  subValue: number;
  /** Called whenever parent or sub changes. Mirrors legacy `onChangeFilter`. */
  onChange: (parentValue: number, subValue: number, meta: { tab: number; subtab: number }) => void;
  navigation?: PageTabsNavigation;
  /** Sub-row overflow: `scroll` (MUI scroll buttons) or `group-window` (sliced
   *  window with arrows; matches legacy AnchorComponent showGroupedTabs). */
  subOverflow?: PageTabsSubOverflow;
  /** Tooltip text rendered on the count Label inside the sub-row. */
  countTooltip?: string;
  /** Bottom-border under the sub-row container. Defaults to true. */
  showSubRowBorder?: boolean;
  /** Optional element rendered on the right edge of the parent row (e.g. a primary
   *  page-level action like "Create Auto Optimize"). Replaces legacy
   *  `buttonComponent` on AnchorComponent. */
  rightSlot?: React.ReactNode;
  ariaLabel?: string;
}

/* -------------------------------------------------------------------------- */
/*  Icon rendering — supports next/image static imports, raw URLs, and React  */
/*  nodes (e.g. lucide). No runtime dependency on legacy SafeIcon.            */
/* -------------------------------------------------------------------------- */

interface NavIconProps {
  src: IconLike;
  alt: string;
  size?: number;
  className?: string;
}

function NavIcon({ src, alt, size = 18, className }: NavIconProps) {
  if (src == null) return null;
  if (React.isValidElement(src)) {
    return (
      <Box
        component='span'
        className={className}
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
  // next/image static import: { src, height, width } | string URL
  const url = typeof src === 'string' ? src : (src as { src?: string })?.src;
  if (!url) return null;
  const isSvg = url.endsWith('.svg');
  return (
    <Box
      component='span'
      className={className}
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

/* -------------------------------------------------------------------------- */
/*  URL helpers — preserve legacy hash routing exactly.                       */
/* -------------------------------------------------------------------------- */

function buildHashUrl(asPath: string, parentFragment: string, childFragment?: string): string {
  const [pathAndQuery] = asPath.split('#');
  const [path, queryString] = (pathAndQuery || '').split('?');
  const searchParams = new URLSearchParams(queryString);
  searchParams.delete('tab');
  searchParams.delete('subtab');

  let hash = parentFragment || '';
  if (childFragment) {
    hash = hash ? `${hash}/${childFragment}` : childFragment;
  }
  const cleanQuery = searchParams.toString();
  return `${path || ''}${cleanQuery ? `?${cleanQuery}` : ''}#${hash}`;
}

function parseHash(): { parent: string; child: string } {
  if (typeof window === 'undefined') return { parent: '', child: '' };
  const raw = window.location.hash || '';
  const decoded = decodeURIComponent(raw.replace('#', ''));
  const [parent, child] = decoded.split('/');
  return { parent: parent || '', child: child || '' };
}

/* -------------------------------------------------------------------------- */
/*  Group-window slice arithmetic for sub-row (legacy parity).                */
/* -------------------------------------------------------------------------- */

function computeWindow(parentValue: number, subValue: number, totalLength: number): [number, number] {
  // Legacy AnchorComponent windows (Monitoring=4, Security & Tools=5).
  if (parentValue === 5) {
    if (subValue >= 0 && subValue <= 2) return [0, 3];
    if (subValue >= 3 && subValue <= 6) return [3, 7];
    return [0, totalLength];
  }
  if (parentValue === 4) {
    if (subValue >= 0 && subValue <= 1) return [0, 2];
    if (subValue >= 2 && subValue <= 3) return [2, 4];
    if (subValue >= 4 && subValue <= 7) return [4, 8];
    if (subValue >= 8 && subValue <= 9) return [8, 10];
    return [0, totalLength];
  }
  return [0, totalLength];
}

/* -------------------------------------------------------------------------- */
/*  Parent-tab popover content                                                */
/* -------------------------------------------------------------------------- */

interface PopoverItemsProps {
  parent: PageTabItem;
  activeSubValue: number;
  isParentActive: boolean;
  buildSubHref: (sub: PageTabSubItem, parent: PageTabItem) => string;
  onSelect: (parentValue: number, subValue: number) => void;
  grouped: boolean;
}

function groupByTabName(options: PageTabSubItem[]): Record<string, PageTabSubItem[]> {
  return options.reduce<Record<string, PageTabSubItem[]>>((acc, opt) => {
    const key = opt.tabName ?? 'undefined';
    if (!acc[key]) acc[key] = [];
    acc[key].push(opt);
    return acc;
  }, {});
}

function PopoverMenuItem({
  item,
  parent,
  active,
  href,
  onClick,
}: {
  item: PageTabSubItem;
  parent: PageTabItem;
  active: boolean;
  href: string;
  onClick: () => void;
}) {
  if (item.hidden) return null;
  const disabled = !!item.disabled;
  return (
    <MenuItem
      id={`page-tabs-dropdown-${item.id}`}
      key={item.id}
      component={disabled ? 'div' : (Link as React.ElementType)}
      {...(!disabled && { href, scroll: false })}
      onClick={(e: React.MouseEvent) => {
        if (disabled) {
          e.preventDefault();
          return;
        }
        onClick();
      }}
      selected={active}
      disabled={disabled}
      sx={{
        padding: '6px 12px !important',
        margin: '4px 6px !important',
        borderRadius: 'var(--ds-radius-sm)',
        fontWeight: 'var(--ds-font-weight-medium)',
        fontSize: 'var(--ds-text-small)',
        color: 'var(--ds-gray-700)',
        minWidth: '188px',
        maxWidth: '240px',
        gap: 'var(--ds-space-2)',
        ...(disabled && {
          opacity: 0.5,
          cursor: 'not-allowed',
          pointerEvents: 'none',
          color: 'var(--ds-gray-500)',
        }),
        '&:hover': disabled
          ? {}
          : {
              backgroundColor: 'var(--ds-gray-100)',
              color: 'var(--ds-gray-900, var(--ds-gray-700))',
            },
        '&.Mui-selected': {
          color: 'var(--ds-brand-700)',
          backgroundColor: 'var(--ds-brand-100)',
          fontWeight: 'var(--ds-font-weight-semibold)',
        },
        '&.Mui-selected:hover': {
          backgroundColor: 'var(--ds-brand-200)',
        },
      }}
      data-parent-fragment={parent.fragment}
    >
      {item.icon && <NavIcon src={item.icon} alt={item.text} size={item.height || 18} />}
      <Typography
        component='span'
        sx={{
          fontSize: 'var(--ds-text-small)',
          color: 'inherit',
          flex: 1,
        }}
      >
        {item.text}
      </Typography>
      {item.count !== undefined && (
        <Label tone='neutral' size='sm'>
          {item.count}
        </Label>
      )}
    </MenuItem>
  );
}

function PopoverItems({ parent, activeSubValue, isParentActive, buildSubHref, onSelect, grouped }: PopoverItemsProps) {
  const list = parent.tabOptions ?? [];

  if (grouped) {
    const groups = groupByTabName(list);
    return (
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          columnGap: 'var(--ds-space-3)',
          maxWidth: '490px',
        }}
      >
        {Object.entries(groups).map(([groupName, items]) => (
          <Box key={groupName}>
            {groupName && groupName !== 'undefined' && (
              <Typography
                sx={{
                  padding: '2px 8px',
                  borderRadius: 'var(--ds-radius-sm)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-gray-600)',
                  backgroundColor: 'var(--ds-gray-100)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.04em',
                  margin: '4px 6px',
                }}
              >
                {groupName}
              </Typography>
            )}
            {items.map((item) => (
              <PopoverMenuItem
                key={item.id}
                item={item}
                parent={parent}
                active={isParentActive && item.value === activeSubValue}
                href={buildSubHref(item, parent)}
                onClick={() => onSelect(parent.value, item.value)}
              />
            ))}
          </Box>
        ))}
      </Box>
    );
  }

  return (
    <Box>
      {list.map((item) => (
        <PopoverMenuItem
          key={item.id}
          item={item}
          parent={parent}
          active={isParentActive && item.value === activeSubValue}
          href={buildSubHref(item, parent)}
          onClick={() => onSelect(parent.value, item.value)}
        />
      ))}
    </Box>
  );
}

/* -------------------------------------------------------------------------- */
/*  Parent-tab button                                                         */
/* -------------------------------------------------------------------------- */

interface ParentTabButtonProps {
  tab: PageTabItem;
  selected: boolean;
  href: string;
  onActivate: () => void;
}

const ParentTabButton = React.forwardRef<HTMLAnchorElement, ParentTabButtonProps>(function ParentTabButton({ tab, selected, href, onActivate }, ref) {
  // Selected = soft brand tint (--ds-brand-100), so icons keep their original
  // colour. `doNotInvertIcon` is now a no-op — kept on the type for API
  // back-compat with legacy AnchorComponent callers.
  return (
    <ButtonBase
      ref={ref as React.Ref<HTMLButtonElement>}
      component={Link as React.ElementType}
      href={href}
      onClick={onActivate}
      id={`page-tabs-parent-${tab.id || tab.fragment}`}
      data-testid={`page-tabs-parent-${tab.id || tab.fragment}`}
      disabled={tab.disabled || false}
      aria-current={selected ? 'page' : undefined}
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 'var(--ds-space-2)',
        height: '36px',
        padding: '0 var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-md)',
        fontFamily: 'var(--ds-font-sans)',
        fontSize: 'var(--ds-text-small)',
        fontWeight: selected ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
        lineHeight: 1,
        textTransform: 'none',
        textDecoration: 'none',
        cursor: tab.disabled ? 'not-allowed' : 'pointer',
        transition: 'background-color 120ms ease, color 120ms ease',
        backgroundColor: selected ? 'var(--ds-brand-100)' : 'transparent',
        color: selected ? 'var(--ds-brand-700)' : 'var(--ds-gray-700)',
        '&:hover': {
          backgroundColor: selected ? 'var(--ds-brand-200)' : 'var(--ds-gray-100)',
          color: selected ? 'var(--ds-brand-700)' : 'var(--ds-brand-700)',
        },
        '&:focus-visible': {
          outline: 'none',
          boxShadow: '0 0 0 3px var(--ds-yellow-500, var(--ds-blue-100))',
        },
        '&.Mui-disabled': {
          opacity: 0.5,
          pointerEvents: 'none',
        },
        '& .ds-page-tabs__icon': {
          color: selected ? 'var(--ds-brand-700)' : 'var(--ds-gray-600)',
          transition: 'color 120ms ease',
        },
      }}
    >
      {tab.icon && <NavIcon src={tab.icon} alt={tab.name} size={20} className='ds-page-tabs__icon' />}
      <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
        <span>{tab.name}</span>
        {tab.count !== undefined && (
          <Label tone='neutral' size='sm'>
            {tab.count}
          </Label>
        )}
      </Box>
      {tab.betaIcon && (
        <Label tone='info' size='sm'>
          BETA
        </Label>
      )}
      {tab.tabOptions && tab.tabOptions.length > 0 && (
        <KeyboardArrowDownRoundedIcon
          sx={{
            fontSize: 16,
            transition: 'transform 200ms ease',
            color: selected ? 'var(--ds-brand-700)' : 'var(--ds-gray-500)',
          }}
        />
      )}
    </ButtonBase>
  );
});

/* -------------------------------------------------------------------------- */
/*  Sub-row (the bottom tab strip)                                            */
/* -------------------------------------------------------------------------- */

interface SubTabRowProps {
  parent: PageTabItem;
  activeSubValue: number;
  windowSlice: [number, number] | null;
  buildSubHref: (sub: PageTabSubItem, parent: PageTabItem) => string;
  onSelect: (subValue: number) => void;
  countTooltip?: string;
  showBorder: boolean;
}

function SubTabRow({ parent, activeSubValue, windowSlice, buildSubHref, onSelect, countTooltip, showBorder }: SubTabRowProps) {
  const allOptions = (parent.tabOptions ?? []).filter((opt) => !opt.hidden);
  const visibleOptions = windowSlice ? allOptions.slice(windowSlice[0], windowSlice[1]) : allOptions;
  const groupHeading = visibleOptions[0]?.tabName;

  // ── Scroll arrows: hide native scrollbar, show chevron buttons when content
  //    overflows (matches the polished MUI Tabs scrollable affordance). ────
  const stripRef = React.useRef<HTMLDivElement | null>(null);
  const [canScrollLeft, setCanScrollLeft] = React.useState(false);
  const [canScrollRight, setCanScrollRight] = React.useState(false);

  const updateScrollState = React.useCallback(() => {
    const el = stripRef.current;
    if (!el) return;
    setCanScrollLeft(el.scrollLeft > 1);
    setCanScrollRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
  }, []);

  React.useEffect(() => {
    updateScrollState();
    const el = stripRef.current;
    if (!el) return;
    el.addEventListener('scroll', updateScrollState, { passive: true });
    const ro = new ResizeObserver(updateScrollState);
    ro.observe(el);
    return () => {
      el.removeEventListener('scroll', updateScrollState);
      ro.disconnect();
    };
  }, [updateScrollState, visibleOptions.length]);

  const scrollBy = (dx: number) => {
    stripRef.current?.scrollBy({ left: dx, behavior: 'smooth' });
  };

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        backgroundColor: 'var(--ds-background-100)',
        borderBottom: showBorder ? '1px solid var(--ds-gray-200)' : 'none',
        borderRadius: 'var(--ds-radius-md)',
        overflow: 'hidden',
        boxShadow: '0px 1px 0px var(--ds-gray-alpha-100, rgba(0,0,0,0.04))',
      }}
    >
      {groupHeading && groupHeading !== 'undefined' && (
        <Box
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            alignSelf: 'stretch',
            gap: 'var(--ds-space-2)',
            paddingX: 'var(--ds-space-4)',
            backgroundColor: 'var(--ds-background-100)',
            borderRight: '1px solid var(--ds-gray-200)',
            flexShrink: 0,
          }}
        >
          <Typography
            sx={{
              color: 'var(--ds-gray-600)',
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              lineHeight: 1,
              textTransform: 'uppercase',
              letterSpacing: '0.04em',
              whiteSpace: 'nowrap',
            }}
          >
            {groupHeading}
          </Typography>
          <KeyboardArrowRightRoundedIcon sx={{ color: 'var(--ds-gray-500)', fontSize: 16 }} />
        </Box>
      )}
      <IconButton
        size='small'
        aria-label='Scroll sub-tabs left'
        onClick={() => scrollBy(-200)}
        disabled={!canScrollLeft}
        sx={{
          flexShrink: 0,
          width: 28,
          height: 28,
          marginLeft: 'var(--ds-space-2)',
          color: 'var(--ds-gray-600)',
          opacity: canScrollLeft ? 1 : 0,
          pointerEvents: canScrollLeft ? 'auto' : 'none',
          transition: 'opacity 160ms ease',
          '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
        }}
      >
        <KeyboardArrowLeftRoundedIcon sx={{ fontSize: 20 }} />
      </IconButton>
      <Box
        ref={stripRef}
        role='tablist'
        aria-label={`${parent.name} sub-tabs`}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          padding: '6px var(--ds-space-2)',
          overflowX: 'auto',
          flex: 1,
          scrollbarWidth: 'none',
          msOverflowStyle: 'none',
          '&::-webkit-scrollbar': { display: 'none' },
        }}
      >
        {visibleOptions.map((opt) => {
          const selected = opt.value === activeSubValue;
          const disabled = !!opt.disabled;
          const iconAtEnd = opt.iconPosition === 'end';
          return (
            <ButtonBase
              key={opt.id}
              role='tab'
              aria-selected={selected}
              component={disabled ? 'button' : (Link as React.ElementType)}
              {...(!disabled && { href: buildSubHref(opt, parent), scroll: false })}
              onClick={(e: React.MouseEvent) => {
                if (disabled) {
                  e.preventDefault();
                  return;
                }
                onSelect(opt.value);
              }}
              disabled={disabled}
              id={`page-tabs-sub-${opt.id}`}
              data-testid={`page-tabs-sub-${opt.id}`}
              sx={{
                position: 'relative',
                display: 'inline-flex',
                alignItems: 'center',
                gap: 'var(--ds-space-2)',
                height: 36,
                padding: '0 var(--ds-space-3)',
                borderRadius: 'var(--ds-radius-sm)',
                fontFamily: 'var(--ds-font-sans)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: selected ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
                color: selected ? 'var(--ds-brand-700)' : 'var(--ds-gray-600)',
                textDecoration: 'none',
                cursor: disabled ? 'not-allowed' : 'pointer',
                whiteSpace: 'nowrap',
                flexShrink: 0,
                transition: 'color 120ms ease, background-color 120ms ease',
                opacity: disabled ? 0.5 : 1,
                '&:hover': {
                  color: selected ? 'var(--ds-brand-700)' : 'var(--ds-gray-700)',
                  backgroundColor: 'var(--ds-gray-100)',
                },
                '&::after': {
                  content: '""',
                  position: 'absolute',
                  left: 'var(--ds-space-3)',
                  right: 'var(--ds-space-3)',
                  bottom: -6,
                  height: 2,
                  borderRadius: 2,
                  backgroundColor: selected ? 'var(--ds-brand-700)' : 'transparent',
                  transition: 'background-color 120ms ease',
                },
              }}
            >
              {opt.icon && !iconAtEnd && <NavIcon src={opt.icon} alt={opt.text} size={opt.height || 16} />}
              <span>{opt.text}</span>
              {opt.icon && iconAtEnd && <NavIcon src={opt.icon} alt={opt.text} size={opt.height || 16} />}
              {opt.count !== undefined && (
                <Label tone='neutral' size='sm' title={countTooltip}>
                  {opt.count > 99 ? '99+' : opt.count}
                </Label>
              )}
              {opt.betaIcon && (
                <Label tone='info' size='sm'>
                  BETA
                </Label>
              )}
            </ButtonBase>
          );
        })}
      </Box>
      <IconButton
        size='small'
        aria-label='Scroll sub-tabs right'
        onClick={() => scrollBy(200)}
        disabled={!canScrollRight}
        sx={{
          flexShrink: 0,
          width: 28,
          height: 28,
          marginRight: 'var(--ds-space-2)',
          color: 'var(--ds-gray-600)',
          opacity: canScrollRight ? 1 : 0,
          pointerEvents: canScrollRight ? 'auto' : 'none',
          transition: 'opacity 160ms ease',
          '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
        }}
      >
        <KeyboardArrowRightRoundedIcon sx={{ fontSize: 20 }} />
      </IconButton>
    </Box>
  );
}

/* -------------------------------------------------------------------------- */
/*  PageTabs (composite)                                                      */
/* -------------------------------------------------------------------------- */

export function PageTabs({
  tabs,
  value,
  subValue,
  onChange,
  navigation = 'router',
  subOverflow = 'scroll',
  countTooltip,
  showSubRowBorder = true,
  rightSlot,
  ariaLabel = 'Page navigation',
}: PageTabsProps) {
  const router = useRouter();
  const isFirstMount = React.useRef(true);
  const manageRoute = navigation === 'router';

  // ── URL ↔ state sync (router mode) ─────────────────────────────────────
  React.useEffect(() => {
    if (!manageRoute) return;
    const { parent: parentFrag, child: childFrag } = parseHash();
    if (!parentFrag || tabs.length === 0) return;

    const parent = tabs.find((opt) => opt.fragment === parentFrag);
    if (!parent) return;

    let nextSub = 0;
    if (childFrag && parent.tabOptions) {
      const child = parent.tabOptions.find((opt) => opt.fragment === childFrag);
      if (child) nextSub = child.value;
    }

    if (parent.value !== value || nextSub !== subValue) {
      onChange(parent.value, nextSub, { tab: parent.value, subtab: nextSub });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.asPath, tabs, manageRoute]);

  // Suppress the very first onChange firing if the URL already encodes state —
  // matches AnchorComponent's "don't overwrite the URL on load" guarantee.
  React.useEffect(() => {
    if (isFirstMount.current) {
      isFirstMount.current = false;
    }
  }, []);

  const buildParentHref = React.useCallback((tab: PageTabItem) => buildHashUrl(router.asPath || '', tab.fragment), [router.asPath]);

  const buildSubHref = React.useCallback(
    (sub: PageTabSubItem, parent: PageTabItem) => buildHashUrl(router.asPath || '', parent.fragment, sub.fragment),
    [router.asPath]
  );

  const activeParent = tabs.find((t) => t.value === value) ?? tabs[0];

  const windowSlice: [number, number] | null = React.useMemo(() => {
    if (!activeParent || subOverflow !== 'group-window') return null;
    const total = activeParent.tabOptions?.length ?? 0;
    return computeWindow(activeParent.value, subValue, total);
  }, [activeParent, subValue, subOverflow]);

  const handleParentSelect = (parentValue: number) => {
    onChange(parentValue, 0, { tab: parentValue, subtab: 0 });
  };

  const handleSubSelect = (parentValue: number, nextSub: number) => {
    onChange(parentValue, nextSub, { tab: parentValue, subtab: nextSub });
  };

  return (
    <Box
      role='navigation'
      aria-label={ariaLabel}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--ds-space-3)',
        backgroundColor: 'var(--ds-background-100)',
        padding: 'var(--ds-space-3) var(--ds-space-5) 0',
        borderTop: '1px solid var(--ds-gray-200)',
      }}
    >
      {/* Parent row */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
        }}
      >
        <Box
          sx={{
            flex: 1,
            minWidth: 0,
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            overflowX: 'auto',
            scrollbarWidth: 'none',
            msOverflowStyle: 'none',
            '&::-webkit-scrollbar': { display: 'none' },
          }}
        >
          {tabs
            .filter((t) => !t.hidden)
            .map((tab) => {
              const selected = tab.value === value;
              const button = (
                <ParentTabButton tab={tab} selected={selected} href={buildParentHref(tab)} onActivate={() => handleParentSelect(tab.value)} />
              );

              if (!tab.tabOptions || tab.tabOptions.length === 0) {
                return <React.Fragment key={tab.fragment}>{button}</React.Fragment>;
              }

              return (
                <Popover
                  key={tab.fragment}
                  trigger='hover-with-intent'
                  side='bottom'
                  align='start'
                  size='md-320'
                  hoverIntent={{ open: 120, close: 200 }}
                  content={
                    <PopoverItems
                      parent={tab}
                      activeSubValue={subValue}
                      isParentActive={selected}
                      buildSubHref={buildSubHref}
                      onSelect={handleSubSelect}
                      grouped={!!tab.groupedTab}
                    />
                  }
                >
                  {button}
                </Popover>
              );
            })}
        </Box>
        {rightSlot && <Box sx={{ flexShrink: 0, display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>{rightSlot}</Box>}
      </Box>

      {/* Sub row */}
      {activeParent?.tabOptions && activeParent.tabOptions.length > 0 && (
        <SubTabRow
          parent={activeParent}
          activeSubValue={subValue}
          windowSlice={windowSlice}
          buildSubHref={buildSubHref}
          onSelect={(next) => handleSubSelect(activeParent.value, next)}
          countTooltip={countTooltip}
          showBorder={showSubRowBorder}
        />
      )}
    </Box>
  );
}

export default PageTabs;
