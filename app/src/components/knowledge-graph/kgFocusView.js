// Where to point the Knowledge Graph camera after entering focus mode.
//
// fitView() centres on the bounds of all visible nodes and clamps its zoom at
// FIT_VIEW_MIN_ZOOM. When the focused node has more neighbours than fit at that
// zoom (e.g. 29 in one ELK column), the bounds centre is somewhere in the middle of
// the column and the focused node itself ends up off screen (#41141).
//
// Returns the flow-space centre of the focused node when the visible subgraph cannot
// fit the canvas at `minZoom` (the caller should setCenter on it at `minZoom`), or
// null when it does fit (the caller should keep using fitView). Uses the same maths
// as React Flow's getViewportForBounds so the two agree on what "fits".
export const getFocusCenter = (nodes, focusNodeId, width, height, minZoom, padding) => {
  const sized = nodes.filter((n) => n.width && n.height);
  const focusNode = sized.find((n) => n.id === focusNodeId);
  if (!focusNode || !width || !height) return null;

  const pos = (n) => n?.positionAbsolute || n?.position || { x: 0, y: 0 };
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const n of sized) {
    const { x, y } = pos(n);
    minX = Math.min(minX, x);
    minY = Math.min(minY, y);
    maxX = Math.max(maxX, x + n.width);
    maxY = Math.max(maxY, y + n.height);
  }

  const zoom = Math.min(width / ((maxX - minX) * (1 + padding)), height / ((maxY - minY) * (1 + padding)));
  if (zoom >= minZoom) return null;

  const { x, y } = pos(focusNode);
  return { x: x + focusNode.width / 2, y: y + focusNode.height / 2 };
};
