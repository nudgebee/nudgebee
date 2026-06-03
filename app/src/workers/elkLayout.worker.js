import ELK from 'elkjs/lib/elk.bundled.js';

const elk = new ELK();

self.onmessage = async (event) => {
  const { nodes, edges, options } = event.data;

  const isHorizontal = options['elk.direction'] === 'RIGHT';

  const graph = {
    id: 'root',
    layoutOptions: options,
    children: nodes.map((node) => ({
      ...node,
      targetPosition: isHorizontal ? 'left' : 'top',
      sourcePosition: isHorizontal ? 'right' : 'bottom',
      width: 310,
      height: 100,
    })),
    edges,
  };

  try {
    const layoutedGraph = await elk.layout(graph);
    self.postMessage({
      success: true,
      nodes: layoutedGraph?.children?.map((node) => ({
        ...node,
        position: { x: node.x, y: node.y },
      })),
      edges: layoutedGraph?.edges,
    });
  } catch (err) {
    self.postMessage({
      success: false,
      error: err.message,
      nodes,
      edges,
    });
  }
};
