/**
 * ToggleGroup — DS V2 of legacy CustomButtonsGroup.
 * Spec: app/design-system/primitives/forms/toggle-group.html
 *
 * Mutually-exclusive or multi-toggle button group. Distinct from
 * **Tabs (segmented)** by intent — Tabs change views, ToggleGroup changes a
 * value or filter set. The visual is similar but the semantics differ.
 *
 * Variants per spec:
 *   selection   = 'single' | 'multi'
 *   size        = 'sm' | 'md'
 *   composition = 'text' | 'icon-only' | 'icon+text'  (auto from option shape)
 *
 * Don't (per spec):
 *   - Don't use ToggleGroup with more than 5 options — switch to Select.
 *   - Don't mix icon-only and icon+text in one group. Pick one composition per group.
 *   - Don't use ToggleGroup as the only filter on a list when "no selection" is
 *     meaningful. Single-mode requires a chosen value.
 *
 * Migration:
 *   `import CustomButtonsGroup from '@components1/common/CustomButtonsGroup'`
 * → `import { ToggleGroup } from '@components1/ds/ToggleGroup'`
 *   Visual identity is shared with `Tabs (segmented)`; intent disambiguates.
 */
import * as React from 'react';
import { Box, ButtonBase } from '@mui/material';
import Tooltip from './Tooltip';

export type ToggleGroupSize = 'sm' | 'md';
export type ToggleGroupSelection = 'single' | 'multi';

export interface ToggleGroupOption<V extends string = string> {
  value: V;
  label?: React.ReactNode;
  icon?: React.ReactNode;
  /** Used as `aria-label` when the option is icon-only. */
  ariaLabel?: string;
  /** Tooltip text shown on hover. */
  tooltip?: string;
  disabled?: boolean;
}

interface ToggleGroupBaseProps<V extends string = string> {
  options: ToggleGroupOption<V>[];
  size?: ToggleGroupSize;
  /** Group accessible name (announced by screen readers). */
  ariaLabel?: string;
  className?: string;
  id?: string;
}

interface ToggleGroupSingleProps<V extends string = string> extends ToggleGroupBaseProps<V> {
  selection: 'single';
  value: V;
  onChange: (next: V) => void;
}

interface ToggleGroupMultiProps<V extends string = string> extends ToggleGroupBaseProps<V> {
  selection: 'multi';
  value: V[];
  onChange: (next: V[]) => void;
}

export type ToggleGroupProps<V extends string = string> = ToggleGroupSingleProps<V> | ToggleGroupMultiProps<V>;

const SIZE_TOKENS: Record<ToggleGroupSize, { height: string; fontSize: string; padX: string; iconSize: number }> = {
  sm: { height: '24px', fontSize: 'var(--ds-text-caption)', padX: '8px', iconSize: 12 },
  md: { height: '32px', fontSize: 'var(--ds-text-body)', padX: '12px', iconSize: 14 },
};

export function ToggleGroup<V extends string = string>(props: ToggleGroupProps<V>) {
  const { options, size = 'md', ariaLabel, className, id } = props;
  const tokens = SIZE_TOKENS[size];

  const isSelected = (v: V): boolean => {
    if (props.selection === 'single') return props.value === v;
    return props.value.includes(v);
  };

  const handleClick = (opt: ToggleGroupOption<V>) => {
    if (opt.disabled) return;
    if (props.selection === 'single') {
      if (props.value !== opt.value) props.onChange(opt.value);
      return;
    }
    const set = new Set(props.value);
    if (set.has(opt.value)) set.delete(opt.value);
    else set.add(opt.value);
    props.onChange(Array.from(set));
  };

  return (
    <Box
      id={id}
      className={className}
      role='group'
      aria-label={ariaLabel}
      sx={{
        display: 'inline-flex',
        alignItems: 'stretch',
        height: tokens.height,
        border: '1px solid var(--ds-gray-300)',
        borderRadius: 'var(--ds-radius-sm)',
        backgroundColor: 'var(--ds-background-100)',
        overflow: 'hidden',
        '& > button + button': { borderLeft: '1px solid var(--ds-gray-300)' },
      }}
    >
      {options.map((opt) => {
        const selected = isSelected(opt.value);
        const iconOnly = !opt.label && !!opt.icon;
        const button = (
          <ButtonBase
            type='button'
            role={props.selection === 'single' ? 'radio' : 'checkbox'}
            aria-checked={selected}
            aria-label={iconOnly ? opt.ariaLabel ?? opt.value : undefined}
            disabled={opt.disabled}
            onClick={() => handleClick(opt)}
            sx={{
              minWidth: iconOnly ? tokens.height : 'auto',
              padding: iconOnly ? 0 : `0 ${tokens.padX}`,
              fontSize: tokens.fontSize,
              fontWeight: selected ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
              color: selected ? 'var(--ds-blue-700)' : 'var(--ds-gray-700)',
              backgroundColor: selected ? 'var(--ds-blue-100)' : 'transparent',
              transition: 'background-color var(--ds-motion-micro) var(--ds-motion-ease)',
              gap: '6px',
              '&:hover': opt.disabled ? undefined : { backgroundColor: selected ? 'var(--ds-blue-100)' : 'var(--ds-gray-100)' },
              '&.Mui-disabled': { color: 'var(--ds-gray-400)', cursor: 'not-allowed' },
              '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '-2px' },
            }}
          >
            {opt.icon && (
              <Box component='span' sx={{ display: 'inline-flex', fontSize: tokens.iconSize }}>
                {opt.icon}
              </Box>
            )}
            {opt.label}
          </ButtonBase>
        );
        return opt.tooltip ? (
          <Tooltip key={opt.value} title={opt.tooltip} placement='top'>
            {button}
          </Tooltip>
        ) : (
          <React.Fragment key={opt.value}>{button}</React.Fragment>
        );
      })}
    </Box>
  );
}

export default ToggleGroup;
