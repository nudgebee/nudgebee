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
 *
 * Forwards a ref to the native input/textarea (e.g. for selectionStart/
 * setSelectionRange caret work). This is the only escape hatch reintroduced
 * so far — InputProps/inputProps spreading stays out per the note above.
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
  /**
   * Phrases to cycle through while animating. Each is typed in, held, then
   * deleted back out before the next begins. Overrides `placeholder` for the
   * animation; falls back to `[placeholder]` when omitted.
   */
  placeholderSequence?: string[];
  /** Typing speed in ms per character. Default 60. */
  typingSpeed?: number;
  /** Backspace speed in ms per character for the fade-out. Default = typingSpeed / 2. */
  deletingSpeed?: number;
  required?: boolean;
  disabled?: boolean;
  readOnly?: boolean;
  inputMode?: 'text' | 'numeric' | 'decimal' | 'tel' | 'email' | 'url' | 'search';
  rows?: number;
  minRows?: number;
  maxRows?: number;
  name?: string;
  /**
   * Defaults to `new-password` on a password input. Pass `current-password`
   * on a sign-in field to opt back into saved-credential fill.
   */
  autoComplete?: string;
  onBlur?: React.FocusEventHandler<HTMLInputElement | HTMLTextAreaElement>;
  onFocus?: React.FocusEventHandler<HTMLInputElement | HTMLTextAreaElement>;
  onKeyDown?: React.KeyboardEventHandler<HTMLInputElement | HTMLTextAreaElement>;
  className?: string;
  id?: string;
  /**
   * Automation hook, forwarded to the native input/textarea. Declared explicitly
   * because this component destructures a fixed prop list and does NOT spread the
   * rest — without it a caller's `data-testid` is silently dropped. Same fix as
   * ds/Checkbox.
   */
  'data-testid'?: string;
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

export const Input = React.forwardRef<HTMLInputElement | HTMLTextAreaElement, InputProps>(function Input(
  {
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
    placeholderSequence,
    typingSpeed = 60,
    deletingSpeed,
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
    'data-testid': dataTestId,
  },
  forwardedRef
) {
  const tokens = SIZE_TOKENS[size];
  const reactId = React.useId();
  const inputId = id ?? reactId;
  const hasError = typeof error === 'string' && error.length > 0;
  const helpId = `${inputId}-help`;
  const errorId = `${inputId}-error`;
  const instrId = `${inputId}-instr`;
  const isTextarea = type === 'textarea';
  const textareaRef = React.useRef<HTMLTextAreaElement | null>(null);
  const styleCacheRef = React.useRef<{ lineH: number; padding: number } | null>(null);

  // Textarea needs its own ref for the auto-grow effect below, so merge it
  // with whatever ref the caller forwarded in rather than overwriting either.
  const setTextareaRef = React.useCallback(
    (node: HTMLTextAreaElement | null) => {
      textareaRef.current = node;
      if (typeof forwardedRef === 'function') {
        forwardedRef(node);
      } else if (forwardedRef) {
        (forwardedRef as React.MutableRefObject<HTMLInputElement | HTMLTextAreaElement | null>).current = node;
      }
    },
    [forwardedRef]
  );

  // Typewriter placeholder animation: type a phrase in, hold on it, delete it
  // back out (faster than typing), then advance to the next phrase and repeat.
  const [animatedPlaceholder, setAnimatedPlaceholder] = React.useState('');
  const timerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  // Serialize the phrase list so an inline-array prop doesn't restart the effect
  // on every render; a genuine content change still retriggers it.
  const sequenceKey = placeholderSequence?.join('\n') ?? '';

  React.useEffect(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }

    // A non-empty sequence wins; otherwise fall back to the single placeholder.
    const phrases = sequenceKey ? sequenceKey.split('\n') : placeholder ? [placeholder] : [];

    if (!animatePlaceholder || phrases.length === 0 || value) {
      setAnimatedPlaceholder('');
      return;
    }

    // Fade out runs quicker than fade in; hold on the full phrase, blink on empty.
    const deleteSpeed = deletingSpeed ?? Math.max(15, Math.round(typingSpeed / 2));
    const HOLD_FULL_MS = 1500;
    const HOLD_EMPTY_MS = 400;

    let phraseIdx = 0;
    let charIdx = 0;
    let deleting = false;

    const tick = () => {
      const phrase = phrases[phraseIdx];

      if (!deleting) {
        // Fade in — type forward one character at a time.
        charIdx += 1;
        setAnimatedPlaceholder(phrase.slice(0, charIdx));
        if (charIdx >= phrase.length) {
          deleting = true;
          timerRef.current = setTimeout(tick, HOLD_FULL_MS);
        } else {
          timerRef.current = setTimeout(tick, typingSpeed);
        }
      } else {
        // Fade out — delete backwards, quicker than we typed.
        charIdx -= 1;
        setAnimatedPlaceholder(phrase.slice(0, Math.max(0, charIdx)));
        if (charIdx <= 0) {
          deleting = false;
          phraseIdx = (phraseIdx + 1) % phrases.length;
          timerRef.current = setTimeout(tick, HOLD_EMPTY_MS);
        } else {
          timerRef.current = setTimeout(tick, deleteSpeed);
        }
      }
    };

    timerRef.current = setTimeout(tick, typingSpeed);

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [animatePlaceholder, placeholder, sequenceKey, value, typingSpeed, deletingSpeed]);

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

  // A password input with no `autocomplete` is a credential target: the browser
  // pairs it with whatever text field looks like a username, fills both, and
  // re-fills the secret as soon as it is cleared. Secrets we collect are new
  // values, not saved logins, so default to `new-password` — the one token
  // Chrome honours here, since it ignores `off` on password inputs.
  const effectiveAutoComplete = autoComplete ?? (type === 'password' ? 'new-password' : undefined);

  // Inline-icon padding math: padX + icon + 6px gap = inset on that side.
  // Keeps the visual rhythm consistent across sizes when an icon is present.
  const leftInset = leadingIcon ? `calc(${tokens.padX} + ${tokens.iconSize} + 6px)` : tokens.padX;
  const rightInset = trailingIcon ? `calc(${tokens.padX} + ${tokens.iconSize} + 6px)` : tokens.padX;

  const describedBy =
    [hasError ? errorId : null, !hasError && help ? helpId : null, instructionText ? instrId : null].filter(Boolean).join(' ') || undefined;

  const fillColor = effectiveDisabled || readOnly ? 'var(--ds-background-200)' : 'var(--ds-background-100)';
  const focusRing = `0 0 0 3px ${hasError ? 'var(--ds-red-100)' : 'var(--ds-blue-100)'}`;

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
      boxShadow: focusRing,
    },
    // Browser autofill paints its own blue fill that CSS can't override; an inset shadow covers it.
    '&:-webkit-autofill': {
      boxShadow: `inset 0 0 0 1000px ${fillColor}`,
      WebkitTextFillColor: effectiveDisabled ? 'var(--ds-gray-500)' : 'var(--ds-gray-700)',
    },
    '&:-webkit-autofill:focus': { boxShadow: `inset 0 0 0 1000px ${fillColor}, ${focusRing}` },
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
    'data-testid': dataTestId,
    value,
    placeholder: effectivePlaceholder,
    required,
    disabled: effectiveDisabled,
    readOnly,
    autoComplete: effectiveAutoComplete,
    'aria-invalid': hasError || undefined,
    'aria-describedby': describedBy,
    onBlur,
    onFocus,
    onKeyDown,
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => onChange(e.currentTarget.value),
    sx: inputBaseSx,
  };

  const inputEl = isTextarea ? (
    <Box component='textarea' ref={setTextareaRef} rows={rows} spellCheck={false} {...sharedInputProps} />
  ) : (
    <Box component='input' type={type} inputMode={inputMode} ref={forwardedRef} {...sharedInputProps} />
  );

  // Wrap the input in a position-relative shell and absolute-position any icon;
  // the input itself owns the padding inset. The shell is rendered even with no
  // icon so the <input> keeps its place in the tree: callers that show an icon
  // only once there is a value would otherwise remount it (and drop focus) on
  // the first keystroke.
  const inputWithIcons = (
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
});

Input.displayName = 'Input';

export default Input;
