/**
 * Form — DS V2 layout primitive for any form-shaped UI.
 *
 * Container-agnostic: works inside a `Modal`, inside a `Card`, on a settings
 * page, in an `Inspector`, or standalone. The container owns outer padding;
 * the Form owns internal layout (section spacing, field spacing, label
 * placement, row layout).
 *
 * Subcomponents:
 *   Form               — OPTIONAL wrapper. Use it only when you need a real
 *                        `<form>` element (`onSubmit`), or want to set a
 *                        default `variant` / `density` for every nested
 *                        Section. For a modal body that mixes form sections
 *                        with non-form UI, skip the wrapper and use
 *                        `Form.Section` directly with its own `density` prop.
 *   Form.Section       — labeled group with optional title + description.
 *                        Accepts per-section `density` and `variant` props —
 *                        these override the inherited context and propagate
 *                        to nested Form.Field / Form.Row. **Standalone-usable**;
 *                        does not require a `<Form>` ancestor.
 *   Form.Field         — label + description + control + helperText/error.
 *                        Standalone-usable; reads density/variant from the
 *                        nearest Form / Form.Section (defaults if neither).
 *   Form.Row           — horizontal layout for related fields (e.g. first/last name).
 *                        Standalone-usable.
 *   Form.Actions       — submit/cancel row. Omit when used inside a `Modal`
 *                        (Modal owns the footer).
 *
 * Wrapping pattern — when do you need `<Form>`?
 *   - You're rendering a real form that submits → `<Form onSubmit={...}>` wraps
 *     everything so the root is `<form noValidate>`.
 *   - Every section in the form should share the same density/variant → put it
 *     on `<Form>` once.
 *   - The modal body is a mix of form sections and non-form UI (status cards,
 *     custom badges, dividers, switches) → DON'T wrap; use `Form.Section` for
 *     the form-shaped parts and let the outer container handle the rest.
 *
 * Variants:
 *   `stacked` (default) — label above the control, full-width control.
 *                         The default for modal-shaped forms, create flows,
 *                         onboarding, and mobile.
 *   `split`             — label + description on the left (35%), control
 *                         on the right (65%). The settings-page pattern.
 *
 * Density (drives all internal gaps):
 *   comfortable — 24px field gap, 48px section gap, 16px row gap
 *   default     — 16px field gap, 32px section gap, 12px row gap   (default)
 *   compact     — 12px field gap, 24px section gap,  8px row gap
 *
 * Spec: app/design-system/primitives/form/form.html (to be created)
 *
 * Canonical labeling pattern:
 *   Inside `Form.Field`, the inner control must NOT set its own `label` prop.
 *   `Form.Field` owns the label rendering (positioning per variant, required
 *   asterisk, description, error/helper text). Pass the label to `Form.Field`,
 *   not to the wrapped Input / Select / etc.
 *
 *     // ✅ Right
 *     <Form.Field label='Email' required>
 *       <Input value={email} onChange={setEmail} />
 *     </Form.Field>
 *
 *     // ❌ Wrong — two labels with different visual specs
 *     <Form.Field label='Email' required>
 *       <Input label='Email' value={email} onChange={setEmail} />
 *     </Form.Field>
 *
 *   A dev-mode `console.warn` flags the double-label anti-pattern at runtime
 *   so it gets caught in PR review.
 *
 * Don't:
 *   - Don't set `label` on a control wrapped in `Form.Field` — Form.Field owns
 *     the label (see "Canonical labeling pattern" above).
 *   - Don't use `FilterDropdown` / `FilterDropdownButton` inside a Form — those
 *     are toolbar-filter affordances (pill trigger, clear-X, blue-when-active).
 *     Inside a Form, value pickers must be `ds/Select` (or `ds/Autocomplete`
 *     for async / free-typing). The field chrome matches `ds/Input` so Select
 *     rows align with Input rows in a form column. See §1.6 in
 *     COMPONENT_USAGE.md for the "form field vs toolbar filter" rule.
 *   - Don't use Form for non-form-shaped layouts (use Stack / Grid directly).
 *   - Don't expose 3+ column layouts at the Form level. Use `Form.Row ratio={...}`
 *     for related fields (first/last name, street/state/zip). Real forms aren't
 *     dense grids — eye-tracking shows users miss fields in multi-column forms.
 *   - Don't mix `Form.Actions` with a Modal's `confirmText`/`actionButtons`.
 *     When inside a Modal, the Modal owns the footer.
 *   - Don't set `fontFamily` on the control inside `Form.Field` — let the body
 *     default (Roboto via MUI theme) inherit, the label uses Poppins via
 *     `--ds-font-display`.
 */
import * as React from 'react';
import { Box, Typography } from '@mui/material';

export type FormVariant = 'stacked' | 'split';
export type FormDensity = 'comfortable' | 'default' | 'compact';
export type FormActionsAlign = 'left' | 'right' | 'between' | 'center';
export type FormRowGap = 'tight' | 'default' | 'wide';

interface DensityTokens {
  fieldGap: string;
  sectionGap: string;
  rowGap: string;
}

const DENSITY_TOKENS: Record<FormDensity, DensityTokens> = {
  comfortable: {
    fieldGap: 'var(--ds-space-5)', // 24
    sectionGap: 'var(--ds-space-7)', // 48
    rowGap: 'var(--ds-space-4)', // 16
  },
  default: {
    fieldGap: 'var(--ds-space-4)', // 16
    sectionGap: 'var(--ds-space-6)', // 32
    rowGap: 'var(--ds-space-3)', // 12
  },
  compact: {
    fieldGap: 'var(--ds-space-3)', // 12
    sectionGap: 'var(--ds-space-5)', // 24
    rowGap: 'var(--ds-space-2)', // 8
  },
};

const ROW_GAP_TOKENS: Record<FormRowGap, string> = {
  tight: 'var(--ds-space-2)', // 8
  default: 'var(--ds-space-3)', // 12
  wide: 'var(--ds-space-4)', // 16
};

// Split variant: label column width. GitHub uses ~35%, Figma ~50%. 35% reads as
// clearly "label-on-side" without crowding controls on tablet widths.
const SPLIT_LABEL_FLEX = '0 0 35%';

interface FormContextValue {
  variant: FormVariant;
  density: FormDensity;
}

const FormContext = React.createContext<FormContextValue>({
  variant: 'stacked',
  density: 'default',
});

/* ════════════════════════════════════════════════════════════════════════
   Form (root)
   ════════════════════════════════════════════════════════════════════════ */

export interface FormProps {
  /** Layout variant — controls label placement. Default `'stacked'`. */
  variant?: FormVariant;
  /** Internal spacing density. Default `'default'`. */
  density?: FormDensity;
  /**
   * Outer padding around the form content. Default `'none'` — the container
   * (Modal body, Card body, page wrapper) controls outer padding.
   */
  padding?: 'none' | 'comfortable';
  /**
   * Submit handler. When provided (and `asElement` is not explicitly false),
   * the root renders as a real `<form>` element with `noValidate`.
   */
  onSubmit?: (e: React.FormEvent<HTMLFormElement>) => void;
  /**
   * Force the root element type. Defaults to `<form>` when `onSubmit` is set,
   * otherwise `<div>`.
   */
  asElement?: boolean;
  id?: string;
  className?: string;
  children: React.ReactNode;
}

export function Form({ variant = 'stacked', density = 'default', padding = 'none', onSubmit, asElement, id, className, children }: FormProps) {
  const renderAsForm = asElement ?? !!onSubmit;
  const tokens = DENSITY_TOKENS[density];

  const stack = (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: tokens.sectionGap,
      }}
    >
      {children}
    </Box>
  );

  const padded = padding === 'comfortable' ? <Box sx={{ padding: 'var(--ds-space-5) var(--ds-space-6)' }}>{stack}</Box> : stack;

  const rootSx = { width: '100%' };

  return (
    <FormContext.Provider value={{ variant, density }}>
      {renderAsForm ? (
        <Box component='form' onSubmit={onSubmit} id={id} className={className} noValidate sx={rootSx}>
          {padded}
        </Box>
      ) : (
        <Box id={id} className={className} sx={rootSx}>
          {padded}
        </Box>
      )}
    </FormContext.Provider>
  );
}

/* ════════════════════════════════════════════════════════════════════════
   Form.Section
   ════════════════════════════════════════════════════════════════════════ */

export interface FormSectionProps {
  /** Section title — Poppins display font, semibold, gray-700. */
  title?: React.ReactNode;
  /** Description below the title — body-small, gray-500. */
  description?: React.ReactNode;
  /**
   * Render a horizontal divider above the section. Defaults to `false` —
   * section spacing alone is usually enough; use the divider for strong
   * visual rules between major groups.
   */
  divider?: boolean;
  /**
   * Per-section density override. Sets the field gap inside this section
   * AND propagates to all nested `Form.Field` / `Form.Row` via context.
   * Inherits from the wrapping `<Form>` (or the default `'default'`) when
   * unset. Use this to avoid wrapping a single section in its own `<Form>`
   * just to change density.
   */
  density?: FormDensity;
  /**
   * Per-section variant override (`stacked` vs `split`). Propagates to
   * nested `Form.Field` via context. Same rationale as `density`.
   */
  variant?: FormVariant;
  id?: string;
  children: React.ReactNode;
}

function Section({ title, description, divider = false, density, variant, id, children }: FormSectionProps) {
  const ctx = React.useContext(FormContext);
  // Effective density/variant: explicit prop overrides inherited Form (or default) context.
  const effectiveDensity: FormDensity = density ?? ctx.density;
  const effectiveVariant: FormVariant = variant ?? ctx.variant;
  const tokens = DENSITY_TOKENS[effectiveDensity];

  // Re-provide context when this section overrides anything, so nested
  // Form.Field / Form.Row pick up the section's density/variant rather than
  // a stale parent value.
  const overridesContext = density !== undefined || variant !== undefined;

  const content = (
    <Box
      id={id}
      sx={{
        ...(divider && {
          pt: 'var(--ds-space-5)',
          borderTop: '1px solid var(--ds-gray-200)',
        }),
      }}
    >
      {(title || description) && (
        <Box sx={{ mb: 'var(--ds-space-4)' }}>
          {title && (
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                fontFamily: 'var(--ds-font-display)',
                lineHeight: 1.3,
              }}
            >
              {title}
            </Typography>
          )}
          {description && (
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: 'var(--ds-gray-500)',
                mt: 'var(--ds-space-1)',
                lineHeight: 1.5,
              }}
            >
              {description}
            </Typography>
          )}
        </Box>
      )}
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          gap: tokens.fieldGap,
        }}
      >
        {children}
      </Box>
    </Box>
  );

  return overridesContext ? (
    <FormContext.Provider value={{ variant: effectiveVariant, density: effectiveDensity }}>{content}</FormContext.Provider>
  ) : (
    content
  );
}

Form.Section = Section;

/* ════════════════════════════════════════════════════════════════════════
   Form.Field
   ════════════════════════════════════════════════════════════════════════ */

export interface FormFieldProps {
  /** Label shown above the control (stacked) or to the left (split). */
  label?: React.ReactNode;
  /**
   * Description text. In `stacked` mode renders below the label; in `split`
   * mode renders below the label on the left side.
   */
  description?: React.ReactNode;
  /** Render a red asterisk after the label. */
  required?: boolean;
  /** Optional badge after the label (e.g. "Optional", "Recommended"). */
  badge?: React.ReactNode;
  /** Helper text below the control. Hidden when `error` is set. */
  helperText?: React.ReactNode;
  /** Error message — when set, helperText is hidden and the message is red. */
  error?: React.ReactNode;
  /**
   * Pass-through id. Wired to the label's `htmlFor` and cloned onto the first
   * child element as `id`. Provide for a11y when the wrapped control doesn't
   * already accept an `id` prop.
   */
  id?: string;
  children: React.ReactNode;
}

function Field({ label, description, required, badge, helperText, error, id, children }: FormFieldProps) {
  const { variant } = React.useContext(FormContext);

  // Dev warning: detect the double-label anti-pattern. When Form.Field renders
  // a label AND the wrapped control sets its own `label` prop, the user sees
  // two labels with different visual specs. The canonical rule per
  // COMPONENT_USAGE.md §2.3 is: inside Form.Field, the inner control must NOT
  // set `label` — Form.Field owns the label.
  //
  // Known false positive: FilterDropdownButton uses `label` as the dropdown's
  // placeholder/trigger text rather than a form label. That's an API smell on
  // FilterDropdownButton (its `label` prop overloads two concepts) — flag the
  // call site so it gets cleaned up to use a `placeholder` prop instead.
  if (process.env.NODE_ENV !== 'production' && label && React.isValidElement(children)) {
    const childLabel = (children.props as { label?: unknown }).label;
    if (typeof childLabel === 'string' && childLabel.length > 0) {
      const childName =
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        (children.type as any)?.displayName ?? (children.type as any)?.name ?? 'inner control';
      // eslint-disable-next-line no-console
      console.warn(
        `[ds/Form.Field] Double label detected. <Form.Field label=${JSON.stringify(label)}> ` +
          `wraps <${childName} label=${JSON.stringify(childLabel)}>. ` +
          `Inside Form.Field the inner control must not set \`label\` — remove it from the child. ` +
          `(See COMPONENT_USAGE.md §2.3.)`
      );
    }
  }

  const labelNode = label ? (
    <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-2)' }}>
      <Typography
        component='label'
        htmlFor={id}
        sx={{
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: 'var(--ds-gray-700)',
          fontFamily: 'var(--ds-font-display)',
          lineHeight: 1.4,
        }}
      >
        {label}
        {required && (
          <Box component='span' sx={{ color: 'var(--ds-red-500)', ml: '2px' }} aria-hidden='true'>
            *
          </Box>
        )}
      </Typography>
      {badge && (
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-500)',
            fontFamily: 'var(--ds-font-display)',
          }}
        >
          {badge}
        </Typography>
      )}
    </Box>
  ) : null;

  const descriptionNode = description ? (
    <Typography
      sx={{
        fontSize: 'var(--ds-text-caption)',
        color: 'var(--ds-gray-500)',
        mt: 'var(--ds-space-1)',
        lineHeight: 1.5,
      }}
    >
      {description}
    </Typography>
  ) : null;

  const helperOrError =
    error || helperText ? (
      <Typography
        sx={{
          fontSize: 'var(--ds-text-caption)',
          color: error ? 'var(--ds-red-500)' : 'var(--ds-gray-500)',
          mt: 'var(--ds-space-1)',
          lineHeight: 1.5,
        }}
        role={error ? 'alert' : undefined}
      >
        {error ?? helperText}
      </Typography>
    ) : null;

  // When id is set, inject it into the first child element so the label's
  // htmlFor wires correctly. Skip if the child already has an id.
  const control =
    id && React.isValidElement(children) && !(children.props as { id?: string }).id
      ? React.cloneElement(children as React.ReactElement<{ id?: string }>, { id })
      : children;

  if (variant === 'split') {
    return (
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-5)', alignItems: 'flex-start' }}>
        <Box sx={{ flex: SPLIT_LABEL_FLEX, pt: '6px' /* aligns with 32px input baseline */ }}>
          {labelNode}
          {descriptionNode}
        </Box>
        <Box sx={{ flex: '1 1 auto', minWidth: 0 }}>
          {control}
          {helperOrError}
        </Box>
      </Box>
    );
  }

  // stacked
  return (
    <Box>
      {labelNode}
      {descriptionNode}
      <Box sx={{ mt: labelNode || descriptionNode ? 'var(--ds-space-2)' : 0 }}>{control}</Box>
      {helperOrError}
    </Box>
  );
}

Form.Field = Field;

/* ════════════════════════════════════════════════════════════════════════
   Form.Row — horizontal layout for related fields.
   Collapses to a single column under 600px to keep dense rows readable on
   small viewports.
   ════════════════════════════════════════════════════════════════════════ */

export interface FormRowProps {
  /**
   * Column width ratio. Defaults to equal columns matching the child count.
   * Examples: `[1, 1]` = 50/50, `[2, 1]` = 66/33, `[2, 1, 1]` = 50/25/25.
   */
  ratio?: number[];
  /** Override the gap between fields (defaults to the Form's density rowGap). */
  gap?: FormRowGap;
  children: React.ReactNode;
}

function Row({ ratio, gap, children }: FormRowProps) {
  const { density } = React.useContext(FormContext);
  const childArray = React.Children.toArray(children);
  const effectiveRatio = ratio ?? childArray.map(() => 1);
  const gapValue = gap ? ROW_GAP_TOKENS[gap] : DENSITY_TOKENS[density].rowGap;

  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: effectiveRatio.map((r) => `${r}fr`).join(' '),
        gap: gapValue,
        '@media (max-width: 600px)': {
          gridTemplateColumns: '1fr',
        },
      }}
    >
      {children}
    </Box>
  );
}

Form.Row = Row;

/* ════════════════════════════════════════════════════════════════════════
   Form.Actions — submit/cancel row. Omit when used inside a Modal (the
   Modal owns the footer via `confirmText` or `actionButtons`).
   ════════════════════════════════════════════════════════════════════════ */

export interface FormActionsProps {
  /** Horizontal alignment. Default `'right'`. */
  align?: FormActionsAlign;
  /** Render a top divider above the actions row. Default `false`. */
  divider?: boolean;
  children: React.ReactNode;
}

function Actions({ align = 'right', divider = false, children }: FormActionsProps) {
  const justifyContent = align === 'right' ? 'flex-end' : align === 'left' ? 'flex-start' : align === 'between' ? 'space-between' : 'center';

  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent,
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
        ...(divider && {
          pt: 'var(--ds-space-4)',
          borderTop: '1px solid var(--ds-gray-200)',
        }),
      }}
    >
      {children}
    </Box>
  );
}

Form.Actions = Actions;

export default Form;
