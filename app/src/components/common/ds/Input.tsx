/**
 * Input — unified text-entry primitive.
 * Spec: app/design-system/primitives/forms/input.html
 *
 * Successor to both `ds/TextField` (strict, incomplete) and `common/CustomTextField`
 * (battle-tested, loose). Composition pattern is DS V2's; the legacy features
 * carried over are: `instructionText`, `leadingIcon`, `trailingIcon`,
 * `minRows`/`maxRows`. The legacy escape hatches (`InputProps`, `inputProps`,
 * `sx`, `showActiveState`, `activeColor`, boolean `error`, `variant`) are
 * intentionally dropped — they're the reason chrome drifts across the app.
 *
 * Don'ts (per proposal):
 *   - Don't pass a boolean to `error`. String form forces an explanation.
 *   - Don't combine `leadingIcon` with `prefix` (or `trailingIcon` with `suffix`)
 *     on the same side — pick one per side.
 *   - Don't combine `readonly` and `disabled`. Readonly wins if both set.
 *   - Don't reach for a wrapper to override styling; extend the API instead.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type InputSize = 'sm' | 'md' | 'lg';
export type InputType = 'text' | 'number' | 'email' | 'password' | 'url' | 'textarea';

export interface InputProps {
  value: string;
  onChange: (next: string) => void;
  label?: React.ReactNode;
  /** Renders between label and input. Use for context the label can't carry. */
  instructionText?: React.ReactNode;
  /** Renders below the input. Hidden when `error` is set. */
  help?: React.ReactNode;
  /** Presence ⇒ error state. Message is required. */
  error?: string;
  /** Static affix outside the input bounds (e.g. "https://", "m/cpu"). */
  prefix?: React.ReactNode;
  suffix?: React.ReactNode;
  /** Icon inside the input bounds, on the leading edge. */
  leadingIcon?: React.ReactNode;
  trailingIcon?: React.ReactNode;
  size?: InputSize;
  type?: InputType;
  placeholder?: string;
  /** Animate the placeholder with a typewriter effect when the input is empty. */
  animatePlaceholder?: boolean;
  /** Typing speed in ms per character. Default 60. */
  typingSpeed?: number;
  required?: boolean;
  disabled?: boolean;
  readOnly?: boolean;
  inputMode?: 'text' | 'numeric' | 'decimal' | 'tel' | 'email' | 'url' | 'search';
  rows?: number;
  minRows?: number;
  maxRows?: number;
  name?: string;
  autoComplete?: string;
  onBlur?: React.FocusEventHandler<HTMLInputElement | HTMLTextAreaElement>;
  onFocus?: React.FocusEventHandler<HTMLInputElement | HTMLTextAreaElement>;
  onKeyDown?: React.KeyboardEventHandler<HTMLInputElement | HTMLTextAreaElement>;
  className?: string;
  id?: string;
}

type SizeToken = {
  height: string;
  fontSize: string;
  labelFontSize: string;
  padX: string;
  iconSize: string;
  labelGap: string;
};

const SIZE_TOKENS: Record<InputSize, SizeToken> = {
  sm: {
    height: '32px',
    fontSize: 'var(--ds-text-body)',
    labelFontSize: 'var(--ds-text-small)',
    padX: 'var(--ds-space-3)',
    iconSize: '16px',
    labelGap: '6px',
  },
  md: {
    height: '36px',
    fontSize: 'var(--ds-text-body)',
    labelFontSize: 'var(--ds-text-small)',
    padX: 'var(--ds-space-3)',
    iconSize: '16px',
    labelGap: '6px',
  },
  lg: {
    height: '40px',
    fontSize: 'var(--ds-text-body)',
    labelFontSize: 'var(--ds-text-body)',
    padX: 'var(--ds-space-4)',
    iconSize: '18px',
    labelGap: '6px',
  },
};

export function Input({
  value,
  onChange,
  label,
  instructionText,
  help,
  error,
  prefix,
  suffix,
  leadingIcon,
  trailingIcon,
  size = 'md',
  type = 'text',
  placeholder,
  animatePlaceholder = false,
  typingSpeed = 60,
  required,
  disabled,
  readOnly,
  inputMode,
  rows,
  minRows = 3,
  maxRows = 20,
  name,
  autoComplete,
  onBlur,
  onFocus,
  onKeyDown,
  className,
  id,
}: InputProps) {
  const tokens = SIZE_TOKENS[size];
  const reactId = React.useId();
  const inputId = id ?? reactId;
  const hasError = typeof error === 'string' && error.length > 0;
  const helpId = `${inputId}-help`;
  const errorId = `${inputId}-error`;
  const instrId = `${inputId}-instr`;
  const isTextarea = type === 'textarea';
  const textareaRef = React.useRef<HTMLTextAreaElement>(null);
  const styleCacheRef = React.useRef<{ lineH: number; padding: number } | null>(null);

  // Typewriter placeholder animation
  const [animatedPlaceholder, setAnimatedPlaceholder] = React.useState('');
  const charIndexRef = React.useRef(0);
  const timerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  React.useEffect(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }

    if (!animatePlaceholder || !placeholder || value) {
      charIndexRef.current = 0;
      setAnimatedPlaceholder('');
      return;
    }

    charIndexRef.current = 0;

    const type = () => {
      const idx = charIndexRef.current;
      if (idx <= (placeholder?.length ?? 0)) {
        setAnimatedPlaceholder(placeholder.slice(0, idx));
        charIndexRef.current = idx + 1;
        timerRef.current = setTimeout(type, typingSpeed);
      } else {
        // Pause at full text, then restart
        timerRef.current = setTimeout(() => {
          charIndexRef.current = 0;
          type();
        }, 1500);
      }
    };

    type();

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [animatePlaceholder, placeholder, value, typingSpeed]);

  const effectivePlaceholder = animatePlaceholder ? animatedPlaceholder : placeholder;

  React.useEffect(() => {
    const el = textareaRef.current;
    if (!el || !isTextarea) return;
    if (!styleCacheRef.current) {
      const { lineHeight, paddingTop, paddingBottom, fontSize } = window.getComputedStyle(el);
      const lineH = parseFloat(lineHeight) || parseFloat(fontSize) * 1.4;
      const padding = parseFloat(paddingTop) + parseFloat(paddingBottom);
      styleCacheRef.current = { lineH, padding };
    }
    const { lineH, padding } = styleCacheRef.current;
    el.style.minHeight = minRows ? `${minRows * lineH + padding}px` : '';
    el.style.height = 'auto';
    if (maxRows) {
      const cap = maxRows * lineH + padding;
      if (el.scrollHeight > cap) {
        el.style.height = `${cap}px`;
        el.style.overflow = 'auto';
      } else {
        el.style.height = `${el.scrollHeight}px`;
        el.style.overflow = 'hidden';
      }
    } else {
      el.style.height = `${el.scrollHeight}px`;
    }
  }, [value, isTextarea, maxRows, minRows]);

  // readOnly beats disabled per spec — disabled hides the value from copy-paste.
  const effectiveDisabled = disabled && !readOnly;

  // Inline-icon padding math: padX + icon + 6px gap = inset on that side.
  // Keeps the visual rhythm consistent across sizes when an icon is present.
  const leftInset = leadingIcon ? `calc(${tokens.padX} + ${tokens.iconSize} + 6px)` : tokens.padX;
  const rightInset = trailingIcon ? `calc(${tokens.padX} + ${tokens.iconSize} + 6px)` : tokens.padX;

  const describedBy =
    [hasError ? errorId : null, !hasError && help ? helpId : null, instructionText ? instrId : null].filter(Boolean).join(' ') || undefined;

  const inputBaseSx = {
    width: '100%',
    height: isTextarea ? 'auto' : tokens.height,
    padding: isTextarea ? `6px ${rightInset} 6px ${leftInset}` : `0 ${rightInset} 0 ${leftInset}`,
    // No fontFamily — value text inherits the body default (Roboto via MUI).
    // The label below uses --ds-font-display (Poppins) explicitly.
    fontSize: tokens.fontSize,
    lineHeight: 1.4,
    color: 'var(--ds-gray-700)',
    backgroundColor: effectiveDisabled ? 'var(--ds-background-200)' : 'var(--ds-background-100)',
    border: `1px solid ${hasError ? 'var(--ds-red-500)' : 'var(--ds-gray-300)'}`,
    borderRadius: prefix || suffix ? 0 : 'var(--ds-radius-md)',
    outline: 'none',
    boxSizing: 'border-box' as const,
    resize: isTextarea ? ('vertical' as const) : ('none' as const),
    overflow: isTextarea ? ('hidden' as const) : undefined,
    '&:hover': effectiveDisabled ? undefined : { borderColor: hasError ? 'var(--ds-red-600)' : 'var(--ds-gray-400)' },
    '&:focus': {
      borderColor: hasError ? 'var(--ds-red-500)' : 'var(--ds-blue-500)',
      boxShadow: `0 0 0 3px ${hasError ? 'var(--ds-red-100)' : 'var(--ds-blue-100)'}`,
    },
    '&:disabled': { color: 'var(--ds-gray-500)', cursor: 'not-allowed' },
    '&[readonly]': { backgroundColor: 'var(--ds-background-200)', cursor: 'default' },
    '&::placeholder': { color: 'var(--ds-gray-500)' },
    '&::-webkit-scrollbar': { width: '4px' },
    '&::-webkit-scrollbar-track': { background: 'transparent' },
    '&::-webkit-scrollbar-thumb': { background: 'var(--ds-gray-300)', borderRadius: '2px' },
    '&::-webkit-scrollbar-thumb:hover': { background: 'var(--ds-gray-400)' },
  };

  const sharedInputProps = {
    id: inputId,
    name,
    value,
    placeholder: effectivePlaceholder,
    required,
    disabled: effectiveDisabled,
    readOnly,
    autoComplete,
    'aria-invalid': hasError || undefined,
    'aria-describedby': describedBy,
    onBlur,
    onFocus,
    onKeyDown,
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => onChange(e.currentTarget.value),
    sx: inputBaseSx,
  };

  const inputEl = isTextarea ? (
    <Box component='textarea' ref={textareaRef} rows={rows} spellCheck={false} {...sharedInputProps} />
  ) : (
    <Box component='input' type={type} inputMode={inputMode} {...sharedInputProps} />
  );

  // When an inline icon is present, wrap the input in a position-relative shell
  // and absolute-position the icon. The input itself owns the padding inset.
  const inputWithIcons =
    leadingIcon || trailingIcon ? (
      <Box sx={{ position: 'relative', display: 'flex', width: '100%' }}>
        {leadingIcon && (
          <Box
            aria-hidden='true'
            sx={{
              position: 'absolute',
              left: tokens.padX,
              top: '50%',
              transform: 'translateY(-50%)',
              width: tokens.iconSize,
              height: tokens.iconSize,
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: 'var(--ds-gray-500)',
              pointerEvents: 'none',
              fontSize: tokens.iconSize,
            }}
          >
            {leadingIcon}
          </Box>
        )}
        {inputEl}
        {trailingIcon && (
          <Box
            sx={{
              position: 'absolute',
              right: tokens.padX,
              top: '50%',
              transform: 'translateY(-50%)',
              width: tokens.iconSize,
              height: tokens.iconSize,
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: 'var(--ds-gray-500)',
              fontSize: tokens.iconSize,
            }}
          >
            {trailingIcon}
          </Box>
        )}
      </Box>
    ) : (
      inputEl
    );

  const inputBlock =
    prefix || suffix ? (
      <Box sx={{ display: 'flex', alignItems: 'stretch', width: '100%' }}>
        {prefix && (
          <Box
            component='span'
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              padding: `0 ${tokens.padX}`,
              border: '1px solid var(--ds-gray-300)',
              borderRight: 0,
              borderRadius: 'var(--ds-radius-md) 0 0 var(--ds-radius-md)',
              backgroundColor: 'var(--ds-background-200)',
              color: 'var(--ds-gray-600)',
              fontSize: tokens.fontSize,
              whiteSpace: 'nowrap',
            }}
          >
            {prefix}
          </Box>
        )}
        <Box
          sx={{
            flex: 1,
            '& > input, & > textarea, & > div > input, & > div > textarea': {
              borderRadius:
                prefix && suffix ? 0 : prefix ? '0 var(--ds-radius-md) var(--ds-radius-md) 0' : 'var(--ds-radius-md) 0 0 var(--ds-radius-md)',
            },
          }}
        >
          {inputWithIcons}
        </Box>
        {suffix && (
          <Box
            component='span'
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              padding: `0 ${tokens.padX}`,
              border: '1px solid var(--ds-gray-300)',
              borderLeft: 0,
              borderRadius: '0 var(--ds-radius-md) var(--ds-radius-md) 0',
              backgroundColor: 'var(--ds-background-200)',
              color: 'var(--ds-gray-600)',
              fontSize: tokens.fontSize,
              whiteSpace: 'nowrap',
            }}
          >
            {suffix}
          </Box>
        )}
      </Box>
    ) : (
      inputWithIcons
    );

  return (
    <Box className={className} sx={{ display: 'flex', flexDirection: 'column', gap: tokens.labelGap, width: '100%' }}>
      {label !== undefined && (
        <Box
          component='label'
          htmlFor={inputId}
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: tokens.labelFontSize,
            color: 'var(--ds-gray-700)',
            fontWeight: 'var(--ds-font-weight-medium)',
          }}
        >
          {label}
          {required && (
            <Box component='span' aria-hidden='true' sx={{ color: 'var(--ds-red-500)', marginLeft: '2px' }}>
              *
            </Box>
          )}
        </Box>
      )}
      {instructionText !== undefined && (
        <Box component='span' id={instrId} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          {instructionText}
        </Box>
      )}
      {inputBlock}
      {!hasError && help !== undefined && (
        <Box component='span' id={helpId} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          {help}
        </Box>
      )}
      {hasError && (
        <Box component='span' id={errorId} role='alert' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-red-600)' }}>
          {error}
        </Box>
      )}
    </Box>
  );
}

export default Input;
