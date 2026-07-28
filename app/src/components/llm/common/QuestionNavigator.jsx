import { useCallback, useEffect, useRef, useState } from 'react';
import { Box, GlobalStyles } from '@mui/material';
import { LuList } from 'react-icons/lu';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';

const HOVER_CLOSE_GRACE_MS = 160;

const containerOverflows = (el) => !!el && el.scrollHeight > el.clientHeight + 4;

const MIN_QUESTIONS = 3;

const QuestionNavigator = ({ questions, scrollContainerRef, popup = false, spyOffset = 80, jumpOffset = 16 }) => {
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState(0);
  const rootRef = useRef(null);
  const handleRef = useRef(null);
  const rowRefs = useRef([]);
  const hideTimerRef = useRef(null);
  const focusFirstRowRef = useRef(false);

  const count = questions?.length ?? 0;

  // Scroll spy: current = last question whose top is at/above the spy line.
  useEffect(() => {
    if (count < MIN_QUESTIONS || typeof window === 'undefined') {
      return undefined;
    }
    let raf = null;
    const spy = () => {
      raf = null;
      const scroller = scrollContainerRef?.current;
      const usesContainer = containerOverflows(scroller);
      const atBottom = usesContainer
        ? scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 4
        : window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 4;
      if (atBottom) {
        setCurrent(count - 1);
        return;
      }

      const surfaceTop = usesContainer ? scroller.getBoundingClientRect().top : 0;
      // Scan from the end and stop at the first question above the spy line — it is
      // the "last one at/above the line" without measuring every earlier question.
      let idx = 0;
      for (let i = questions.length - 1; i >= 0; i--) {
        const el = document.getElementById(questions[i].id);
        if (el && el.getBoundingClientRect().top - surfaceTop <= spyOffset) {
          idx = i;
          break;
        }
      }
      setCurrent(idx);
    };
    const schedule = () => {
      if (raf === null) {
        raf = requestAnimationFrame(spy);
      }
    };
    schedule();
    window.addEventListener('scroll', schedule, { passive: true });
    const el = scrollContainerRef?.current;
    if (el) {
      el.addEventListener('scroll', schedule, { passive: true });
    }
    return () => {
      window.removeEventListener('scroll', schedule);
      if (el) {
        el.removeEventListener('scroll', schedule);
      }
      if (raf !== null) {
        cancelAnimationFrame(raf);
      }
    };
  }, [questions, scrollContainerRef, spyOffset, count]);

  // Close on Escape and outside-click while open.
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const onKey = (e) => e.key === 'Escape' && setOpen(false);
    const onDown = (e) => rootRef.current && !rootRef.current.contains(e.target) && setOpen(false);
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onDown);
    };
  }, [open]);

  useEffect(() => {
    if (open && focusFirstRowRef.current) {
      focusFirstRowRef.current = false;
      rowRefs.current[0]?.focus();
    }
  }, [open]);

  useEffect(() => () => clearTimeout(hideTimerRef.current), []);

  const jumpTo = useCallback(
    (q) => {
      const el = document.getElementById(q.id);
      if (!el) {
        return;
      }
      const scroller = scrollContainerRef?.current;
      // scrollIntoView is avoided deliberately: it scrolls every scrollable ancestor
      // (including the page behind the sidebar); scrolling the surface directly doesn't.
      if (containerOverflows(scroller)) {
        const delta = el.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
        scroller.scrollTo({ top: scroller.scrollTop + delta - jumpOffset, behavior: 'smooth' });
      } else {
        window.scrollTo({ top: window.scrollY + el.getBoundingClientRect().top - jumpOffset, behavior: 'smooth' });
      }
      el.classList.remove('qnav-flash');
      void el.offsetWidth; // restart the animation if the same card is jumped to twice
      el.classList.add('qnav-flash');
      setOpen(false);
    },
    [scrollContainerRef, jumpOffset]
  );

  const onRowKey = (e, i) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      rowRefs.current[Math.min(i + 1, count - 1)]?.focus();
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      (i <= 0 ? handleRef.current : rowRefs.current[i - 1])?.focus();
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      jumpTo(questions[i]);
    }
  };

  if (count < MIN_QUESTIONS) {
    return null;
  }

  return (
    <Box
      ref={rootRef}
      data-testid='question-navigator'
      onMouseEnter={() => {
        clearTimeout(hideTimerRef.current);
        setOpen(true);
      }}
      onMouseLeave={() => {
        hideTimerRef.current = setTimeout(() => {
          // Keyboard users can have focus inside the flyout while the pointer wanders
          // off — closing then would yank their focus away mid-navigation.
          if (rootRef.current && rootRef.current.contains(document.activeElement)) {
            return;
          }
          setOpen(false);
        }, HOVER_CLOSE_GRACE_MS);
      }}
      sx={{
        position: popup ? 'absolute' : 'fixed',
        inset: 0,
        pointerEvents: 'none',
        zIndex: 12,
      }}
    >
      <Box
        component='button'
        type='button'
        ref={handleRef}
        id='question-navigator-handle'
        aria-haspopup='menu'
        aria-expanded={open}
        aria-controls='question-navigator-flyout'
        aria-label='Jump to a question'
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || (e.key === 'Enter' && !open)) {
            e.preventDefault();
            focusFirstRowRef.current = true;
            setOpen(true);
            if (open) {
              rowRefs.current[0]?.focus();
            }
          }
        }}
        sx={{
          all: 'unset',
          boxSizing: 'border-box',
          position: 'absolute',
          top: '50%',
          right: 0,
          transform: 'translateY(-50%)',
          pointerEvents: 'auto',
          cursor: 'pointer',
          backgroundColor: 'var(--ds-gray-100)',
          color: 'var(--ds-gray-700)',
          border: `1px solid var(--ds-gray-300)`,
          borderRight: 0,
          borderRadius: `${ds.radius.lg} 0 0 ${ds.radius.lg}`,
          padding: '11px 7px',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: '7px',
          boxShadow: `0px 2px 6px ${ds.gray.alpha[200]}`,
          transition: 'background-color var(--ds-motion-micro), border-color var(--ds-motion-micro)',
          '&:hover': { backgroundColor: 'var(--ds-gray-200)', borderColor: 'var(--ds-blue-500)' },
          '&:focus-visible': { outline: `2px solid var(--ds-blue-500)`, outlineOffset: '2px' },
        }}
      >
        <LuList size={15} strokeWidth={1.6} aria-hidden='true' />
        <Box component='span' sx={{ fontFamily: ds.font.mono, fontSize: '10px', fontWeight: 'var(--ds-font-weight-semibold)' }}>
          {count}
        </Box>
      </Box>

      <Box
        id='question-navigator-flyout'
        role='menu'
        aria-label='Questions in this conversation'
        sx={{
          position: 'absolute',
          top: '50%',
          right: '34px',
          width: '244px',
          backgroundColor: 'var(--ds-overlay-bg)',
          borderRadius: 'var(--ds-overlay-radius)',
          boxShadow: 'var(--ds-overlay-shadow)',
          overflow: 'hidden',
          pointerEvents: 'auto',
          opacity: open ? 1 : 0,
          visibility: open ? 'visible' : 'hidden',
          transform: open ? 'translateY(-50%) translateX(0)' : 'translateY(-50%) translateX(8px)',
          transition:
            'opacity var(--ds-motion-panel) var(--ds-motion-ease), transform var(--ds-motion-panel) var(--ds-motion-ease), visibility var(--ds-motion-panel)',
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], padding: '11px 13px 9px', borderBottom: `1px solid var(--ds-gray-200)` }}>
          <Box
            component='span'
            sx={{ fontFamily: 'var(--ds-font-display)', fontWeight: 'var(--ds-font-weight-semibold)', fontSize: '12px', color: 'var(--ds-gray-700)' }}
          >
            Questions
          </Box>
          <Box
            component='span'
            sx={{
              marginLeft: 'auto',
              fontFamily: ds.font.mono,
              fontSize: '10px',
              fontWeight: 'var(--ds-font-weight-semibold)',
              backgroundColor: 'var(--ds-brand-100)',
              color: 'var(--ds-brand-600)',
              borderRadius: ds.radius.pill,
              padding: '2px 7px',
            }}
          >
            {count}
          </Box>
        </Box>
        <Box component='ul' sx={{ maxHeight: '250px', overflowY: 'auto', padding: '6px', margin: 0, listStyle: 'none' }}>
          {questions.map((q, i) => (
            <Box
              component='li'
              key={q.id}
              ref={(el) => (rowRefs.current[i] = el)}
              role='menuitem'
              tabIndex={-1}
              aria-current={i === current ? 'true' : undefined}
              onClick={() => jumpTo(q)}
              onKeyDown={(e) => onRowKey(e, i)}
              sx={{
                padding: '7px 8px',
                borderRadius: 'var(--ds-overlay-item-radius)',
                cursor: 'pointer',
                transition: 'background-color var(--ds-motion-micro)',
                fontFamily: ds.font.sans,
                fontSize: '11.5px',
                lineHeight: 1.35,
                color: 'var(--ds-gray-700)',
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
                '&:focus-visible': { outline: '2px solid var(--ds-brand-600)', outlineOffset: '-2px' },
                '&[aria-current="true"]': { backgroundColor: 'var(--ds-brand-100)' },
              }}
            >
              {q.text}
            </Box>
          ))}
        </Box>
      </Box>

      {/* Flash wash applied to the jumped-to question card. Global because the target
          element lives in MessageItem's subtree, outside this component. */}
      <GlobalStyles
        styles={{
          // No `to` keyframe on purpose: the browser animates back to the element's own
          // computed background (question cards are gray-100, not transparent).
          '@keyframes qnav-flash': {
            from: { backgroundColor: 'var(--ds-yellow-100)' },
          },
          '.qnav-flash': { animation: 'qnav-flash 1.2s var(--ds-motion-ease)' },
        }}
      />
    </Box>
  );
};

QuestionNavigator.propTypes = {
  questions: PropTypes.arrayOf(
    PropTypes.shape({
      id: PropTypes.string.isRequired,
      text: PropTypes.string.isRequired,
    })
  ).isRequired,
  scrollContainerRef: PropTypes.shape({ current: PropTypes.any }).isRequired,
  popup: PropTypes.bool,
  spyOffset: PropTypes.number,
  jumpOffset: PropTypes.number,
};

export default QuestionNavigator;
