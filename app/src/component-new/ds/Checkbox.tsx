/**
 * Checkbox — DS V2 of legacy CustomCheckbox.
 * Spec: app/design-system/primitives/forms/checkbox.html
 *
 * Tri-state on / off / indeterminate. Always paired with a label —
 * unlabeled checkboxes are an accessibility bug.
 *
 * Variants per spec:
 *   size        = 'sm' | 'md'
 *   state       = 'off' | 'on' | 'indeterminate' | 'disabled'
 *                 (auto-resolved from `checked` / `indeterminate` / `disabled`)
 *   composition = 'label-right' | 'label-right+description' | 'checkbox-only'
 *                 (auto from `label` + `description` props)
 *
 * Don't (per spec):
 *   - Don't use Checkbox for "enable / disable" of a setting that takes effect
 *     immediately. That's a Switch.
 *   - Don't render label-less Checkboxes in lists. Each row needs an accessible
 *     name (use `aria-label` if the visible label sits elsewhere).
 *   - Don't use indeterminate for "loading" — it has a specific selection-tree
 *     meaning. Use a Skeleton.
 *
 * Migration:
 *   `import CustomCheckbox from '@components1/common/CustomCheckbox'`
 * → `import { Checkbox } from '@components1/ds/Checkbox'`
 *   Adds indeterminate state.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type CheckboxSize = 'sm' | 'md';

export interface CheckboxProps {
  checked: boolean;
  onChange: (next: boolean) => void;
  /** Visible label rendered to the right of the box. */
  label?: React.ReactNode;
  /** Optional secondary description below the label. */
  description?: React.ReactNode;
  /** Selection-tree partial-state. Visual tri-state — `checked` is still required for the underlying input. */
  indeterminate?: boolean;
  disabled?: boolean;
  size?: CheckboxSize;
  /** Required when no visible `label` is given (spec accessibility rule). */
  'aria-label'?: string;
  className?: string;
  id?: string;
  name?: string;
  value?: string;
}

const SIZE_TOKENS: Record<CheckboxSize, { box: string; fontSize: string; gap: string; iconSize: number }> = {
  sm: { box: '14px', fontSize: 'var(--ds-text-caption)', gap: '6px', iconSize: 10 },
  md: { box: '16px', fontSize: 'var(--ds-text-body)', gap: '8px', iconSize: 12 },
};

export function Checkbox({
  checked,
  onChange,
  label,
  description,
  indeterminate,
  disabled,
  size = 'md',
  'aria-label': ariaLabel,
  className,
  id,
  name,
  value,
}: CheckboxProps) {
  const tokens = SIZE_TOKENS[size];
  const reactId = React.useId();
  const inputId = id ?? reactId;
  const inputRef = React.useRef<HTMLInputElement | null>(null);

  // Native indeterminate is only settable via DOM property, not attribute.
  React.useEffect(() => {
    if (inputRef.current) inputRef.current.indeterminate = !!indeterminate;
  }, [indeterminate]);

  // Composition: 'checkbox-only' | 'label-right' | 'label-right+description'
  const hasLabel = label !== undefined;
  const hasDescription = description !== undefined;

  const visualState: 'on' | 'off' | 'indeterminate' = indeterminate ? 'indeterminate' : checked ? 'on' : 'off';

  const visualBox = (
    <Box
      aria-hidden='true'
      sx={{
        position: 'relative',
        // Visual is purely decorative — clicks must pass through to the native input
        // beneath, otherwise the checkbox-only composition (no <label>) is unclickable.
        pointerEvents: 'none',
        width: tokens.box,
        height: tokens.box,
        flexShrink: 0,
        borderRadius: 'var(--ds-radius-sm)',
        border: `1px solid ${disabled ? 'var(--ds-gray-300)' : visualState !== 'off' ? 'var(--ds-blue-500)' : 'var(--ds-gray-400)'}`,
        backgroundColor: disabled ? 'var(--ds-background-200)' : visualState !== 'off' ? 'var(--ds-blue-500)' : 'var(--ds-background-100)',
        transition: 'background-color var(--ds-motion-micro) var(--ds-motion-ease)',
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        color: 'var(--ds-background-100)',
      }}
    >
      <Box
        component='svg'
        viewBox='0 0 16 16'
        sx={{
          width: tokens.iconSize,
          height: tokens.iconSize,
          opacity: visualState === 'on' ? 1 : 0,
          transition: 'opacity var(--ds-motion-micro) var(--ds-motion-ease)',
        }}
      >
        <polyline points='3.5,8 7,11 12.5,5' fill='none' stroke='currentColor' strokeWidth='2' strokeLinecap='round' strokeLinejoin='round' />
      </Box>
      {visualState === 'indeterminate' && (
        <Box
          component='span'
          sx={{
            width: '60%',
            height: '2px',
            backgroundColor: 'currentColor',
            borderRadius: '1px',
          }}
        />
      )}
    </Box>
  );

  const nativeInput = (
    <Box
      component='input'
      ref={inputRef}
      type='checkbox'
      id={inputId}
      name={name}
      value={value}
      checked={checked}
      disabled={disabled}
      aria-label={!hasLabel ? ariaLabel : undefined}
      onChange={(e) => onChange(e.currentTarget.checked)}
      sx={{
        position: 'absolute',
        width: tokens.box,
        height: tokens.box,
        margin: 0,
        opacity: 0,
        cursor: disabled ? 'not-allowed' : 'pointer',
        '&:focus-visible + span': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '2px' },
      }}
    />
  );

  // checkbox-only composition: just the box, no <label>.
  if (!hasLabel) {
    return (
      <Box
        className={className}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          position: 'relative',
          width: tokens.box,
          height: tokens.box,
          cursor: disabled ? 'not-allowed' : 'pointer',
        }}
      >
        {nativeInput}
        <Box component='span' sx={{ display: 'inline-flex' }}>
          {visualBox}
        </Box>
      </Box>
    );
  }

  // label-right or label-right+description
  return (
    <Box
      component='label'
      htmlFor={inputId}
      className={className}
      sx={{
        display: 'inline-flex',
        alignItems: hasDescription ? 'flex-start' : 'center',
        gap: tokens.gap,
        cursor: disabled ? 'not-allowed' : 'pointer',
        color: disabled ? 'var(--ds-gray-500)' : 'var(--ds-gray-700)',
        position: 'relative',
      }}
    >
      <Box
        sx={{
          position: 'relative',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: tokens.box,
          height: tokens.box,
          marginTop: hasDescription ? '2px' : 0,
          flexShrink: 0,
        }}
      >
        {nativeInput}
        {visualBox}
      </Box>
      {hasDescription ? (
        <Box component='span'>
          <Box
            component='span'
            sx={{
              display: 'block',
              fontSize: tokens.fontSize,
              fontWeight: 'var(--ds-font-weight-medium)',
            }}
          >
            {label}
          </Box>
          <Box
            component='span'
            sx={{
              display: 'block',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
              marginTop: '2px',
            }}
          >
            {description}
          </Box>
        </Box>
      ) : (
        <Box component='span' sx={{ fontSize: tokens.fontSize }}>
          {label}
        </Box>
      )}
    </Box>
  );
}

export default Checkbox;
