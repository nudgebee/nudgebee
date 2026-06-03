/**
 * Accordion — DS V2 of legacy CustomAccordion + AccordionSmall.
 * Spec:        app/design-system/primitives/layout/accordion.html
 * Variants:    density = 'sm' | 'md'
 *              selection = 'single' | 'multi'
 *              composition = 'label' | 'label+meta' | 'icon+label+meta' (auto from items[i] shape)
 *
 * Migration:   `import CustomAccordion from '@common/CustomAccordion'`
 *              `import AccordionSmall from '@common/AccordionSmall'`
 *           →  `import { Accordion } from '@components1/ds/Accordion'`
 *
 * Don't (per spec):
 *   - Don't use Accordion for < 3 rows. Two collapsibles are just two Cards.
 *   - Don't put a critical setting inside a closed-by-default Accordion.
 */
import * as React from 'react';
import { Accordion as MuiAccordion, AccordionDetails as MuiAccordionDetails, AccordionSummary as MuiAccordionSummary } from '@mui/material';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';

export type AccordionDensity = 'sm' | 'md';
export type AccordionSelection = 'single' | 'multi';

export interface AccordionItem {
  id: string;
  label: React.ReactNode;
  /** Optional right-aligned chip / status / count */
  meta?: React.ReactNode;
  /** Optional left-aligned icon */
  icon?: React.ReactNode;
  /** Body content; lazy-mounted only when expanded */
  body: React.ReactNode;
  disabled?: boolean;
}

export interface AccordionProps {
  items: AccordionItem[];
  selection?: AccordionSelection;
  density?: AccordionDensity;
  /** Initial open ids (uncontrolled). Ignored when `expandedIds` is set. */
  defaultExpandedIds?: string[];
  /** Controlled open ids; pair with `onExpandedChange`. */
  expandedIds?: string[];
  onExpandedChange?: (next: string[]) => void;
}

const DENSITY_PADDING: Record<AccordionDensity, { summary: string; body: string; fontSize: string }> = {
  sm: { summary: 'var(--ds-space-2) var(--ds-space-3)', body: 'var(--ds-space-3)', fontSize: 'var(--ds-text-small)' },
  md: { summary: 'var(--ds-space-3) var(--ds-space-4)', body: 'var(--ds-space-4)', fontSize: 'var(--ds-text-body)' },
};

export function Accordion({ items, selection = 'multi', density = 'md', defaultExpandedIds = [], expandedIds, onExpandedChange }: AccordionProps) {
  const isControlled = expandedIds !== undefined;
  const [internal, setInternal] = React.useState<string[]>(defaultExpandedIds);
  const open = isControlled ? expandedIds! : internal;

  const setOpen = (next: string[]) => {
    if (!isControlled) setInternal(next);
    onExpandedChange?.(next);
  };

  const toggle = (id: string) => {
    if (open.includes(id)) {
      setOpen(open.filter((x) => x !== id));
    } else if (selection === 'single') {
      setOpen([id]);
    } else {
      setOpen([...open, id]);
    }
  };

  const d = DENSITY_PADDING[density];

  return (
    <div style={{ width: '100%' }}>
      {items.map((item) => {
        const isOpen = open.includes(item.id);
        return (
          <MuiAccordion
            key={item.id}
            expanded={isOpen}
            disabled={item.disabled}
            disableGutters
            elevation={0}
            square
            onChange={() => !item.disabled && toggle(item.id)}
            sx={{
              backgroundColor: 'var(--ds-background-100)',
              borderBottom: '1px solid var(--ds-gray-200)',
              '&:before': { display: 'none' },
              '&.Mui-expanded': { margin: 0 },
            }}
          >
            <MuiAccordionSummary
              expandIcon={
                <KeyboardArrowRightIcon
                  sx={{
                    fontSize: density === 'sm' ? 16 : 18,
                    color: 'var(--ds-gray-600)',
                    transition: 'transform var(--ds-motion-micro) var(--ds-motion-ease)',
                  }}
                />
              }
              sx={{
                padding: d.summary,
                minHeight: 'unset',
                '& .MuiAccordionSummary-content': {
                  margin: 0,
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--ds-space-2)',
                },
                '& .MuiAccordionSummary-content.Mui-expanded': { margin: 0 },
                '& .MuiAccordionSummary-expandIconWrapper': {
                  transform: 'rotate(0deg)',
                  '&.Mui-expanded': { transform: 'rotate(90deg)' },
                  order: -1,
                  marginRight: 'var(--ds-space-1)',
                },
                '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
              }}
            >
              {item.icon && (
                <span style={{ display: 'inline-flex', alignItems: 'center' }} aria-hidden='true'>
                  {item.icon}
                </span>
              )}
              <span
                style={{
                  fontSize: d.fontSize,
                  fontWeight: 'var(--ds-font-weight-medium)' as React.CSSProperties['fontWeight'],
                  color: 'var(--ds-gray-700)',
                  flex: 1,
                  textAlign: 'left',
                }}
              >
                {item.label}
              </span>
              {item.meta !== undefined && (
                <span
                  style={{
                    marginLeft: 'auto',
                    fontSize: 'var(--ds-text-small)',
                    color: 'var(--ds-gray-600)',
                  }}
                >
                  {item.meta}
                </span>
              )}
            </MuiAccordionSummary>
            <MuiAccordionDetails sx={{ padding: d.body, paddingTop: 0 }}>{isOpen ? item.body : null}</MuiAccordionDetails>
          </MuiAccordion>
        );
      })}
    </div>
  );
}

export default Accordion;
