import { getFocusCenter } from '../kgFocusView';

const MIN_ZOOM = 0.4;
const PADDING = 0.2;
const CARD = { width: 220, height: 60 };
// Canvas size of the #41141 repro (1536 x 730 browser, minus the filter panel / header).
const WIDTH = 1200;
const HEIGHT = 600;

const node = (id, x, y) => ({ id, position: { x, y }, positionAbsolute: { x, y }, ...CARD });

// ELK layered output for a focused node with `count` neighbours: the focused node on its
// own at the top of layer 0, its neighbours stacked in a single column in layer 1.
const focusLayout = (count) => [node('focus', 0, 0), ...Array.from({ length: count }, (_, i) => node(`n${i}`, 400, i * 160))];

// Viewport fitView() would pick: centre of the bounds, zoom clamped at MIN_ZOOM.
const boundsCentredViewport = (nodes) => {
  const minX = Math.min(...nodes.map((n) => n.position.x));
  const minY = Math.min(...nodes.map((n) => n.position.y));
  const maxX = Math.max(...nodes.map((n) => n.position.x + n.width));
  const maxY = Math.max(...nodes.map((n) => n.position.y + n.height));
  return { cx: (minX + maxX) / 2, cy: (minY + maxY) / 2, zoom: MIN_ZOOM };
};

const isInView = ({ cx, cy, zoom }, n) => {
  const halfW = WIDTH / 2 / zoom;
  const halfH = HEIGHT / 2 / zoom;
  return n.position.x >= cx - halfW && n.position.x + n.width <= cx + halfW && n.position.y >= cy - halfH && n.position.y + n.height <= cy + halfH;
};

describe('getFocusCenter', () => {
  it('centres on the focused node when its neighbours do not fit at the min zoom (#41141)', () => {
    const nodes = focusLayout(29);
    const focus = nodes[0];

    // The pre-fix behaviour: fitting the bounds leaves the focused node off screen.
    expect(isInView(boundsCentredViewport(nodes), focus)).toBe(false);

    const center = getFocusCenter(nodes, 'focus', WIDTH, HEIGHT, MIN_ZOOM, PADDING);
    expect(center).toEqual({ x: focus.position.x + CARD.width / 2, y: focus.position.y + CARD.height / 2 });
    expect(isInView({ cx: center.x, cy: center.y, zoom: MIN_ZOOM }, focus)).toBe(true);
  });

  it('returns null (keep fitView) when the subgraph fits, e.g. a node with 1 neighbour', () => {
    expect(getFocusCenter(focusLayout(1), 'focus', WIDTH, HEIGHT, MIN_ZOOM, PADDING)).toBeNull();
  });

  it('returns null when the focused node is not measured or the canvas has no size yet', () => {
    const nodes = focusLayout(29);
    const unmeasured = nodes.map((n) => (n.id === 'focus' ? { ...n, width: undefined, height: undefined } : n));
    expect(getFocusCenter(unmeasured, 'focus', WIDTH, HEIGHT, MIN_ZOOM, PADDING)).toBeNull();
    expect(getFocusCenter(nodes, 'focus', 0, 0, MIN_ZOOM, PADDING)).toBeNull();
    expect(getFocusCenter(nodes, 'missing', WIDTH, HEIGHT, MIN_ZOOM, PADDING)).toBeNull();
  });
});
