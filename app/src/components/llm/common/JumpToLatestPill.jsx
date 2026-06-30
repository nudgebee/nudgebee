import { useEffect, useRef, useState } from 'react';
import { Box } from '@mui/material';
import { FaArrowDown } from 'react-icons/fa';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';

const SCROLL_THRESHOLD_PX = 200;

// Private to this file — the pill is the only consumer. Listens on whichever surface is
// actually scrolling: the container ref when it overflows, otherwise the window. This
// matters because in full-page chat mode the inner Box has only `minHeight: 100vh` (no
// height cap), so the window is the real scroll surface, while in sidebar/popup mode the
// Box itself is the scroll surface.
const useScrollPosition = (ref) => {
  const [metrics, setMetrics] = useState({ scrollTop: 0, scrollHeight: 0, clientHeight: 0 });
  const rafRef = useRef(null);

  useEffect(() => {
    const measure = () => {
      rafRef.current = null;
      const el = ref?.current;
      const containerOverflows = el && el.scrollHeight > el.clientHeight + 4;
      if (containerOverflows) {
        setMetrics({ scrollTop: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight });
      } else if (typeof window !== 'undefined') {
        const doc = document.documentElement;
        setMetrics({
          scrollTop: window.scrollY || doc.scrollTop || 0,
          scrollHeight: doc.scrollHeight,
          clientHeight: window.innerHeight,
        });
      }
    };
    const schedule = () => {
      if (rafRef.current != null) {
        return;
      }
      rafRef.current = requestAnimationFrame(measure);
    };

    measure();
    if (typeof window === 'undefined') {
      return undefined;
    }
    window.addEventListener('scroll', schedule, { passive: true });
    const el = ref?.current;
    if (el) {
      el.addEventListener('scroll', schedule, { passive: true });
    }
    let ro;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(schedule);
      if (el) {
        ro.observe(el);
        if (el.firstElementChild) {
          ro.observe(el.firstElementChild);
        }
      }
      if (document.body) {
        ro.observe(document.body);
      }
    }
    return () => {
      window.removeEventListener('scroll', schedule);
      if (el) {
        el.removeEventListener('scroll', schedule);
      }
      if (ro) {
        ro.disconnect();
      }
      if (rafRef.current != null) {
        cancelAnimationFrame(rafRef.current);
      }
    };
  }, [ref]);

  return metrics;
};

const JumpToLatestPill = ({ scrollContainerRef }) => {
  const { scrollTop, scrollHeight, clientHeight } = useScrollPosition(scrollContainerRef);
  const distanceFromBottom = Math.max(0, scrollHeight - scrollTop - clientHeight);
  const isAwayFromBottom = clientHeight > 0 && distanceFromBottom > SCROLL_THRESHOLD_PX;

  const handleClick = () => {
    const el = scrollContainerRef.current;
    // Mirror useScrollPosition's surface detection: scroll the container if it overflows,
    // otherwise scroll the window (full-page mode where the body is the real scroll surface).
    if (el && el.scrollHeight > el.clientHeight + 4) {
      el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
    } else if (typeof window !== 'undefined') {
      window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' });
    }
  };

  return (
    <Box
      sx={{
        position: 'absolute',
        bottom: '100%',
        left: '50%',
        transform: 'translateX(-50%)',
        mb: ds.space[3],
        opacity: isAwayFromBottom ? 1 : 0,
        pointerEvents: isAwayFromBottom ? 'auto' : 'none',
        transition: 'opacity 150ms ease-out',
        zIndex: 11,
      }}
    >
      <Box
        component='button'
        type='button'
        onClick={handleClick}
        aria-label='Scroll to latest message'
        data-testid='jump-to-latest-pill'
        sx={{
          all: 'unset',
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: ds.space[6],
          height: ds.space[6],
          borderRadius: '50%',
          border: `1px solid ${'var(--ds-gray-300)'}`,
          backgroundColor: 'var(--ds-background-100)',
          boxShadow: '0px 2px 6px rgba(0, 0, 0, 0.08)',
          cursor: 'pointer',
          color: 'var(--ds-gray-700)',
          transition: 'background-color 0.15s ease, border-color 0.15s ease',
          '&:hover': { backgroundColor: 'var(--ds-background-200)', borderColor: 'var(--ds-blue-500)' },
          '&:focus-visible': { outline: `${ds.space[0]} solid ${'var(--ds-blue-500)'}`, outlineOffset: '2px' },
        }}
      >
        <FaArrowDown size={12} />
      </Box>
    </Box>
  );
};

JumpToLatestPill.propTypes = {
  scrollContainerRef: PropTypes.shape({ current: PropTypes.any }).isRequired,
};

export default JumpToLatestPill;
