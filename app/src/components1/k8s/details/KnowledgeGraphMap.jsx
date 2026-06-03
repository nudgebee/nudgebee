import { useEffect, useState, useMemo, useCallback, memo } from 'react';
import ReactFlow, {
  ReactFlowProvider,
  Controls,
  Background,
  BackgroundVariant,
  MiniMap,
  useNodesState,
  useEdgesState,
  Handle,
  Position,
} from 'reactflow';
import 'reactflow/dist/style.css';
import ELK from 'elkjs/lib/elk.bundled.js';
import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';

const elk = new ELK();

const ELK_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.edgeRouting': 'ORTHOGONAL',
  'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
  'elk.layered.spacing.edgeNodeBetweenLayers': '30',
  'elk.spacing.nodeNode': '60',
  'elk.spacing.nodeNodeBetweenLayers': '80',
  'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
  'elk.layered.mergeEdges': 'true',
};

const NODE_TYPE_COLORS = {
  Service: 'var(--ds-blue-500)',
  Workload: 'var(--ds-blue-500)',
  Database: 'var(--ds-teal-500)',
  ExternalService: 'var(--ds-amber-400)',
  MessageQueue: 'var(--ds-purple-400)',
  Cache: 'var(--ds-purple-400)',
  Queue: 'var(--ds-purple-400)',
  Topic: 'var(--ds-purple-400)',
  LoadBalancer: 'var(--ds-teal-400)',
  ServerlessFunction: 'var(--ds-pink-400)',
};

const NODE_TYPE_LABELS = {
  Service: 'SVC',
  Workload: 'WKL',
  Database: 'DB',
  ExternalService: 'EXT',
  MessageQueue: 'MQ',
  Cache: 'CACHE',
  Queue: 'QUEUE',
  Topic: 'TOPIC',
  LoadBalancer: 'LB',
  ServerlessFunction: 'FN',
};

const getLayoutedElements = async (nodes, edges) => {
  const graph = {
    id: 'root',
    layoutOptions: ELK_OPTIONS,
    children: nodes.map((node) => ({
      ...node,
      targetPosition: 'left',
      sourcePosition: 'right',
      width: 220,
      height: 60,
    })),
    edges: edges,
  };

  try {
    const layoutedGraph = await elk.layout(graph);
    return {
      nodes: layoutedGraph.children.map((node) => ({
        ...node,
        position: { x: node.x, y: node.y },
      })),
      edges: layoutedGraph.edges,
    };
  } catch (err) {
    console.error('ELK Layout Failed:', err);
    return { nodes, edges };
  }
};

const KGNode = memo(({ data }) => {
  const color = data.color || '#6b7280';
  const isTarget = data.isTarget;

  return (
    <Box
      sx={{
        padding: 'var(--ds-space-2) var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-md)',
        background: 'var(--ds-background-100)',
        border: `2px solid ${isTarget ? color : 'var(--ds-brand-150)'}`,
        boxShadow: isTarget ? `0 0 0 2px ${color}33` : '0 1px 3px rgba(0,0,0,0.08)',
        minWidth: ds.space.mul(0, 80),
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--ds-space-2)',
      }}
    >
      <Handle type='target' position={Position.Left} style={{ background: color, width: 6, height: 6 }} />
      <Box
        sx={{
          width: ds.space.mul(1, 7),
          height: ds.space.mul(1, 7),
          borderRadius: 'var(--ds-radius-sm)',
          background: `${color}1a`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)', color: color, lineHeight: 1 }}>
          {data.typeLabel}
        </Typography>
      </Box>
      <Box sx={{ overflow: 'hidden' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontWeight: isTarget ? 600 : 500,
            color: ds.blue[500],
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            maxWidth: ds.space.mul(0, 70),
          }}
          title={data.name}
        >
          {data.name}
        </Typography>
        {data.namespace ? (
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: ds.gray[700],
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              maxWidth: ds.space.mul(0, 70),
            }}
          >
            {data.namespace}
          </Typography>
        ) : null}
      </Box>
      <Handle type='source' position={Position.Right} style={{ background: color, width: 6, height: 6 }} />
    </Box>
  );
});

KGNode.displayName = 'KGNode';

function transformKGData(kgNodes, kgEdges, targetService) {
  const nodeMap = new Map();
  kgNodes.forEach((n) => {
    nodeMap.set(n.id, n);
  });

  const rfNodes = kgNodes.map((node) => {
    const name = node.properties?.name || node.unique_key || node.id;
    const namespace = node.properties?.namespace || '';
    const nodeType = node.node_type || 'Service';
    const color = NODE_TYPE_COLORS[nodeType] || '#6b7280';
    const typeLabel = NODE_TYPE_LABELS[nodeType] || nodeType.substring(0, 3).toUpperCase();
    const isTarget = name === targetService;

    return {
      id: node.id,
      type: 'kg-node',
      position: { x: 0, y: 0 },
      data: { name, namespace, color, typeLabel, isTarget, nodeType },
    };
  });

  const rfEdges = kgEdges
    .filter((edge) => nodeMap.has(edge.source_node_id) && nodeMap.has(edge.dest_node_id))
    .map((edge, idx) => ({
      id: `kg-edge-${idx}`,
      source: edge.source_node_id,
      target: edge.dest_node_id,
      animated: edge.relationship_type === 'CALLS',
      style: { stroke: edge.relationship_type === 'CALLS' ? '#3b82f6' : '#94a3b8', strokeWidth: 1.5 },
      markerEnd: { type: 'arrow', color: edge.relationship_type === 'CALLS' ? '#3b82f6' : '#94a3b8' },
      label: edge.relationship_type !== 'CALLS' ? edge.relationship_type : '',
      labelStyle: { fontSize: 9, fill: 'var(--ds-gray-600)' },
    }));

  return { nodes: rfNodes, edges: rfEdges };
}

const KnowledgeGraphMapInner = ({ nodes: kgNodes, edges: kgEdges, targetService }) => {
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const [isLayoutLoading, setIsLayoutLoading] = useState(true);

  const nodeTypes = useMemo(() => ({ 'kg-node': KGNode }), []);

  const { nodes: rawNodes, edges: rawEdges } = useMemo(() => transformKGData(kgNodes, kgEdges, targetService), [kgNodes, kgEdges, targetService]);

  useEffect(() => {
    if (rawNodes.length === 0) {
      setIsLayoutLoading(false);
      return;
    }

    const runLayout = async () => {
      setIsLayoutLoading(true);
      const { nodes: lNodes, edges: lEdges } = await getLayoutedElements(rawNodes, rawEdges);
      setNodes(lNodes);
      setEdges(lEdges);
      setIsLayoutLoading(false);
    };

    runLayout();
  }, [rawNodes, rawEdges, setNodes, setEdges]);

  const [highlightedEdges, setHighlightedEdges] = useState([]);

  const onNodeMouseEnter = useCallback(
    (_, node) => {
      const connectedIds = edges.filter((e) => e.source === node.id || e.target === node.id).map((e) => e.id);
      setHighlightedEdges(connectedIds);
    },
    [edges]
  );

  const onNodeMouseLeave = useCallback(() => {
    setHighlightedEdges([]);
  }, []);

  const styledEdges = useMemo(() => {
    if (highlightedEdges.length === 0) {
      return edges;
    }
    return edges.map((e) => ({
      ...e,
      style: {
        ...e.style,
        opacity: highlightedEdges.includes(e.id) ? 1 : 0.15,
        strokeWidth: highlightedEdges.includes(e.id) ? 2.5 : 1.5,
      },
    }));
  }, [edges, highlightedEdges]);

  if (isLayoutLoading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '400px' }}>
        <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body)' }}>Computing layout...</Typography>
      </Box>
    );
  }

  if (nodes.length === 0) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '200px' }}>
        <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body)' }}>No service dependency data available</Typography>
      </Box>
    );
  }

  return (
    <Box sx={{ height: '450px', width: '100%', border: '1px solid var(--ds-brand-150)', borderRadius: 'var(--ds-radius-lg)', overflow: 'hidden' }}>
      <ReactFlow
        nodes={nodes}
        edges={styledEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeMouseEnter={onNodeMouseEnter}
        onNodeMouseLeave={onNodeMouseLeave}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.1}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <Controls showInteractive={false} />
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color='#e5e7eb' />
        <MiniMap
          nodeStrokeWidth={2}
          nodeColor={(node) => (node.data?.isTarget ? node.data.color : 'var(--ds-brand-150)')}
          style={{ height: 80, width: 120 }}
        />
      </ReactFlow>
    </Box>
  );
};

const KnowledgeGraphMap = (props) => (
  <ReactFlowProvider>
    <KnowledgeGraphMapInner {...props} />
  </ReactFlowProvider>
);

export default KnowledgeGraphMap;
