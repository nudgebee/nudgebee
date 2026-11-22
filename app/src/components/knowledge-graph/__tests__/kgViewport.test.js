import { getAutoFitCenter } from '../kgViewport';

const node = (id, x, y, extra = {}) => ({ id, position: { x, y }, width: 310, height: 100, ...extra });

// Canvas from the #41135 report (1536 x 730 browser, minus the filter sidebar).
const WIDTH = 1200;
const HEIGHT = 560;
const MIN_ZOOM = 0.4;

// Is the node with this centre inside the canvas once setCenter(x, y, { zoom }) runs?
const inView = (n, center, zoom) => {
  const left = (n.position.x - center.x) * zoom + WIDTH / 2;
  const top = (n.position.y - center.y) * zoom + HEIGHT / 2;
  return left < WIDTH && left + n.width * zoom > 0 && top < HEIGHT && top + n.height * zoom > 0;
};

describe('getAutoFitCenter', () => {
  it('leaves fitView in charge when the whole graph fits at the minimum zoom', () => {
    const nodes = [node('a', 0, 0), node('b', 400, 0), node('c', 0, 200)];
    expect(getAutoFitCenter(nodes, WIDTH, HEIGHT, MIN_ZOOM)).toBeNull();
  });

  it('centres on a real node when the bounding-box centre is empty space', () => {
    // Two far-apart clusters: centring their bounding box at zoom 0.4 shows neither.
    const nodes = [node('a', 0, 0), node('b', 400, 0), node('c', 20000, 12000), node('d', 20400, 12000)];
    const boundsCenter = { x: (0 + 20710) / 2, y: (0 + 12100) / 2 };
    expect(nodes.filter((n) => inView(n, boundsCenter, MIN_ZOOM))).toHaveLength(0);

    const center = getAutoFitCenter(nodes, WIDTH, HEIGHT, MIN_ZOOM);
    expect(center).not.toBeNull();
    expect(nodes.filter((n) => inView(n, center, MIN_ZOOM)).length).toBeGreaterThan(0);
  });

  it('picks the node nearest the bounding-box centre', () => {
    const nodes = [node('a', 0, 0), node('mid', 9000, 5500), node('c', 20000, 12000)];
    expect(getAutoFitCenter(nodes, WIDTH, HEIGHT, MIN_ZOOM)).toEqual({ x: 9155, y: 5550 });
  });

  it('ignores hidden nodes', () => {
    const nodes = [node('a', 0, 0), node('far', 50000, 50000, { hidden: true })];
    expect(getAutoFitCenter(nodes, WIDTH, HEIGHT, MIN_ZOOM)).toBeNull();
  });

  it('returns null before the canvas has a size or when there are no nodes', () => {
    expect(getAutoFitCenter([node('a', 0, 0), node('b', 50000, 0)], 0, 0, MIN_ZOOM)).toBeNull();
    expect(getAutoFitCenter([], WIDTH, HEIGHT, MIN_ZOOM)).toBeNull();
  });
});
