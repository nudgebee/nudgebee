import React, { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import PropTypes from 'prop-types';
import { Drawer, Box, Typography, IconButton, Paper } from '@mui/material';
import { useForkRef } from '@mui/material/utils';
import { Transition } from 'react-transition-group';
import CloseIcon from '@mui/icons-material/Close';
import { ds } from 'src/utils/colors';

const SECONDARY_TRANSITION_MS = 360;
const SECONDARY_EASE = 'cubic-bezier(0.16, 1, 0.3, 1)';

const DRAWER_MS = 420;
const DRAWER_EASE = 'cubic-bezier(0.16, 1, 0.3, 1)';
const DRAWER_TRANSITION = `opacity ${DRAWER_MS}ms ${DRAWER_EASE}, transform ${DRAWER_MS}ms ${DRAWER_EASE}`;

// Read a layout prop to flush pending styles so the browser animates from the
// hidden state instead of snapping straight to shown.
const reflow = (node) => node && node.scrollTop;

const SoftDrawerTransition = React.forwardRef(function SoftDrawerTransition({ children, in: inProp, onEnter, onExited }, ref) {
  const nodeRef = useRef(null);
  const handleRef = useForkRef(nodeRef, ref);
  return (
    <Transition
      nodeRef={nodeRef}
      in={inProp}
      appear
      timeout={DRAWER_MS}
      // MUI's Modal relies on onEnter/onExited to flush styles and to unmount.
      onEnter={() => {
        reflow(nodeRef.current);
        onEnter?.(nodeRef.current);
      }}
      onExited={() => onExited?.(nodeRef.current)}
    >
      {(state) => {
        const shown = state === 'entering' || state === 'entered';
        return React.cloneElement(children, {
          ref: handleRef,
          style: {
            transition: DRAWER_TRANSITION,
            opacity: shown ? 1 : 0,
            transform: shown ? 'translateX(0)' : 'translateX(24px)',
            ...children.props.style,
          },
        });
      }}
    </Transition>
  );
});

SoftDrawerTransition.propTypes = {
  children: PropTypes.element.isRequired,
  in: PropTypes.bool,
  onEnter: PropTypes.func,
  onExited: PropTypes.func,
};

const STORAGE_KEY = 'nb.customDrawer.width';
const MIN_WIDTH = 320;
const MAX_WIDTH_FRACTION = 0.7;

const getMaxWidth = () => (typeof window === 'undefined' ? 1200 : Math.floor(window.innerWidth * MAX_WIDTH_FRACTION));

const resolveInitialWidth = (raw) => {
  if (typeof raw === 'number') {
    return raw;
  }
  if (typeof raw !== 'string') {
    return 880;
  }
  if (raw.endsWith('px')) {
    return parseInt(raw, 10);
  }
  if (raw.endsWith('%') && typeof window !== 'undefined') {
    return Math.round((parseFloat(raw) / 100) * window.innerWidth);
  }
  const n = parseFloat(raw);
  return Number.isFinite(n) ? n : 880;
};

const readPersistedWidth = (key) => {
  if (typeof window === 'undefined' || !key) {
    return null;
  }
  try {
    const raw = window.localStorage.getItem(key);
    const n = raw ? parseInt(raw, 10) : NaN;
    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
};

const useDrawerResize = (defaultWidth, storageKey = STORAGE_KEY, enabled = true) => {
  const [width, setWidth] = useState(() =>
    enabled ? readPersistedWidth(storageKey) ?? resolveInitialWidth(defaultWidth) : resolveInitialWidth(defaultWidth)
  );
  const [viewportWidth, setViewportWidth] = useState(() => (typeof window === 'undefined' ? 1920 : window.innerWidth));
  const isResizingRef = useRef(false);
  const widthRef = useRef(width);
  const storageKeyRef = useRef(storageKey);

  useEffect(() => {
    widthRef.current = width;
  }, [width]);

  useEffect(() => {
    storageKeyRef.current = storageKey;
  }, [storageKey]);

  useEffect(() => {
    if (!enabled || typeof window === 'undefined') {
      return undefined;
    }
    const onResize = () => setViewportWidth(window.innerWidth);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [enabled]);

  useEffect(() => {
    if (!enabled) {
      return undefined;
    }
    const handleMouseMove = (e) => {
      if (!isResizingRef.current) {
        return;
      }
      const next = window.innerWidth - e.clientX;
      setWidth(Math.max(MIN_WIDTH, Math.min(next, getMaxWidth())));
    };
    const handleMouseUp = () => {
      if (!isResizingRef.current) {
        return;
      }
      isResizingRef.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      if (storageKeyRef.current) {
        try {
          window.localStorage.setItem(storageKeyRef.current, String(widthRef.current));
        } catch {
          /* no-op */
        }
      }
    };
    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', handleMouseUp);
    return () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };
  }, [enabled]);

  const handleMouseDown = (e) => {
    if (!enabled) {
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    isResizingRef.current = true;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  };

  const clampedWidth = Math.max(MIN_WIDTH, Math.min(width, Math.floor(viewportWidth * MAX_WIDTH_FRACTION)));
  return { width: clampedWidth, handleMouseDown };
};

const ResizeHandle = ({ onMouseDown, ariaLabel = 'Resize drawer' }) => (
  <Box
    onMouseDown={onMouseDown}
    aria-label={ariaLabel}
    sx={{
      position: 'absolute',
      left: '-3px',
      top: 0,
      bottom: 0,
      width: ds.space.mul(0, 3),
      cursor: 'col-resize',
      zIndex: 1,
      backgroundColor: 'transparent',
      transition: 'background-color 0.15s ease',
      '&:hover, &:active': { backgroundColor: ds.blue[500] },
    }}
  />
);

ResizeHandle.propTypes = {
  onMouseDown: PropTypes.func.isRequired,
  ariaLabel: PropTypes.string,
};

const DrawerHeader = ({ title, onClose }) => (
  <Box
    sx={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      px: 'var(--ds-space-4)',
      py: 'var(--ds-space-3)',
      borderBottom: `1px solid ${ds.blue[300]}`,
      flexShrink: 0,
    }}
  >
    <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', fontFamily: 'Roboto', color: ds.brand[500] }}>
      {title}
    </Typography>
    <IconButton onClick={onClose} size='small' data-testid='custom-drawer-close'>
      <CloseIcon fontSize='small' />
    </IconButton>
  </Box>
);

DrawerHeader.propTypes = {
  title: PropTypes.node,
  onClose: PropTypes.func.isRequired,
};

const MODERN_INSET = 16;
const MODERN_RADIUS = 10;

const CustomDrawer = ({
  open,
  onClose,
  title,
  width = ds.space.mul(0, 225),
  children,
  onWidthChange,
  resizable = true,
  variant = 'default',
  bare = false,
  storageKey = STORAGE_KEY,
  nonModal = false,
}) => {
  const { width: drawerWidth, handleMouseDown } = useDrawerResize(width, storageKey, resizable);
  const effectiveWidth = drawerWidth;
  const isModern = variant === 'modern';

  useEffect(() => {
    onWidthChange?.(effectiveWidth);
  }, [effectiveWidth, onWidthChange]);

  return (
    <Drawer
      anchor='right'
      variant='temporary'
      open={open}
      onClose={onClose}
      // In non-modal mode the Modal root would still cover the viewport and
      // swallow clicks, so make the root click-through and re-enable pointer
      // events on the Paper only — letting the user work with the page behind.
      sx={{ zIndex: 1200, ...(nonModal && { pointerEvents: 'none' }) }}
      TransitionComponent={SoftDrawerTransition}
      transitionDuration={DRAWER_MS}
      hideBackdrop={nonModal}
      ModalProps={
        nonModal
          ? {
              // Keep focus, scrolling, and clicks free for the page behind.
              disableScrollLock: true,
              disableEnforceFocus: true,
              disableAutoFocus: true,
              disableRestoreFocus: true,
            }
          : undefined
      }
      slotProps={{
        backdrop: {
          sx: { backgroundColor: `color-mix(in srgb, ${ds.gray[700]} 15%, transparent)` },
        },
      }}
      PaperProps={{
        tabIndex: -1,
        sx: {
          width: `${effectiveWidth}px`,
          maxWidth: '100vw',
          overflow: 'hidden',
          border: `1px solid ${ds.gray.alpha[300]}`,
          overscrollBehavior: 'contain',
          ...(nonModal && { pointerEvents: 'auto' }),
          ...(isModern
            ? {
                top: `${MODERN_INSET}px`,
                bottom: `${MODERN_INSET}px`,
                right: `${MODERN_INSET}px`,
                height: 'auto',
                borderRadius: `${MODERN_RADIUS}px`,
                backgroundColor: ds.background[200],
                boxShadow: `0 ${ds.space[3]} ${ds.space[4]} color-mix(in srgb, ${ds.gray[700]} 16%, transparent)`,
              }
            : {
                boxShadow: `${ds.space.mul(1, -1)} 0 ${ds.space[3]} ${ds.gray.alpha[200]}`,
              }),
        },
      }}
    >
      {resizable && <ResizeHandle onMouseDown={handleMouseDown} />}
      {bare ? (
        children
      ) : (
        <>
          <DrawerHeader title={title} onClose={onClose} />
          <Box sx={{ flex: 1, overflowY: 'auto', px: 'var(--ds-space-4)', py: 'var(--ds-space-4)' }}>{children}</Box>
        </>
      )}
    </Drawer>
  );
};

CustomDrawer.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  title: PropTypes.node,
  width: PropTypes.string,
  children: PropTypes.node,
  onWidthChange: PropTypes.func,
  resizable: PropTypes.bool,
  variant: PropTypes.oneOf(['default', 'modern']),
  bare: PropTypes.bool,
  storageKey: PropTypes.string,
  nonModal: PropTypes.bool,
};

const SecondaryDrawer = ({ open, onClose, title, rightOffset = 0, defaultWidth = '40%', children, variant = 'default' }) => {
  const isModern = variant === 'modern';
  const SECONDARY_GAP = 8;
  const wrapperRight = isModern ? rightOffset + MODERN_INSET + SECONDARY_GAP : rightOffset;
  const wrapperTop = isModern ? MODERN_INSET : 0;
  const wrapperBottom = isModern ? MODERN_INSET : 0;

  const [viewportWidth, setViewportWidth] = useState(() => (typeof window === 'undefined' ? 1920 : window.innerWidth));
  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const onResize = () => setViewportWidth(window.innerWidth);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);
  const SECONDARY_LEFT_MARGIN = 24;
  const requestedWidth = resolveInitialWidth(defaultWidth);
  const availableWidth = Math.max(MIN_WIDTH, viewportWidth - wrapperRight - SECONDARY_LEFT_MARGIN);
  const effectiveWidth = Math.min(requestedWidth, availableWidth);

  const [mounted, setMounted] = useState(open);
  const [revealed, setRevealed] = useState(false);
  useEffect(() => {
    if (open) {
      setMounted(true);
      const r1 = requestAnimationFrame(() => {
        const r2 = requestAnimationFrame(() => setRevealed(true));
        return () => cancelAnimationFrame(r2);
      });
      return () => cancelAnimationFrame(r1);
    }
    setRevealed(false);
    const t = setTimeout(() => setMounted(false), SECONDARY_TRANSITION_MS);
    return () => clearTimeout(t);
  }, [open]);

  if (typeof document === 'undefined' || !mounted) {
    return null;
  }

  return createPortal(
    <Box
      aria-hidden={!open}
      sx={{
        position: 'fixed',
        top: `${wrapperTop}px`,
        bottom: `${wrapperBottom}px`,
        right: `${wrapperRight}px`,
        width: revealed ? `${effectiveWidth}px` : '0px',
        maxWidth: '100vw',
        overflow: 'hidden',
        zIndex: 1401,
        transition: `width ${SECONDARY_TRANSITION_MS}ms ${SECONDARY_EASE}`,
        pointerEvents: revealed ? 'auto' : 'none',
        ...(isModern && { borderRadius: `${MODERN_RADIUS}px` }),
      }}
    >
      <Paper
        elevation={8}
        square={!isModern}
        sx={{
          position: 'absolute',
          top: 0,
          bottom: 0,
          right: 0,
          width: `${effectiveWidth}px`,
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
          backgroundColor: ds.background[100],
          ...(isModern
            ? {
                borderRadius: `${MODERN_RADIUS}px`,
                boxShadow: `0 ${ds.space[3]} ${ds.space[6]} color-mix(in srgb, ${ds.gray[700]} 16%, transparent)`,
              }
            : {
                boxShadow: `${ds.space.mul(1, -1)} 0 ${ds.space[3]} ${ds.gray.alpha[200]}`,
                borderRight: `1px solid ${ds.blue[500]}`,
              }),
        }}
      >
        <DrawerHeader title={title} onClose={onClose} />
        <Box sx={{ flex: 1, overflowY: 'auto', px: 'var(--ds-space-4)', py: 'var(--ds-space-4)' }}>{children}</Box>
      </Paper>
    </Box>,
    document.body
  );
};

SecondaryDrawer.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  title: PropTypes.node,
  rightOffset: PropTypes.number,
  defaultWidth: PropTypes.string,
  children: PropTypes.node,
  variant: PropTypes.oneOf(['default', 'modern']),
};

export { SecondaryDrawer };
export default CustomDrawer;
