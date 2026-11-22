// Where the Knowledge Graph canvas should land after a new graph is laid out.
//
// The auto-fit is capped at a minimum zoom (so nodes stay legible), and when a graph
// is too big to fit at that zoom, React Flow's fitView() centres the bounding box of
// all nodes. With many disconnected components that centre can be empty space, so the
// canvas looks blank although hundreds of nodes were returned (issue #41135).
//
// Returns null when fitView() can show every node at `minZoom` or above. Otherwise it
// returns the centre of the node nearest the bounding-box centre, for setCenter(), so
// at least one card is always in view.
export const getAutoFitCenter = (nodes, width, height, minZoom, padding = 0.1) => {
  const visible = (nodes || []).filter((n) => !n.hidden && n.position);
  if (!visible.length || !width || !height) return null;

  const boxes = visible.map((n) => {
    const w = n.width || 0;
    const h = n.height || 0;
    return { x: n.position.x, y: n.position.y, w, h, cx: n.position.x + w / 2, cy: n.position.y + h / 2 };
  });
  // Single pass: spreading a large node array into Math.min/max can throw RangeError.
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  boxes.forEach((b) => {
    minX = Math.min(minX, b.x);
    minY = Math.min(minY, b.y);
    maxX = Math.max(maxX, b.x + b.w);
    maxY = Math.max(maxY, b.y + b.h);
  });

  // Same zoom React Flow's getViewportForBounds() computes before clamping.
  const fitZoom = Math.min(width / ((maxX - minX) * (1 + padding)), height / ((maxY - minY) * (1 + padding)));
  if (fitZoom >= minZoom) return null;

  const centerX = (minX + maxX) / 2;
  const centerY = (minY + maxY) / 2;
  let nearest = boxes[0];
  let nearestDist = Infinity;
  boxes.forEach((b) => {
    const dist = (b.cx - centerX) ** 2 + (b.cy - centerY) ** 2;
    if (dist < nearestDist) {
      nearest = b;
      nearestDist = dist;
    }
  });
  return { x: nearest.cx, y: nearest.cy };
};
