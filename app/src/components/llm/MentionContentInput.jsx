/**
 * MentionContentInput — a textarea that recognises `@agent` and `#tool`.
 *
 * Same idea as the prompt box in CreateFunction (type `@AgentName`, get a
 * coloured chip confirming it resolved), with two additions the prompt box
 * doesn't have: a suggestion list while you type, and the mention coloured
 * where it sits in the text rather than only in the summary strip.
 *
 * Mentions are plain text. Nothing here is persisted separately — the `@`/`#`
 * tokens travel inside the content string, exactly as typed.
 *
 * The in-place colouring is a mirror layer: an aria-hidden div sitting behind a
 * transparent-text textarea, painting the same string with the mention runs
 * wrapped. It only lines up if it inherits the textarea's own metrics, so the
 * font, padding and line height are read off the live element rather than
 * re-declared here — the textarea belongs to ds/Input and its styling is not
 * ours to duplicate.
 */
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { ds } from '@utils/colors';

// A mention starts at a boundary so an email address or a `#` inside a URL
// fragment isn't read as one. Names allow what agent and tool names actually
// use (llm identifiers are \w; tools add . and -) but must not END on a dot or
// dash — otherwise the full stop closing a sentence is swallowed into the name
// and the mention stops resolving.
const NAME = '\\w(?:[\\w.-]*\\w)?';
const BOUNDARY = '(?:^|[\\s([{,;])';
const ACTIVE_TOKEN_RE = new RegExp(`${BOUNDARY}([@#])([\\w.-]*)$`);
const ALL_TOKENS_RE = new RegExp(`${BOUNDARY}([@#])(${NAME})`, 'g');

const MAX_SUGGESTIONS = 8;
const SUGGESTIONS_WIDTH = 320;
const SUGGESTIONS_MAX_HEIGHT = 220;

// Computed-style properties that affect line wrapping / glyph metrics — the
// set a caret-position mirror needs to reproduce for its offsets to match the
// real textarea. Layout-only properties (width, padding, border) are copied
// separately since they come off the element's box, not the style object.
const CARET_MIRROR_FONT_PROPS = [
  'fontFamily',
  'fontSize',
  'fontWeight',
  'fontStyle',
  'letterSpacing',
  'textTransform',
  'wordSpacing',
  'lineHeight',
  'whiteSpace',
  'wordBreak',
  'tabSize',
];

/**
 * Pixel offset of `caretIndex` within `el`, relative to el's own top-left
 * (i.e. before scroll is subtracted). Standard "mirror div" trick: an
 * offscreen clone of the textarea holding the text up to the caret, with a
 * zero-width marker at the end — the marker's offset is the caret's.
 */
const measureCaretOffset = (el, caretIndex) => {
  const cs = window.getComputedStyle(el);
  const mirror = document.createElement('div');
  mirror.style.position = 'absolute';
  mirror.style.visibility = 'hidden';
  mirror.style.top = '0';
  mirror.style.left = '-9999px';
  mirror.style.whiteSpace = 'pre-wrap';
  mirror.style.overflowWrap = 'break-word';
  mirror.style.boxSizing = cs.boxSizing;
  mirror.style.width = `${el.clientWidth}px`;
  mirror.style.padding = cs.padding;
  mirror.style.border = `${cs.borderWidth} solid transparent`;
  CARET_MIRROR_FONT_PROPS.forEach((prop) => {
    mirror.style[prop] = cs[prop];
  });

  mirror.textContent = el.value.slice(0, caretIndex);
  const marker = document.createElement('span');
  // A trailing newline needs content after it to occupy the new line.
  marker.textContent = '​';
  mirror.appendChild(marker);
  document.body.appendChild(mirror);

  const offset = { top: marker.offsetTop, left: marker.offsetLeft, lineHeight: marker.offsetHeight };
  document.body.removeChild(mirror);
  return offset;
};

const TRIGGERS = {
  '@': { kind: 'agent', label: 'Agent', bg: 'var(--ds-blue-100)', fg: 'var(--ds-blue-700)' },
  '#': { kind: 'tool', label: 'Tool', bg: 'var(--ds-purple-100)', fg: 'var(--ds-purple-700)' },
};

/** Splits `text` into plain runs and resolved-mention runs, in order. */
const buildSegments = (text, agentNames, toolNames) => {
  const segments = [];
  let cursor = 0;
  ALL_TOKENS_RE.lastIndex = 0;
  let match = ALL_TOKENS_RE.exec(text);
  while (match) {
    const [whole, trigger, name] = match;
    // `whole` may include the leading boundary character; the mention itself
    // starts where the trigger does.
    const start = match.index + whole.length - name.length - 1;
    const known = trigger === '@' ? agentNames : toolNames;
    if (known.has(name)) {
      if (start > cursor) segments.push({ text: text.slice(cursor, start) });
      segments.push({ text: `${trigger}${name}`, trigger });
      cursor = start + name.length + 1;
    }
    match = ALL_TOKENS_RE.exec(text);
  }
  if (cursor < text.length) segments.push({ text: text.slice(cursor) });
  return segments;
};

/** Resolved mentions in first-seen order, for the summary strip. */
const collectMentions = (text, agentNames, toolNames) => {
  const seen = new Set();
  const out = [];
  ALL_TOKENS_RE.lastIndex = 0;
  let match = ALL_TOKENS_RE.exec(text);
  while (match) {
    const [, trigger, name] = match;
    const known = trigger === '@' ? agentNames : toolNames;
    const key = `${trigger}${name}`;
    if (known.has(name) && !seen.has(key)) {
      seen.add(key);
      out.push({ trigger, name });
    }
    match = ALL_TOKENS_RE.exec(text);
  }
  return out;
};

const MentionContentInput = ({
  value,
  onChange,
  agents = [],
  tools = [],
  suggestionsLoading = false,
  placeholder,
  disabled = false,
  minRows = 8,
  maxRows = 15,
  id,
  'data-testid': dataTestId,
  children,
}) => {
  const wrapRef = useRef(null);
  const overlayRef = useRef(null);
  // Caret position to restore after an insert lands in the DOM.
  const pendingCaret = useRef(null);
  // Set when handleKeyDown already preventDefault()'d a nav key (arrows,
  // Enter/Tab, Escape) — the caret didn't move, so the keyup listener below
  // must not re-derive `active` from it. Without this, every ArrowDown's
  // setHighlight(prev+1) was immediately undone by the keyup's setHighlight(0),
  // which is why the highlight never left the first row.
  const suppressNextSyncRef = useRef(false);
  // The blur handler defers closing the list so a click on a suggestion can
  // land first (see onBlur below) — tracked so unmounting mid-defer doesn't
  // leave a stray timer calling setActive after the component is gone.
  const blurTimeoutRef = useRef(null);
  const [active, setActive] = useState(null); // { trigger, query, start }
  const [highlight, setHighlight] = useState(0);
  // Copied off the live textarea so the mirror layer lines up character for
  // character. Empty until the first measure, which is why the overlay renders
  // nothing before then.
  const [metrics, setMetrics] = useState(null);

  const agentNames = useMemo(() => new Set(agents.map((a) => a.name).filter(Boolean)), [agents]);
  const toolNames = useMemo(() => new Set(tools.map((t) => t.name).filter(Boolean)), [tools]);

  const textarea = () => wrapRef.current?.querySelector('textarea') || null;

  const measure = useCallback(() => {
    const el = textarea();
    if (!el) return;
    const cs = window.getComputedStyle(el);
    setMetrics({
      top: el.offsetTop,
      left: el.offsetLeft,
      width: el.offsetWidth,
      height: el.offsetHeight,
      font: cs.font,
      fontFamily: cs.fontFamily,
      fontSize: cs.fontSize,
      fontWeight: cs.fontWeight,
      lineHeight: cs.lineHeight,
      letterSpacing: cs.letterSpacing,
      padding: cs.padding,
      borderWidth: cs.borderWidth,
      textAlign: cs.textAlign,
    });
  }, []);

  /** Reads the token the caret currently sits inside, or clears the list. */
  const syncActiveToken = useCallback(() => {
    const el = textarea();
    if (!el) return;
    const caret = el.selectionStart ?? 0;
    // A selection spanning characters isn't a typing position.
    if (el.selectionEnd !== caret) {
      setActive(null);
      return;
    }
    const match = ACTIVE_TOKEN_RE.exec(el.value.slice(0, caret));
    if (!match) {
      setActive(null);
      return;
    }
    const [, trigger, query] = match;
    const start = caret - query.length - 1;
    // Anchors the list to the line being typed on, not the textarea's edge —
    // otherwise the list opens far from the caret whenever there's blank space
    // below it (the original bug: it rendered at the bottom of the whole box).
    const caretOffset = measureCaretOffset(el, caret);
    setActive({ trigger, query, start, caretOffset });
    setHighlight(0);
  }, []);

  // Re-measure whenever the textarea can have resized: ds/Input autosizes it
  // between minRows and maxRows as the content grows.
  useEffect(() => {
    measure();
  }, [measure, value, minRows, maxRows]);

  useEffect(() => {
    const el = textarea();
    if (!el) return undefined;
    const syncScroll = () => {
      if (overlayRef.current) overlayRef.current.scrollTop = el.scrollTop;
    };
    // ds/Input forwards a fixed prop list and has no `onClick`/`onKeyUp`, so
    // caret moves that aren't edits (arrow keys, a click into the middle of the
    // text) are picked up straight off the element.
    const onCaretMove = () => {
      if (suppressNextSyncRef.current) {
        suppressNextSyncRef.current = false;
        return;
      }
      syncActiveToken();
    };
    el.addEventListener('scroll', syncScroll);
    el.addEventListener('keyup', onCaretMove);
    el.addEventListener('click', onCaretMove);
    window.addEventListener('resize', measure);
    return () => {
      el.removeEventListener('scroll', syncScroll);
      el.removeEventListener('keyup', onCaretMove);
      el.removeEventListener('click', onCaretMove);
      window.removeEventListener('resize', measure);
      if (blurTimeoutRef.current) clearTimeout(blurTimeoutRef.current);
    };
  }, [measure, syncActiveToken]);

  const suggestions = useMemo(() => {
    if (!active) return [];
    const source = active.trigger === '@' ? agents : tools;
    const needle = active.query.toLowerCase();
    const matches = needle
      ? source.filter((item) => item.name?.toLowerCase().includes(needle) || item.description?.toLowerCase().includes(needle))
      : source;
    return matches.slice(0, MAX_SUGGESTIONS);
  }, [active, agents, tools]);

  const insert = (item) => {
    if (!active) return;
    const el = textarea();
    const caret = el?.selectionStart ?? value.length;
    const rest = value.slice(caret);
    // Don't stack a second space when the caret is already sitting in front of
    // one — picking mid-sentence would otherwise leave a gap behind.
    const gap = /^\s/.test(rest) ? '' : ' ';
    const next = `${value.slice(0, active.start)}${active.trigger}${item.name}${gap}${rest}`;
    const caretAfter = active.start + item.name.length + 1 + gap.length;
    onChange(next);
    setActive(null);
    // The value round-trips through the parent, so the caret can only be placed
    // after React has committed the new text — see the layout effect below.
    pendingCaret.current = caretAfter;
  };

  // Deliberately a layout effect and not a requestAnimationFrame: rAF is
  // throttled in a background tab, and the deferred callback then lands after
  // whatever the user typed next, yanking the caret back mid-sentence.
  useLayoutEffect(() => {
    if (pendingCaret.current === null) return;
    const at = pendingCaret.current;
    pendingCaret.current = null;
    const el = textarea();
    if (!el) return;
    el.focus();
    el.setSelectionRange(at, at);
  });

  const handleKeyDown = (event) => {
    if (!active || suggestions.length === 0) {
      // Escape with no list open belongs to the modal, not to us.
      return;
    }
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      suppressNextSyncRef.current = true;
      setHighlight((prev) => (prev + 1) % suggestions.length);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      suppressNextSyncRef.current = true;
      setHighlight((prev) => (prev - 1 + suggestions.length) % suggestions.length);
    } else if (event.key === 'Enter' || event.key === 'Tab') {
      event.preventDefault();
      suppressNextSyncRef.current = true;
      insert(suggestions[highlight]);
    } else if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      suppressNextSyncRef.current = true;
      setActive(null);
    }
  };

  const segments = useMemo(() => buildSegments(value, agentNames, toolNames), [value, agentNames, toolNames]);
  const mentions = useMemo(() => collectMentions(value, agentNames, toolNames), [value, agentNames, toolNames]);

  // Caret-relative position for the suggestion list: right under the line
  // being typed on, clamped so it never runs past the textarea's own edges.
  // A synchronous DOM read (el.scrollTop) is fine here — this only runs while
  // `active` is set, i.e. during a render already triggered by that state change.
  const suggestionsPos = (() => {
    if (!active || !metrics) return null;
    const el = textarea();
    const wrapEl = wrapRef.current;
    if (!el || !wrapEl) return null;
    const lineHeight = active.caretOffset.lineHeight || parseFloat(metrics.lineHeight) || 20;
    // `top`/`left` are relative to the wrap box (position:absolute's containing
    // block); flip/clamp decisions need real screen space, which the textarea's
    // own box can't give when the caret sits near the top of a tall field —
    // that's what sent the list off the top of the page before this.
    const caretTopLocal = metrics.top + active.caretOffset.top - el.scrollTop;
    const wrapRect = wrapEl.getBoundingClientRect();
    const caretViewportY = wrapRect.top + caretTopLocal;
    const VIEWPORT_MARGIN = 8;
    const spaceBelow = window.innerHeight - VIEWPORT_MARGIN - (caretViewportY + lineHeight + 4);
    const spaceAbove = caretViewportY - VIEWPORT_MARGIN;
    // Prefer opening below (how the caret line reads); only flip above when
    // below is cramped and above genuinely has more room.
    const MIN_USABLE_HEIGHT = 160;
    const openBelow = spaceBelow >= MIN_USABLE_HEIGHT || spaceBelow >= spaceAbove;
    const maxHeight = Math.max(120, Math.min(SUGGESTIONS_MAX_HEIGHT, openBelow ? spaceBelow : spaceAbove));
    const top = openBelow ? caretTopLocal + lineHeight + 4 : caretTopLocal - maxHeight - 4;

    const width = Math.min(SUGGESTIONS_WIDTH, metrics.width);
    const maxLeft = metrics.left + metrics.width - width;
    const left = Math.max(metrics.left, Math.min(metrics.left + active.caretOffset.left, maxLeft));

    return { top, left, width, maxHeight };
  })();

  return (
    <Box>
      <Box
        ref={wrapRef}
        sx={{
          position: 'relative',
          // The mirror layer paints the text; the textarea keeps only its caret
          // and selection so the two never render the same glyphs twice.
          '& textarea': { position: 'relative', zIndex: 1, background: 'transparent', color: 'transparent', caretColor: 'var(--ds-gray-700)' },
          '& textarea::selection': { color: 'var(--ds-gray-700)' },
        }}
      >
        {metrics && (
          <Box
            ref={overlayRef}
            aria-hidden
            sx={{
              position: 'absolute',
              top: `${metrics.top}px`,
              left: `${metrics.left}px`,
              width: `${metrics.width}px`,
              height: `${metrics.height}px`,
              zIndex: 0,
              overflow: 'hidden',
              pointerEvents: 'none',
              whiteSpace: 'pre-wrap',
              overflowWrap: 'break-word',
              boxSizing: 'border-box',
              color: 'var(--ds-gray-700)',
              // Transparent border of the same width keeps the text box inset
              // identical to the textarea's.
              border: `${metrics.borderWidth} solid transparent`,
              padding: metrics.padding,
              fontFamily: metrics.fontFamily,
              fontSize: metrics.fontSize,
              fontWeight: metrics.fontWeight,
              lineHeight: metrics.lineHeight,
              letterSpacing: metrics.letterSpacing,
              textAlign: metrics.textAlign,
            }}
          >
            {segments.map((segment, index) =>
              segment.trigger ? (
                <Box
                  key={index}
                  component='span'
                  sx={{
                    backgroundColor: TRIGGERS[segment.trigger].bg,
                    color: TRIGGERS[segment.trigger].fg,
                    borderRadius: ds.radius.sm,
                    fontWeight: 'var(--ds-font-weight-medium)',
                  }}
                >
                  {segment.text}
                </Box>
              ) : (
                <span key={index}>{segment.text}</span>
              )
            )}
            {/* A trailing newline is not painted by the browser without this. */}
            {'\u200b'}
          </Box>
        )}

        <Input
          type='textarea'
          minRows={minRows}
          maxRows={maxRows}
          placeholder={placeholder}
          value={value}
          onChange={(next) => {
            onChange(next);
            // The DOM value updates before this fires, so the caret read inside
            // syncActiveToken is already the post-edit one.
            syncActiveToken();
          }}
          onKeyDown={handleKeyDown}
          onBlur={() => {
            // Let a click on a suggestion land before the list unmounts.
            blurTimeoutRef.current = setTimeout(() => setActive(null), 120);
          }}
          disabled={disabled}
          id={id}
          data-testid={dataTestId}
        />

        {active && suggestionsPos && (
          <Box
            data-testid='mention-suggestions'
            sx={{
              position: 'absolute',
              zIndex: 5,
              top: `${suggestionsPos.top}px`,
              left: `${suggestionsPos.left}px`,
              width: `${suggestionsPos.width}px`,
              maxHeight: `${suggestionsPos.maxHeight}px`,
              overflowY: 'auto',
              backgroundColor: 'var(--ds-background-100)',
              border: '1px solid var(--ds-gray-300)',
              borderRadius: ds.radius.md,
              boxShadow: '0 4px 12px rgba(0, 0, 0, 0.12)',
            }}
          >
            <Typography
              sx={{
                px: ds.space[3],
                py: ds.space[1],
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-500)',
                borderBottom: '1px solid var(--ds-gray-200)',
              }}
            >
              {active.trigger === '@' ? 'Agents' : 'Tools'}
            </Typography>
            {suggestionsLoading && (
              <Typography sx={{ px: ds.space[3], py: ds.space[2], fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                Loading…
              </Typography>
            )}
            {!suggestionsLoading && suggestions.length === 0 && (
              <Typography sx={{ px: ds.space[3], py: ds.space[2], fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
                No {active.trigger === '@' ? 'agents' : 'tools'} match “{active.query}”.
              </Typography>
            )}
            {!suggestionsLoading &&
              suggestions.map((item, index) => (
                <Box
                  key={item.name}
                  // onMouseDown, not onClick: the textarea's blur fires first
                  // otherwise and the list is gone before the click resolves.
                  onMouseDown={(event) => {
                    event.preventDefault();
                    insert(item);
                  }}
                  onMouseEnter={() => setHighlight(index)}
                  aria-selected={index === highlight}
                  sx={{
                    px: ds.space[3],
                    py: ds.space[2],
                    cursor: 'pointer',
                    backgroundColor: index === highlight ? 'var(--ds-background-200)' : 'transparent',
                  }}
                >
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>
                    {active.trigger}
                    {item.name}
                  </Typography>
                  {(item.description || item.type) && (
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-caption)',
                        color: 'var(--ds-gray-500)',
                        display: '-webkit-box',
                        WebkitLineClamp: 1,
                        WebkitBoxOrient: 'vertical',
                        overflow: 'hidden',
                      }}
                    >
                      {item.description || item.type}
                    </Typography>
                  )}
                </Box>
              ))}
          </Box>
        )}

        {children}
      </Box>

      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mt: ds.space[1] }}>
        Type <b>@</b> to reference an agent, <b>#</b> to reference a tool.
      </Typography>

      {mentions.length > 0 && (
        <Box sx={{ mt: ds.space[2], display: 'flex', flexWrap: 'wrap', gap: ds.space[2] }} data-testid='mention-chips'>
          {mentions.map(({ trigger, name }) => (
            <Box
              key={`${trigger}${name}`}
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: ds.space[1],
                backgroundColor: TRIGGERS[trigger].bg,
                color: TRIGGERS[trigger].fg,
                px: ds.space[2],
                borderRadius: ds.radius.xl,
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
              }}
            >
              <Box component='span' sx={{ fontSize: 'var(--ds-text-caption)', opacity: 0.75 }}>
                {TRIGGERS[trigger].label}
              </Box>
              {trigger}
              {name}
            </Box>
          ))}
        </Box>
      )}
    </Box>
  );
};

MentionContentInput.propTypes = {
  value: PropTypes.string.isRequired,
  onChange: PropTypes.func.isRequired,
  /** [{ name, description }] — offered on `@`. */
  agents: PropTypes.array,
  /** [{ name, type, description }] — offered on `#`. */
  tools: PropTypes.array,
  suggestionsLoading: PropTypes.bool,
  placeholder: PropTypes.string,
  disabled: PropTypes.bool,
  minRows: PropTypes.number,
  maxRows: PropTypes.number,
  id: PropTypes.string,
  'data-testid': PropTypes.string,
  /** Rendered inside the input's positioned shell — e.g. an over-limit badge. */
  children: PropTypes.node,
};

export default MentionContentInput;
