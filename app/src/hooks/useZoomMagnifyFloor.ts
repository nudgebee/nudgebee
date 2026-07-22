import { useEffect, useState } from 'react';

/**
 * The app shell's "zoom floor": the layout-viewport width measured at load (and re-measured on
 * genuine window resizes / monitor-DPI changes, but NOT on browser zoom). Pinning the shell's
 * min-width to this value makes browser zoom (Ctrl/Cmd +/-) magnify-and-pan the UI instead of
 * reflowing it — panels keep their shape and a scrollbar lets you pan, like a trackpad pinch-zoom.
 *
 * Zoom is told apart from real geometry changes by the viewport's PHYSICAL pixel width
 * (clientWidth × devicePixelRatio): browser zoom only moves the CSS-px ↔ device-px mapping, so the
 * physical width stays constant, whereas a real resize or monitor-DPI change alters it. We re-measure
 * the floor only when the physical width moves. Returns undefined until mounted (SSR-safe).
 *
 * (Safari doesn't change devicePixelRatio on zoom, so there the floor re-measures and the layout
 * falls back to reflowing.)
 */
export const useZoomMagnifyFloor = (): number | undefined => {
  const [minWidth, setMinWidth] = useState<number | undefined>(undefined);
  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const physicalWidth = () => document.documentElement.clientWidth * (window.devicePixelRatio || 1);
    let lastPhysicalWidth = physicalWidth();
    const measure = () => setMinWidth(document.documentElement.clientWidth);
    measure();
    const onResize = () => {
      const width = physicalWidth();
      // Pure zoom leaves the physical width unchanged → keep the floor frozen so the UI magnifies.
      // A real resize or monitor-DPI change moves it (>5 device-px, past rounding) → re-fit.
      if (Math.abs(width - lastPhysicalWidth) > 5) {
        lastPhysicalWidth = width;
        measure();
      }
    };
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);
  return minWidth;
};

/**
 * True when the frozen zoom floor is at or below `maxWidth` — a zoom-safe replacement for
 * `useMediaQuery('(max-width:${maxWidth}px)')` in the app shell. It reflects the real screen width
 * at load (so small screens still get their compact/responsive layout) but, because the floor is
 * frozen, it does NOT flip while the user zooms — avoiding the mid-zoom restyle that breaks the
 * magnify illusion. Returns false until mounted.
 */
export const useZoomFloorBelow = (maxWidth: number): boolean => {
  const floor = useZoomMagnifyFloor();
  return floor != null && floor <= maxWidth;
};
