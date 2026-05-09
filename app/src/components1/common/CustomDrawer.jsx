import React, { useEffect, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { Drawer, Box, Typography, IconButton } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import { colors } from 'src/utils/colors';

const STORAGE_KEY = 'nb.customDrawer.width';
const MIN_WIDTH = 320;
const MAX_WIDTH_FRACTION = 0.7;

const getMaxWidth = () => (typeof window === 'undefined' ? 1200 : Math.floor(window.innerWidth * MAX_WIDTH_FRACTION));

// Parses '880px' / '40%' / 880 into a pixel number against the current viewport.
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

const readPersistedWidth = () => {
  if (typeof window === 'undefined') {
    return null;
  }
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    const n = raw ? parseInt(raw, 10) : NaN;
    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
};

// Resize state for CustomDrawer — handles drag, viewport-aware clamping, and localStorage.
// `widthRef` mirrors `width` so the global mousemove/mouseup listeners can be registered
// once (with `[]` deps) instead of re-attaching on every drag tick.
const useDrawerResize = (defaultWidth) => {
  const [width, setWidth] = useState(() => readPersistedWidth() ?? resolveInitialWidth(defaultWidth));
  const [viewportWidth, setViewportWidth] = useState(() => (typeof window === 'undefined' ? 1920 : window.innerWidth));
  const isResizingRef = useRef(false);
  const widthRef = useRef(width);

  useEffect(() => {
    widthRef.current = width;
  }, [width]);

  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const onResize = () => setViewportWidth(window.innerWidth);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => {
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
      try {
        window.localStorage.setItem(STORAGE_KEY, String(widthRef.current));
      } catch {
        /* no-op — quota exceeded / private mode */
      }
    };
    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', handleMouseUp);
    return () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };
  }, []);

  const handleMouseDown = (e) => {
    e.preventDefault();
    e.stopPropagation();
    isResizingRef.current = true;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  };

  const clampedWidth = Math.max(MIN_WIDTH, Math.min(width, Math.floor(viewportWidth * MAX_WIDTH_FRACTION)));
  return { width: clampedWidth, handleMouseDown };
};

const CustomDrawer = ({ open, onClose, title, width = '450px', children }) => {
  const { width: drawerWidth, handleMouseDown } = useDrawerResize(width);

  return (
    <Drawer
      anchor='right'
      variant='temporary'
      open={open}
      onClose={onClose}
      sx={{ zIndex: 1400 }}
      PaperProps={{
        sx: {
          width: `${drawerWidth}px`,
          maxWidth: '100vw',
          boxShadow: '-4px 0 12px rgba(0, 0, 0, 0.08)',
          overflow: 'visible',
        },
      }}
    >
      {/* Resize handle — drag the left edge to widen / narrow. Mouse-only. */}
      <Box
        onMouseDown={handleMouseDown}
        aria-label='Resize drawer'
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

      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          px: '20px',
          py: '12px',
          borderBottom: `1px solid ${colors.border.primary}`,
          flexShrink: 0,
        }}
      >
        <Typography sx={{ fontSize: '15px', fontWeight: 500, fontFamily: 'Roboto', color: colors.text.secondary }}>{title}</Typography>
        <IconButton onClick={onClose} size='small' data-testid='custom-drawer-close'>
          <CloseIcon fontSize='small' />
        </IconButton>
      </Box>

      <Box sx={{ flex: 1, overflowY: 'auto', px: '20px', py: '16px' }}>{children}</Box>
    </Drawer>
  );
};

CustomDrawer.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  title: PropTypes.node,
  width: PropTypes.string,
  children: PropTypes.node,
};

export default CustomDrawer;
