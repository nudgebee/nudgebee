import React from 'react';

/**
 * Wraps an MUI icon component so it always renders in the design system's
 * brand blue (`--ds-blue-500`) instead of inheriting the surrounding text
 * color. MUI icons default to `currentColor`, so an unstyled one dropped
 * into a nav dropdown next to custom SVG icons (which hardcode blue) renders
 * gray/black instead of matching them. Forwards all props — including the
 * `style`/`width`/`height` SafeIcon computes for sizing — so callers keep
 * using it exactly like the bare icon component.
 */
const withBrandBlue = (Icon) => {
  const Wrapped = ({ style, ...rest }) => <Icon {...rest} style={{ ...style, color: 'var(--ds-blue-500)' }} />;
  Wrapped.displayName = `withBrandBlue(${Icon.displayName || Icon.name || 'Icon'})`;
  return Wrapped;
};

export default withBrandBlue;
