const React = require('react');

// SVG files are imported as React components (via SVGR) in Next.js.
// Jest doesn't apply the SVGR webpack transform, so we return a lightweight
// stub that satisfies both patterns:
//   import Icon from './icon.svg'   → used as <Icon />
//   import img from './photo.png'   → used as src={img}
const FileMock = (props) => React.createElement('svg', props);
FileMock.src = 'test-file-stub';

module.exports = FileMock;
