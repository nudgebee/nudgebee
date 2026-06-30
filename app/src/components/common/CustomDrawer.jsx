import React, { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import PropTypes from 'prop-types';
import { Drawer, Box, Typography, IconButton, Paper } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import { colors } from 'src/utils/colors';

const SECONDARY_TRANSITION_MS = 260;

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
      width: '6px',
      cursor: 'col-resize',
      zIndex: 1,
      backgroundColor: 'transparent',
      transition: 'background-color 0.15s ease',
      '&:hover, &:active': { backgroundColor: colors.border.primary },
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
      borderBottom: `1px solid ${colors.border.primaryLight}`,
      flexShrink: 0,
    }}
  >
    <Typography
      sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', fontFamily: 'Roboto', color: colors.text.secondary }}
    >
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
const MODERN_RADIUS = 16;

const CustomDrawer = ({ open, onClose, title, width = '450px', children, onWidthChange, resizable = true, variant = 'default' }) => {
  const { width: drawerWidth, handleMouseDown } = useDrawerResize(width, STORAGE_KEY, resizable);
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
      sx={{ zIndex: 1200 }}
      slotProps={{
        backdrop: {
          sx: { backgroundColor: 'rgba(15, 23, 42, 0.25)' },
        },
      }}
      PaperProps={{
        sx: {
          width: `${effectiveWidth}px`,
          maxWidth: '100vw',
          overflow: 'hidden',
          ...(isModern
            ? {
                top: `${MODERN_INSET}px`,
                bottom: `${MODERN_INSET}px`,
                right: `${MODERN_INSET}px`,
                height: 'auto',
                borderRadius: `${MODERN_RADIUS}px`,
                backgroundColor: colors.background.tertiaryLightestest,
                boxShadow: '0 12px 32px rgba(0, 0, 0, 0.16)',
              }
            : {
                boxShadow: '-4px 0 12px rgba(0, 0, 0, 0.08)',
              }),
        },
      }}
    >
      {resizable && <ResizeHandle onMouseDown={handleMouseDown} />}
      <DrawerHeader title={title} onClose={onClose} />
      <Box sx={{ flex: 1, overflowY: 'auto', px: 'var(--ds-space-4)', py: 'var(--ds-space-4)' }}>{children}</Box>
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
        transition: `width ${SECONDARY_TRANSITION_MS}ms cubic-bezier(0.4, 0, 0.2, 1)`,
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
          backgroundColor: colors.background.white,
          ...(isModern
            ? {
                borderRadius: `${MODERN_RADIUS}px`,
                boxShadow: '0 12px 32px rgba(0, 0, 0, 0.16)',
              }
            : {
                boxShadow: '-4px 0 12px rgba(0, 0, 0, 0.08)',
                borderRight: `1px solid ${colors.border.primary}`,
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
