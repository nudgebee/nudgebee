import { useEffect, useState, useMemo, memo, useCallback, useRef } from 'react';
import ReactFlow, {
  ReactFlowProvider,
  Controls,
  Background,
  useNodesState,
  useEdgesState,
  MarkerType,
  useReactFlow,
  useStore,
  Handle,
  Position,
  MiniMap,
  BackgroundVariant,
} from 'reactflow';
import { Box, CircularProgress, Typography } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import AccountTreeOutlinedIcon from '@mui/icons-material/AccountTreeOutlined';
import WarningAmberRoundedIcon from '@mui/icons-material/WarningAmberRounded';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import CloseIcon from '@mui/icons-material/Close';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import ExpandLessIcon from '@mui/icons-material/ExpandLess';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import FilterCenterFocusIcon from '@mui/icons-material/FilterCenterFocus';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import 'reactflow/dist/style.css';
import { BoxLayout2 } from '@components1/common';
import { colors } from 'src/utils/colors';
import WidgetCard from './common/WidgetCard';
import Loader from './common/Loader';
import apiKubernetes1 from '@api1/kubernetes1';
import LangTypeIcon from './common/LangTypeIcon';
import { snackbar } from './common/snackbarService';
import apiHome from '@api1/home';
import { safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import NodeDetails from './k8s/details/NodeDetails';
import { Modal } from './common/modal';
import ServiceMapLegends from './k8s/details/ServiceMapLegends';
import CustomTooltip from './common/CustomTooltip';
import CustomButton from './common/NewCustomButton';
import CustomAutocomplete from './common/CustomAutocomplete';
import LogQueryBuilderAutocomplete from './k8s/common/LogQueryBuilderAutocomplete';
import EdgeDetails from './k8s/details/EdgeDetails';
import Datetime from './common/format/Datetime';
import KGSettings from './KGSettings';
import { isTenantAdmin } from '@lib/auth';
import PropTypes from 'prop-types';

const MAX_NODE_LIMIT = 1500;

// Hoisted constant objects — stable references prevent ReactFlow false-change detection
const EDGE_STYLE = { stroke: '#b1b1b7', strokeWidth: 1.5, cursor: 'pointer' };
const EDGE_MARKER_END = { type: MarkerType.ArrowClosed, color: '#b1b1b7' };
const HANDLE_STYLE_VISIBLE = { background: '#555' };
const HANDLE_STYLE_HIDDEN = { opacity: 0, width: 1, height: 1 };
const EMPTY_SELECTED_DETAILS = {};
const REACT_FLOW_PRO_OPTIONS = { hideAttribution: true };

const levelOptions = [
  { label: '1 - Direct neighbors', value: 1 },
  { label: '2 - 2 hops', value: 2 },
  { label: '3 - 3 hops', value: 3 },
];

// ELK layout options for small/medium graphs (layered produces cleaner results)
const getElkOptions = () => {
  return {
    'elk.algorithm': 'layered',
    'elk.direction': 'RIGHT',
    'elk.edgeRouting': 'ORTHOGONAL',
    'elk.layered.spacing.edgeNodeBetweenLayers': '50',
    'elk.spacing.nodeNode': '60',
    'elk.spacing.nodeNodeBetweenLayers': '100',
    'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
    'elk.layered.mergeEdges': 'false',
    'elk.separateConnectedComponents': 'true',
    'elk.spacing.componentComponent': '150',
  };
};

const NODE_WIDTH = 310;
const NODE_HEIGHT = 100;

// Force-directed layout using d3-force for large graphs (d3js.org/d3-force/link style).
// Connected nodes cluster together naturally; unconnected groups drift apart.
// Ticks in batches via setTimeout to keep the main thread responsive.
const forceDirectedLayout = async (nodes, edges) => {
  const d3 = await import('d3');

  const nodeSet = new Set(nodes.map((n) => n.id));
  const validLinks = edges.filter((e) => nodeSet.has(e.source) && nodeSet.has(e.target));

  // Small initial spread so the simulation converges quickly into tight clusters
  const spread = Math.sqrt(nodes.length) * 30;
  const simNodes = nodes.map((n) => ({ id: n.id, x: (Math.random() - 0.5) * spread, y: (Math.random() - 0.5) * spread }));
  const simLinks = validLinks.map((e) => ({ source: e.source, target: e.target }));

  // Scale down forces for very large graphs to reduce computation per tick
  const isLarge = nodes.length > 800;
  const simulation = d3
    .forceSimulation(simNodes)
    .force(
      'link',
      d3
        .forceLink(simLinks)
        .id((d) => d.id)
        .distance(120)
    )
    .force(
      'charge',
      d3
        .forceManyBody()
        .strength(isLarge ? -200 : -300)
        .theta(isLarge ? 1.0 : 0.9)
    )
    .force(
      'collide',
      d3
        .forceCollide()
        .radius(NODE_HEIGHT / 2 + 30)
        .iterations(isLarge ? 1 : 3)
    )
    .force('center', d3.forceCenter(0, 0))
    .stop();

  // Tick in batches, yielding to the browser between batches so the UI stays responsive
  const totalIterations = isLarge ? 80 : Math.min(200, Math.ceil(Math.log(simNodes.length) * 35));
  const BATCH_SIZE = 25;
  await new Promise((resolve) => {
    let i = 0;
    const tickBatch = () => {
      const end = Math.min(i + BATCH_SIZE, totalIterations);
      while (i < end) {
        simulation.tick();
        i++;
      }
      if (i < totalIterations) {
        setTimeout(tickBatch, 0);
      } else {
        resolve();
      }
    };
    tickBatch();
  });

  const posMap = new Map(simNodes.map((n) => [n.id, { x: n.x, y: n.y }]));

  // Release d3 internal state (quadtrees, force caches)
  simulation.stop();
  simulation.force('link', null);
  simulation.force('charge', null);
  simulation.force('collide', null);
  simulation.force('center', null);

  return {
    nodes: nodes.map((node) => ({
      ...node,
      targetPosition: 'left',
      sourcePosition: 'right',
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      position: posMap.get(node.id) || { x: 0, y: 0 },
    })),
    edges,
  };
};

// Compute graph layout: d3-force for large graphs, ELK layered for smaller ones.
// ELK runs on the main thread via its built-in FakeWorker (elk.bundled.js).
// A real Web Worker wrapper was removed because elkjs cannot create nested
// Workers inside a Turbopack/Next.js dev worker (elk-worker.min.js detects the
// Web Worker context and skips exporting its FakeWorker class).
const getLayoutedElements = (nodes, edges) => {
  if (nodes.length > 300) {
    return forceDirectedLayout(nodes, edges).catch((error) => {
      console.error('Force layout failed, falling back to ELK:', error);
      return fallbackLayout(nodes, edges, getElkOptions());
    });
  }

  return fallbackLayout(nodes, edges, getElkOptions());
};

// ELK layout on main thread (elk.bundled.js uses an in-process FakeWorker)
const fallbackLayout = async (nodes, edges, options) => {
  const ELK = (await import('elkjs/lib/elk.bundled.js')).default;
  const elk = new ELK();
  const isHorizontal = options['elk.direction'] === 'RIGHT';
  const graph = {
    id: 'root',
    layoutOptions: options,
    children: nodes.map((node) => ({
      ...node,
      targetPosition: isHorizontal ? 'left' : 'top',
      sourcePosition: isHorizontal ? 'right' : 'bottom',
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
    })),
    edges,
  };
  try {
    const layoutedGraph = await elk.layout(graph);
    return {
      nodes: layoutedGraph?.children?.map((node) => ({
        ...node,
        position: { x: node.x, y: node.y },
      })),
      edges: layoutedGraph?.edges,
    };
  } catch (err) {
    console.error('ELK Layout Error:', err);
    return { nodes, edges };
  }
};

const LOD_ZOOM_THRESHOLD = 0.35;

// --- 2. CUSTOM NODE COMPONENT (Adaptive LOD via useStore) ---
// Selector returns boolean — node only re-renders when crossing the zoom threshold, not on every zoom tick
const zoomSelector = (state) => state.transform[2] < LOD_ZOOM_THRESHOLD;

const AdaptiveServiceNode = memo(
  ({ data, isConnectable }) => {
    const isZoomedOut = useStore(zoomSelector);
    const borderColor = data.type === 'Workload' ? '#2196f3' : '#4caf50';

    return (
      <div className={`service-node-wrapper ${isZoomedOut ? 'lod-dot' : 'lod-full'}`}>
        {/* Dot view (zoomed out) */}
        <div className='simple-dot-node' style={{ backgroundColor: borderColor }} title={data.name}>
          <Handle type='target' position={Position.Left} isConnectable={isConnectable} style={HANDLE_STYLE_HIDDEN} />
          <span className='simple-dot-label'>{data.name}</span>
          <Handle type='source' position={Position.Right} isConnectable={isConnectable} style={HANDLE_STYLE_HIDDEN} />
        </div>
        {/* Full view (zoomed in) */}
        <div className='service-node' style={{ borderColor }}>
          <Handle type='target' position={Position.Left} isConnectable={isConnectable} style={HANDLE_STYLE_VISIBLE} />
          <div style={{ width: 24, height: 24 }}>
            <LangTypeIcon appLang={data.subType} />
          </div>
          <div className='node-content'>
            <div className='node-title'>{data.name}</div>
            <span className='node-sub'>{data.subtitle}</span>
            <span className='node-sub'>{data.accountName}</span>
          </div>
          <button
            className='info-btn'
            onClick={(e) => {
              e.stopPropagation();
              if (data.onInfoClick) {
                data.onInfoClick(data.properties);
              }
            }}
          >
            <InfoOutlinedIcon style={{ fontSize: 18 }} />
          </button>
          <CustomButton
            className='focus-btn'
            startIcon={<FilterCenterFocusIcon style={{ fontSize: 14 }} />}
            variant='secondary'
            size='xSmall'
            showTooltip
            toolTipTitle='Focus on this node'
            tooltipPlacement='top'
            onClick={(e) => {
              e.stopPropagation();
              if (data.onFocusClick) {
                data.onFocusClick(data.id);
              }
            }}
          />
          <Handle type='source' position={Position.Right} isConnectable={isConnectable} style={HANDLE_STYLE_VISIBLE} />
        </div>
      </div>
    );
  },
  (prev, next) => prev.data.name === next.data.name && prev.data.subtitle === next.data.subtitle && prev.data.accountName === next.data.accountName
);
AdaptiveServiceNode.displayName = 'AdaptiveServiceNode';
AdaptiveServiceNode.propTypes = {
  data: PropTypes.shape({
    id: PropTypes.string,
    name: PropTypes.string,
    subtitle: PropTypes.string,
    accountName: PropTypes.string,
    type: PropTypes.string,
    subType: PropTypes.string,
    properties: PropTypes.object,
    onInfoClick: PropTypes.func,
    onFocusClick: PropTypes.func,
  }),
  isConnectable: PropTypes.bool,
};

// Stable reference — ReactFlow never unmounts/remounts nodes due to nodeTypes change
const STABLE_NODE_TYPES = { serviceNode: AdaptiveServiceNode };

// --- BREADCRUMB COMPONENT ---
const NodeBreadcrumbs = memo(({ fullPath, onNavigateToNode, onRemoveNode }) => {
  const [isExpanded, setIsExpanded] = useState(false);

  if (!fullPath?.length) return null;

  // Check if there are any edges to show
  const hasEdges = fullPath.some((node) => node.edgeFromPrev);

  return (
    <Box sx={{ borderBottom: '1px solid #eee', backgroundColor: '#fafafa' }}>
      {/* Main breadcrumb bar */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: '4px',
          padding: '8px 12px',
          flexWrap: 'wrap',
          minHeight: '36px',
        }}
      >
        <Typography sx={{ fontSize: '12px', color: '#666', marginRight: '4px' }}>Path:</Typography>
        {fullPath.map((node, index) => (
          <Box key={`${node.value}-${index}`} sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            {index > 0 && <ChevronRightIcon sx={{ fontSize: 16, color: '#999' }} />}
            <CustomTooltip
              title={<BreadcrumbTooltipContent node={node} />}
              placement='bottom'
              tooltipStyle={{
                backgroundColor: 'rgba(50, 50, 50, 0.95)',
                padding: '8px 12px',
                maxWidth: '450px',
              }}
            >
              <Box
                onClick={() => onNavigateToNode(node.value)}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '4px',
                  padding: '2px 8px',
                  borderRadius: '4px',
                  backgroundColor: node.isUserSelected ? (index === fullPath.length - 1 ? '#e3f2fd' : '#f5f5f5') : 'transparent',
                  border: node.isUserSelected ? 'none' : '1px dashed #ccc',
                  cursor: 'pointer',
                  opacity: node.isUserSelected ? 1 : 0.7,
                  '&:hover': { backgroundColor: '#e0e0e0', opacity: 1 },
                }}
              >
                <Typography
                  sx={{ fontSize: '12px', color: '#333', maxWidth: '200px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
                >
                  {node.displayLabel || node.label}
                </Typography>
                {node.isUserSelected && (
                  <CloseIcon
                    onClick={(e) => {
                      e.stopPropagation();
                      onRemoveNode(node.value);
                    }}
                    sx={{ fontSize: 14, color: '#666', cursor: 'pointer', '&:hover': { color: '#333' } }}
                  />
                )}
              </Box>
            </CustomTooltip>
          </Box>
        ))}
        {/* Expand/collapse button for edge details */}
        {hasEdges && fullPath.length > 1 && (
          <Box
            onClick={() => setIsExpanded(!isExpanded)}
            sx={{
              display: 'flex',
              alignItems: 'center',
              marginLeft: 'auto',
              padding: '2px 6px',
              borderRadius: '4px',
              cursor: 'pointer',
              color: '#666',
              '&:hover': { backgroundColor: '#e0e0e0' },
            }}
          >
            <InfoOutlinedIcon sx={{ fontSize: 16, marginRight: '2px' }} />
            <Typography sx={{ fontSize: '11px' }}>Details</Typography>
            {isExpanded ? <ExpandLessIcon sx={{ fontSize: 16 }} /> : <ExpandMoreIcon sx={{ fontSize: 16 }} />}
          </Box>
        )}
      </Box>

      {/* Expanded details section with edge types */}
      {isExpanded && hasEdges && (
        <Box
          sx={{
            padding: '8px 12px',
            backgroundColor: '#f5f5f5',
            borderTop: '1px solid #eee',
          }}
        >
          <Typography sx={{ fontSize: '11px', color: '#666', marginBottom: '6px' }}>Full path with relationships:</Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '4px' }}>
            {fullPath.map((node, index) => (
              <Box key={`detail-${node.value}-${index}`} sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                {index > 0 && node.edgeFromPrev && (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '2px',
                      padding: '2px 6px',
                      backgroundColor: '#fff',
                      border: '1px solid #ddd',
                      borderRadius: '3px',
                    }}
                  >
                    <Typography sx={{ fontSize: '10px', color: '#666' }}>—[</Typography>
                    <Typography sx={{ fontSize: '10px', color: '#1976d2', fontWeight: 500 }}>{node.edgeFromPrev}</Typography>
                    <Typography sx={{ fontSize: '10px', color: '#666' }}>]→</Typography>
                  </Box>
                )}
                {index > 0 && !node.edgeFromPrev && <ChevronRightIcon sx={{ fontSize: 14, color: '#999' }} />}
                <CustomTooltip title={<BreadcrumbTooltipContent node={node} />} placement='bottom'>
                  <Box
                    sx={{
                      padding: '2px 6px',
                      borderRadius: '3px',
                      backgroundColor: node.isUserSelected ? '#e3f2fd' : '#fff',
                      border: node.isUserSelected ? '1px solid #90caf9' : '1px dashed #ccc',
                      cursor: 'pointer',
                      '&:hover': { backgroundColor: '#e8e8e8' },
                    }}
                  >
                    <Typography sx={{ fontSize: '11px', color: '#333' }}>{node.displayLabel || node.label}</Typography>
                  </Box>
                </CustomTooltip>
              </Box>
            ))}
          </Box>
        </Box>
      )}
    </Box>
  );
});
NodeBreadcrumbs.displayName = 'NodeBreadcrumbs';

// --- BREADCRUMB TOOLTIP CONTENT ---
const BreadcrumbTooltipContent = memo(({ node }) => {
  if (!node) return null;

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: '4px',
        padding: '4px',
        minWidth: '200px',
        maxWidth: '400px',
      }}
    >
      {node.accountName && (
        <Box sx={{ display: 'flex', gap: '8px' }}>
          <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Account:</Typography>
          <Typography sx={{ fontSize: '11px', color: '#fff', wordBreak: 'break-word' }}>{node.accountName}</Typography>
        </Box>
      )}
      <Box sx={{ display: 'flex', gap: '8px' }}>
        <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Name:</Typography>
        <Typography sx={{ fontSize: '11px', color: '#fff', wordBreak: 'break-word' }}>{node.name || '-'}</Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: '8px' }}>
        <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Type:</Typography>
        <Typography sx={{ fontSize: '11px', color: '#fff' }}>{snakeToTitleCase(node.nodeType || '')}</Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: '8px', alignItems: 'flex-start' }}>
        <Typography sx={{ fontSize: '11px', color: '#aaa', minWidth: '70px' }}>Unique Key:</Typography>
        <Typography
          sx={{
            fontSize: '10px',
            color: '#ccc',
            wordBreak: 'break-all',
            fontFamily: 'monospace',
          }}
        >
          {node.uniqueKey || node.label}
        </Typography>
      </Box>
    </Box>
  );
});
BreadcrumbTooltipContent.displayName = 'BreadcrumbTooltipContent';
BreadcrumbTooltipContent.propTypes = {
  node: PropTypes.shape({
    accountName: PropTypes.string,
    name: PropTypes.string,
    nodeType: PropTypes.string,
    uniqueKey: PropTypes.string,
    label: PropTypes.string,
  }),
};

// --- BFS PATH FINDING UTILITY (parent-map approach for O(V) memory) ---
const findShortestPath = (adjacencyMap, startId, endId) => {
  if (startId === endId) return [startId];
  if (!adjacencyMap.has(startId) || !adjacencyMap.has(endId)) return null;

  const parent = new Map();
  parent.set(startId, null);
  const queue = [startId];
  let head = 0;

  while (head < queue.length) {
    const current = queue[head++];

    for (const neighbor of adjacencyMap.get(current) || []) {
      if (!parent.has(neighbor)) {
        parent.set(neighbor, current);
        if (neighbor === endId) {
          const path = [];
          let node = endId;
          while (node !== null) {
            path.unshift(node);
            node = parent.get(node);
          }
          return path;
        }
        queue.push(neighbor);
      }
    }
  }
  return null;
};

// --- 3. SHARED ACCOUNT LOOKUP HOOK ---
const useAccountLookup = (accounts) => {
  return useMemo(() => {
    const map = new Map();
    if (accounts) {
      accounts.forEach((acc) => {
        const name = acc.label || acc.account_name;
        if (acc.value) map.set(acc.value, name);
        if (acc.id) map.set(acc.id, name);
      });
    }
    return map;
  }, [accounts]);
};

// --- 4. CUSTOM HOOK: DATA PROCESSING ---
const useGraphBuilder = (rawData, onInfoClick, accMap, onFocusClick) => {
  return useMemo(() => {
    if (!rawData?.nodes) {
      return { initialNodes: [], initialEdges: [] };
    }

    // 1. Create Nodes (slim response: n.kind, n.name, n.account_id, n.logo_id — no n.properties)
    const initialNodes = rawData.nodes.map((n) => {
      const accName = accMap.get(n.account_id) || n.account_id;
      return {
        id: n.id,
        type: 'serviceNode',
        data: {
          name: n.name,
          type: n.kind,
          subtitle: n.kind,
          borderColor: n.kind === 'Workload' ? '#2196f3' : '#4caf50',
          subType: n.logo_id,
          id: n.id,
          properties: { node_id: n.id },
          accountId: n.account_id,
          accountName: accName,
          onInfoClick: onInfoClick,
          onFocusClick: onFocusClick,
        },
        position: { x: 0, y: 0 },
      };
    });

    const validNodeIds = new Set(initialNodes.map((n) => n.id));

    // 2. Create Edges
    const nodeCount = initialNodes.length;
    const isLargeGraph = nodeCount > 500;

    const initialEdges = (rawData.edges || [])
      .filter((e) => validNodeIds.has(e.source_node_id) && validNodeIds.has(e.dest_node_id))
      .map((e) => ({
        id: e.id,
        source: e.source_node_id,
        target: e.dest_node_id,
        type: isLargeGraph ? 'default' : 'smoothstep',
        animated: false,
        style: EDGE_STYLE,
        markerEnd: EDGE_MARKER_END,
        data: {
          ...e,
          relationshipLabel: e.relationship_type ? snakeToTitleCase(e.relationship_type.toLowerCase()) : '',
        },
      }));
    return { initialNodes, initialEdges };
  }, [rawData, onInfoClick, accMap]);
};

const ServiceMapContent = () => {
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const { fitView, getNodes, getEdges } = useReactFlow();

  const [isFilterLoading, setIsFilterLoading] = useState(true);
  const [isGraphLoading, setIsGraphLoading] = useState(true);
  const [rawData, setRawData] = useState({ nodes: [], edges: [] });
  const [selectedAccountIds, setSelectedAccountIds] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [selectedNodes, setSelectedNodes] = useState([]);
  const [selectedNodeDetails, setSelectedNodeDetails] = useState({});
  const [selectedEdgeDetails, setSelectedEdgeDetails] = useState(null);
  const [isNodeDetailsLoading, setIsNodeDetailsLoading] = useState(false);
  const [isEdgeDetailsLoading, setIsEdgeDetailsLoading] = useState(false);
  const [selectedNodeTypes, setSelectedNodeTypes] = useState([]);
  const [selectedLevel, setSelectedLevel] = useState(1);
  const [queryItems, setQueryItems] = useState([]);
  const [queryItemsLabel, setQueryItemsLabel] = useState([]);
  const [labelFilters, setLabelFilters] = useState('');
  const [attributeFilters, setAttributeFilters] = useState('');
  const [kgFilterOptions, setKgFilterOptions] = useState({});
  const [kgFiltersReady, setKgFiltersReady] = useState(false);
  const [isFilterOptionsRefreshing, setIsFilterOptionsRefreshing] = useState(false);
  const initialKgFilterOptionsRef = useRef(null);
  const kgFiltersInitialized = useRef(false);
  const filterOptionsDebounceRef = useRef(null);
  const prevAccountIdsRef = useRef([]);
  const edgeTooltipRef = useRef(null);

  // Draft filter state — holds pending sidebar changes before Apply is clicked
  const [draftAccountIds, setDraftAccountIds] = useState([]);
  const [draftNodes, setDraftNodes] = useState([]);
  const [draftNodeTypes, setDraftNodeTypes] = useState([]);
  const [draftLevel, setDraftLevel] = useState(1);
  const [draftLabelFilters, setDraftLabelFilters] = useState('');
  const [draftAttributeFilters, setDraftAttributeFilters] = useState('');
  const [filterHistory, setFilterHistory] = useState([]);
  const [filterFuture, setFilterFuture] = useState([]);

  // Pin / search / focus mode state
  const [pinnedNodeId, setPinnedNodeId] = useState(null);
  const [searchResetKey, setSearchResetKey] = useState(0);
  const [focusedNodeId, setFocusedNodeId] = useState(null);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const graphWrapperRef = useRef(null);
  const edgesByNodeRef = useRef(new Map());
  const pinnedElementsRef = useRef({ nodes: [], edges: [] });
  const nodeDataMapRef = useRef(new Map());
  const nodeSearchDebounceRef = useRef(null);
  const adjacencyMapRef = useRef(new Map());
  const layoutIdRef = useRef(0);
  const originalPositionsRef = useRef(null);
  const focusLayoutIdRef = useRef(0);
  const nodeDetailRequestRef = useRef(0);
  const edgeDetailRequestRef = useRef(0);
  const isMountedRef = useRef(true);
  const graphAbortRef = useRef(null);
  const skipNextGraphEffectRef = useRef(false);

  // Track mount/unmount (must set true on mount for React strict mode remount)
  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  // Refs for path computation in handleInfoClick (these get updated after useMemos are computed)
  const pathComputationRef = useRef({
    adjacencyMap: new Map(),
    edgeTypeLookup: new Map(),
    uniqueServiceKeysMap: new Map(),
    selectedNodes: [],
  });

  const hasUserFilters =
    selectedAccountIds?.length > 0 || selectedNodes?.length > 0 || selectedNodeTypes?.length > 0 || !!labelFilters || !!attributeFilters;

  const kgNodeCount = kgFilterOptions?.nodeCount || 0;
  const isLimitExceeded = hasUserFilters ? (rawData?.nodes?.length || 0) > MAX_NODE_LIMIT : kgNodeCount > MAX_NODE_LIMIT;

  const handleInfoClick = useCallback(async (properties) => {
    const { adjacencyMap, edgeTypeLookup, uniqueServiceKeysMap, selectedNodes } = pathComputationRef.current;
    const nodeId = properties?.node_id;
    let pathToNode = [];

    // Compute path synchronously using local graph data
    if (selectedNodes.length > 0 && nodeId && adjacencyMap.size > 0) {
      const startNode = selectedNodes[0];
      const pathIds = findShortestPath(adjacencyMap, startNode.value, nodeId);

      if (pathIds) {
        pathToNode = pathIds.map((id, index) => {
          const nodeData = uniqueServiceKeysMap.get(id);
          const edgeFromPrev = index > 0 ? edgeTypeLookup.get(`${pathIds[index - 1]}->${id}`) : null;
          return {
            id,
            label: nodeData?.displayLabel || nodeData?.label || id,
            uniqueKey: nodeData?.uniqueKey || nodeData?.label || id,
            name: nodeData?.name || '',
            nodeType: nodeData?.nodeType || '',
            accountName: nodeData?.accountName || '',
            edgeFromPrev,
          };
        });
      }
    }

    // Open modal immediately, then fetch full node details
    const requestId = ++nodeDetailRequestRef.current;
    setIsNodeDetailsLoading(true);
    setSelectedNodeDetails({ node_id: nodeId, pathToNode });

    try {
      const response = await apiKubernetes1.knowledgeGraphGetNode(nodeId);
      if (requestId !== nodeDetailRequestRef.current || !isMountedRef.current) return;
      const fullNode = response?.data?.data?.kg_get_node?.data;
      if (fullNode) {
        setSelectedNodeDetails({ ...fullNode, pathToNode });
      }
    } catch (err) {
      console.error('Failed to fetch node details:', err);
    } finally {
      if (requestId === nodeDetailRequestRef.current && isMountedRef.current) {
        setIsNodeDetailsLoading(false);
      }
    }
  }, []);

  const nodeLookup = useMemo(() => {
    const map = {};
    if (rawData?.nodes) {
      rawData.nodes.forEach((n) => {
        map[n.id] = n.name || n.unique_key || n.id;
      });
    }
    return map;
  }, [rawData]);

  const accountLookup = useAccountLookup(accounts);

  const handleEdgeClick = useCallback(
    async (_event, edge) => {
      if (!edge.data) {
        return;
      }

      const rawEdge = edge.data;
      const sourceName = nodeLookup[rawEdge.source_node_id] || rawEdge.source_node_id;
      const destName = nodeLookup[rawEdge.dest_node_id] || rawEdge.dest_node_id;

      // Open modal immediately with basic info, then fetch full edge details
      const requestId = ++edgeDetailRequestRef.current;
      setIsEdgeDetailsLoading(true);
      setSelectedEdgeDetails({ ...rawEdge, source_name: sourceName, destination_name: destName });

      try {
        const response = await apiKubernetes1.knowledgeGraphGetEdge(rawEdge.id);
        if (requestId !== edgeDetailRequestRef.current || !isMountedRef.current) return;
        const fullEdge = response?.data?.data?.kg_get_edge?.data;
        if (fullEdge) {
          setSelectedEdgeDetails({ ...fullEdge, source_name: sourceName, destination_name: destName });
        }
      } catch (err) {
        console.error('Failed to fetch edge details:', err);
      } finally {
        if (requestId === edgeDetailRequestRef.current && isMountedRef.current) {
          setIsEdgeDetailsLoading(false);
        }
      }
    },
    [nodeLookup]
  );

  const handleEdgeMouseEnter = useCallback((_event, edge) => {
    const tooltip = edgeTooltipRef.current;
    if (!tooltip) return;
    const label = edge.data?.relationshipLabel;
    if (label) {
      tooltip.textContent = label;
      tooltip.style.left = `${_event.clientX + 12}px`;
      tooltip.style.top = `${_event.clientY - 8}px`;
      tooltip.style.display = 'block';
    }
  }, []);

  const handleEdgeMouseLeave = useCallback(() => {
    const tooltip = edgeTooltipRef.current;
    if (tooltip) {
      tooltip.style.display = 'none';
    }
  }, []);

  useEffect(() => {
    apiHome
      .getCloudAccounts()
      .then(setAccounts)
      .catch((err) => {
        console.error('Failed to fetch cloud accounts:', err);
        snackbar.error('Failed to load cloud accounts.');
      });
  }, []);

  useEffect(() => {
    setIsFilterLoading(true);
    apiKubernetes1
      .knowledgeGraphFilterOptions()
      .then((res) => {
        const filterOptions = {};
        const nodeTypes =
          res?.data?.data?.kg_get_filter_options?.data?.node_types?.map((d) => ({
            label: snakeToTitleCase(d),
            value: d,
          })) || [];
        filterOptions.nodeTypes = nodeTypes;
        const attributes = res?.data?.data?.kg_get_filter_options?.data?.attribute_keys || [];
        filterOptions.attributeMap = attributes.map((l) => ({ label: l }));
        const labels = res?.data?.data?.kg_get_filter_options?.data?.label_keys || [];
        filterOptions.labelMap = labels.map((l) => ({ label: l }));
        const accountIds = res?.data?.data?.kg_get_filter_options?.data?.account_ids || [];
        filterOptions.accountIds = accountIds;
        const lastSyncTime = res?.data?.data?.kg_get_filter_options?.data?.last_sync_time || null;
        filterOptions.lastSyncTime = lastSyncTime;
        const nodeIdMap = res?.data?.data?.kg_get_filter_options?.data?.node_id_map || {};
        filterOptions.nodeIdMap = nodeIdMap;
        const nodeCount = res?.data?.data?.kg_get_filter_options?.data?.node_count ?? 0;
        filterOptions.nodeCount = nodeCount;
        initialKgFilterOptionsRef.current = filterOptions;
        setKgFilterOptions(filterOptions);
        setKgFiltersReady(true);
        kgFiltersInitialized.current = true;
        setIsFilterLoading(false);
      })
      .catch((err) => {
        console.error('Failed to fetch knowledge graph filter options:', err);
        snackbar.error('Failed to load filter options.');
        setIsFilterLoading(false);
      });
  }, []);

  // Cascading filter effect: re-fetch filter options live as draft account/node type selections change
  // (only refreshes dropdown options, does not rebuild the graph)
  useEffect(() => {
    if (!kgFiltersInitialized.current) return;

    const hasAccountFilter = draftAccountIds?.length > 0;
    const hasNodeTypeFilter = draftNodeTypes?.length > 0;

    // No active draft filters — restore initial options without a network call.
    if (!hasAccountFilter && !hasNodeTypeFilter) {
      setIsFilterOptionsRefreshing(false);
      if (initialKgFilterOptionsRef.current) {
        setKgFilterOptions((prev) => ({
          ...prev,
          nodeTypes: initialKgFilterOptionsRef.current.nodeTypes,
          labelMap: initialKgFilterOptionsRef.current.labelMap,
          attributeMap: initialKgFilterOptionsRef.current.attributeMap,
          nodeIdMap: initialKgFilterOptionsRef.current.nodeIdMap,
        }));
      }
      return;
    }

    let cancelled = false;

    // Detect whether the draft account selection changed since the last run.
    const currentAccountValues = (draftAccountIds || []).map((e) => e.value).sort((a, b) => String(a).localeCompare(String(b)));
    const prevAccountValues = prevAccountIdsRef.current;
    const accountChanged =
      currentAccountValues.length !== prevAccountValues.length || currentAccountValues.some((v, i) => v !== prevAccountValues[i]);
    prevAccountIdsRef.current = currentAccountValues;

    const suppressNodeTypesUpdate = draftNodeTypes?.length > 0 && !accountChanged;

    const onFilterOptionsSuccess = (res) => {
      if (cancelled) return;
      const d = res?.data?.data?.kg_get_filter_options?.data;
      const updates = {
        ...(!suppressNodeTypesUpdate && {
          nodeTypes: d?.node_types?.map((v) => ({ label: snakeToTitleCase(v), value: v })) || [],
        }),
        labelMap: (d?.label_keys || []).map((l) => ({ label: l })),
        attributeMap: (d?.attribute_keys || []).map((l) => ({ label: l })),
        nodeIdMap: d?.node_id_map || {},
      };
      setKgFilterOptions((prev) => ({ ...prev, ...updates }));
      setIsFilterOptionsRefreshing(false);
    };

    clearTimeout(filterOptionsDebounceRef.current);
    filterOptionsDebounceRef.current = setTimeout(() => {
      setIsFilterOptionsRefreshing(true);
      apiKubernetes1
        .knowledgeGraphFilterOptions({
          accountIds: draftAccountIds?.map((e) => e.value),
          nodeTypes: draftNodeTypes?.map((e) => e.value),
        })
        .then(onFilterOptionsSuccess)
        .catch((err) => {
          if (cancelled) return;
          console.error('Failed to refresh filter options:', err);
          setIsFilterOptionsRefreshing(false);
        });
    }, 300);

    return () => {
      cancelled = true;
      clearTimeout(filterOptionsDebounceRef.current);
    };
  }, [draftAccountIds, draftNodeTypes]);

  const fetchGraph = useCallback(
    ({ accountIds, nodeIds, nodeTypes, level, labelFiltersVal, attributeFiltersVal } = {}) => {
      const acIds = accountIds ?? selectedAccountIds;
      const nIds = nodeIds ?? selectedNodes;
      const nTypes = nodeTypes ?? selectedNodeTypes;
      const lvl = level ?? selectedLevel;
      const lblFilters = labelFiltersVal ?? labelFilters;
      const attrFilters = attributeFiltersVal ?? attributeFilters;

      const userFilters = acIds?.length > 0 || nIds?.length > 0 || nTypes?.length > 0 || !!lblFilters || !!attrFilters;

      if (!userFilters && (kgNodeCount || 0) > MAX_NODE_LIMIT) {
        setRawData({ nodes: [], edges: [] });
        setIsGraphLoading(false);
        return;
      }

      // Abort any in-flight graph request
      if (graphAbortRef.current) graphAbortRef.current.abort();
      const abortController = new AbortController();
      graphAbortRef.current = abortController;

      setIsGraphLoading(true);

      let labelFilter = {};
      if (lblFilters) {
        const parsedJson = safeJSONParse(lblFilters);
        if (parsedJson) labelFilter = parsedJson;
      }
      let attributeFilter = {};
      if (attrFilters) {
        const parsedJson = safeJSONParse(attrFilters);
        if (parsedJson) attributeFilter = parsedJson;
      }

      apiKubernetes1
        .knowledgeGraph(
          {
            accountIds: acIds?.map((e) => e.value),
            nodeIds: nIds?.map((e) => e.value),
            nodeTypes: nTypes?.map((e) => e.value),
            ...(nIds?.length > 0 ? { levels: lvl } : {}),
            ...(labelFilter?.length ? { labels: Object.fromEntries(labelFilter.map((x) => [x.key, x.value])) } : {}),
            ...(attributeFilter?.length ? { attributes: Object.fromEntries(attributeFilter.map((x) => [x.key, x.value])) } : {}),
          },
          abortController.signal
        )
        .then((res) => {
          const data = res?.data?.data?.kg_get_complete_graph?.data || { nodes: [], edges: [] };
          setRawData(data);
          if (!data.nodes?.length) {
            setIsGraphLoading(false);
          }
        })
        .catch((error) => {
          if (abortController.signal.aborted) return;
          console.error('Failed to fetch knowledge graph:', error);
          snackbar.error('Failed to load knowledge graph data.');
          setIsGraphLoading(false);
        });
    },
    [selectedAccountIds, selectedNodes, selectedNodeTypes, selectedLevel, labelFilters, attributeFilters, kgNodeCount]
  );

  // Snapshot the current applied state onto the history stack (max 20 entries)
  const pushToHistory = useCallback(() => {
    const snapshot = {
      selectedAccountIds,
      selectedNodes,
      selectedNodeTypes,
      selectedLevel,
      labelFilters,
      attributeFilters,
      queryItems,
      queryItemsLabel,
    };
    setFilterHistory((prev) => [...prev.slice(-19), snapshot]);
    setFilterFuture([]); // new action invalidates forward history
  }, [selectedAccountIds, selectedNodes, selectedNodeTypes, selectedLevel, labelFilters, attributeFilters, queryItems, queryItemsLabel]);

  // Pop last snapshot and restore all state + re-fetch the graph
  const handleBack = useCallback(() => {
    if (filterHistory.length === 0) return;
    const snapshot = filterHistory[filterHistory.length - 1];
    setFilterHistory((prev) => prev.slice(0, -1));

    // Save current state to future stack so Forward can return here
    setFilterFuture((prev) => [
      ...prev,
      { selectedAccountIds, selectedNodes, selectedNodeTypes, selectedLevel, labelFilters, attributeFilters, queryItems, queryItemsLabel },
    ]);

    // Restore applied state
    setSelectedAccountIds(snapshot.selectedAccountIds);
    setSelectedNodes(snapshot.selectedNodes);
    setSelectedNodeTypes(snapshot.selectedNodeTypes);
    setSelectedLevel(snapshot.selectedLevel);
    setLabelFilters(snapshot.labelFilters);
    setAttributeFilters(snapshot.attributeFilters);

    // Sync draft state so sidebar reflects the restored values
    setDraftAccountIds(snapshot.selectedAccountIds);
    setDraftNodes(snapshot.selectedNodes);
    setDraftNodeTypes(snapshot.selectedNodeTypes);
    setDraftLevel(snapshot.selectedLevel);
    setDraftLabelFilters(snapshot.labelFilters);
    setDraftAttributeFilters(snapshot.attributeFilters);

    // Restore filter builder chip UI state
    setQueryItems(snapshot.queryItems);
    setQueryItemsLabel(snapshot.queryItemsLabel);

    // Fire fetch immediately; skip the useEffect duplicate trigger
    skipNextGraphEffectRef.current = true;
    fetchGraph({
      accountIds: snapshot.selectedAccountIds,
      nodeIds: snapshot.selectedNodes,
      nodeTypes: snapshot.selectedNodeTypes,
      level: snapshot.selectedLevel,
      labelFiltersVal: snapshot.labelFilters,
      attributeFiltersVal: snapshot.attributeFilters,
    });
  }, [
    filterHistory,
    selectedAccountIds,
    selectedNodes,
    selectedNodeTypes,
    selectedLevel,
    labelFilters,
    attributeFilters,
    queryItems,
    queryItemsLabel,
    fetchGraph,
    setSelectedAccountIds,
    setSelectedNodes,
    setSelectedNodeTypes,
    setSelectedLevel,
    setLabelFilters,
    setAttributeFilters,
    setDraftAccountIds,
    setDraftNodes,
    setDraftNodeTypes,
    setDraftLevel,
    setDraftLabelFilters,
    setDraftAttributeFilters,
    setQueryItems,
    setQueryItemsLabel,
  ]);

  // Pop from future stack and restore — mirror of handleBack
  const handleForward = useCallback(() => {
    if (filterFuture.length === 0) return;
    const snapshot = filterFuture[filterFuture.length - 1];
    setFilterFuture((prev) => prev.slice(0, -1));

    // Save current state to history stack so Back can return here
    setFilterHistory((prev) => [
      ...prev.slice(-19),
      { selectedAccountIds, selectedNodes, selectedNodeTypes, selectedLevel, labelFilters, attributeFilters, queryItems, queryItemsLabel },
    ]);

    // Restore applied state
    setSelectedAccountIds(snapshot.selectedAccountIds);
    setSelectedNodes(snapshot.selectedNodes);
    setSelectedNodeTypes(snapshot.selectedNodeTypes);
    setSelectedLevel(snapshot.selectedLevel);
    setLabelFilters(snapshot.labelFilters);
    setAttributeFilters(snapshot.attributeFilters);

    // Sync draft state so sidebar reflects the restored values
    setDraftAccountIds(snapshot.selectedAccountIds);
    setDraftNodes(snapshot.selectedNodes);
    setDraftNodeTypes(snapshot.selectedNodeTypes);
    setDraftLevel(snapshot.selectedLevel);
    setDraftLabelFilters(snapshot.labelFilters);
    setDraftAttributeFilters(snapshot.attributeFilters);

    // Restore filter builder chip UI state
    setQueryItems(snapshot.queryItems);
    setQueryItemsLabel(snapshot.queryItemsLabel);

    // Fire fetch immediately; skip the useEffect duplicate trigger
    skipNextGraphEffectRef.current = true;
    fetchGraph({
      accountIds: snapshot.selectedAccountIds,
      nodeIds: snapshot.selectedNodes,
      nodeTypes: snapshot.selectedNodeTypes,
      level: snapshot.selectedLevel,
      labelFiltersVal: snapshot.labelFilters,
      attributeFiltersVal: snapshot.attributeFilters,
    });
  }, [
    filterFuture,
    selectedAccountIds,
    selectedNodes,
    selectedNodeTypes,
    selectedLevel,
    labelFilters,
    attributeFilters,
    queryItems,
    queryItemsLabel,
    fetchGraph,
    setSelectedAccountIds,
    setSelectedNodes,
    setSelectedNodeTypes,
    setSelectedLevel,
    setLabelFilters,
    setAttributeFilters,
    setDraftAccountIds,
    setDraftNodes,
    setDraftNodeTypes,
    setDraftLevel,
    setDraftLabelFilters,
    setDraftAttributeFilters,
    setQueryItems,
    setQueryItemsLabel,
  ]);

  useEffect(() => {
    if (!kgFiltersReady) return;
    // Skip if handleApply/handleClear already fired the fetch synchronously
    if (skipNextGraphEffectRef.current) {
      skipNextGraphEffectRef.current = false;
      return;
    }
    fetchGraph();
    return () => {
      // Don't abort when handleApply/handleClear already fired the fetch —
      // skipNextGraphEffectRef is true only in that window.
      if (!skipNextGraphEffectRef.current && graphAbortRef.current) graphAbortRef.current.abort();
    };
  }, [
    kgFiltersReady,
    kgNodeCount,
    hasUserFilters,
    selectedAccountIds,
    selectedNodes,
    selectedNodeTypes,
    labelFilters,
    attributeFilters,
    selectedLevel,
  ]);

  // Stable focus mode callback ref — allows passing a stable fn to useGraphBuilder
  // while enterFocusMode itself can be defined later (after adjacency map is available)
  const enterFocusModeRef = useRef(null);
  const stableEnterFocusMode = useCallback((nodeId) => {
    enterFocusModeRef.current?.(nodeId);
  }, []);

  const { initialNodes, initialEdges } = useGraphBuilder(rawData, handleInfoClick, accountLookup, stableEnterFocusMode);

  const adjacencyMap = useMemo(() => {
    const map = new Map();
    initialEdges.forEach((edge) => {
      if (!map.has(edge.source)) {
        map.set(edge.source, new Set());
      }
      if (!map.has(edge.target)) {
        map.set(edge.target, new Set());
      }
      map.get(edge.source).add(edge.target);
      map.get(edge.target).add(edge.source);
    });
    return map;
  }, [initialEdges]);

  // Edge type lookup: Maps "sourceId->targetId" to relationship label
  const edgeTypeLookup = useMemo(() => {
    const map = new Map();
    initialEdges.forEach((edge) => {
      const label = edge.data?.relationshipLabel || edge.label;
      map.set(`${edge.source}->${edge.target}`, label);
      map.set(`${edge.target}->${edge.source}`, label);
    });
    return map;
  }, [initialEdges]);

  // DOM-based hover: Directly toggle CSS classes on DOM elements instead of
  // calling setNodes/setEdges which triggers O(n) React re-renders for all nodes/edges.
  const hoveredElementsRef = useRef({ nodes: [], edges: [] });

  // Keep adjacencyMapRef in sync so stable pin/focus callbacks always read the latest map
  useEffect(() => {
    adjacencyMapRef.current = adjacencyMap;
  }, [adjacencyMap]);

  // clearPinHighlight: removes DOM pin classes only (no React state mutation)
  const clearPinHighlight = useCallback(() => {
    const wrapper = graphWrapperRef.current;
    if (wrapper) wrapper.classList.remove('kg-pin-active');
    const { nodes: pNodes, edges: pEdges } = pinnedElementsRef.current;
    pNodes.forEach((el) => el.classList.remove('kg-node-pin-highlighted'));
    pEdges.forEach((el) => el.classList.remove('kg-edge-pin-highlighted'));
    pinnedElementsRef.current = { nodes: [], edges: [] };
  }, []);

  // applyPinHighlight: highlights nodeId + its direct neighbors persistently via CSS classes
  const applyPinHighlight = useCallback(
    (nodeId) => {
      const wrapper = graphWrapperRef.current;
      if (!wrapper || !nodeId) return;

      clearPinHighlight();

      const neighbors = adjacencyMapRef.current.get(nodeId) || new Set();
      wrapper.classList.add('kg-pin-active');

      const touchedNodes = [];
      for (const id of [nodeId, ...neighbors]) {
        const el = wrapper.querySelector(`.react-flow__node[data-id="${id}"]`);
        if (el) {
          el.classList.add('kg-node-pin-highlighted');
          touchedNodes.push(el);
        }
      }

      // If the pinned node isn't known in the graph data, clear and bail.
      // (touchedNodes may be empty simply because the node is off-screen and
      // not rendered in the DOM yet, so we check the data map instead.)
      if (!nodeDataMapRef.current.has(nodeId)) {
        wrapper.classList.remove('kg-pin-active');
        setPinnedNodeId(null);
        return;
      }

      const touchedEdges = [];
      const connectedEdgeIds = edgesByNodeRef.current.get(nodeId);
      if (connectedEdgeIds) {
        for (const edgeId of connectedEdgeIds) {
          const el = wrapper.querySelector(`.react-flow__edge[data-testid="rf__edge-${edgeId}"]`);
          if (el) {
            el.classList.add('kg-edge-pin-highlighted');
            touchedEdges.push(el);
          }
        }
      }

      pinnedElementsRef.current = { nodes: touchedNodes, edges: touchedEdges };
    },
    [clearPinHighlight]
  );

  const handleNodeMouseEnter = useCallback(
    (_, node) => {
      const wrapper = graphWrapperRef.current;
      if (!wrapper) return;

      const hoveredId = node.id;
      const neighbors = adjacencyMap.get(hoveredId) || new Set();

      // Mark the wrapper as having a hovered state (enables global dimming via CSS)
      wrapper.classList.add('kg-hover-active');

      // Targeted node highlighting: O(neighbors) instead of O(allNodes)
      const touchedNodes = [];
      const highlightIds = [hoveredId, ...neighbors];
      for (const nodeId of highlightIds) {
        const el = wrapper.querySelector(`.react-flow__node[data-id="${nodeId}"]`);
        if (el) {
          el.classList.add('kg-node-highlighted');
          touchedNodes.push(el);
        }
      }

      // Targeted edge highlighting using precomputed index: O(connectedEdges) instead of O(allEdges^2)
      const touchedEdges = [];
      const connectedEdgeIds = edgesByNodeRef.current.get(hoveredId);
      if (connectedEdgeIds) {
        for (const edgeId of connectedEdgeIds) {
          const el = wrapper.querySelector(`.react-flow__edge[data-testid="rf__edge-${edgeId}"]`);
          if (el) {
            el.classList.add('kg-edge-highlighted');
            touchedEdges.push(el);
          }
        }
      }

      hoveredElementsRef.current = { nodes: touchedNodes, edges: touchedEdges };
    },
    [adjacencyMap]
  );

  const handleNodeMouseLeave = useCallback(() => {
    const wrapper = graphWrapperRef.current;
    if (!wrapper) return;

    wrapper.classList.remove('kg-hover-active');

    // Remove classes from previously highlighted elements
    const { nodes: prevNodes, edges: prevEdges } = hoveredElementsRef.current;
    prevNodes.forEach((el) => el.classList.remove('kg-node-highlighted'));
    prevEdges.forEach((el) => el.classList.remove('kg-edge-highlighted'));
    hoveredElementsRef.current = { nodes: [], edges: [] };

    // Restore pin highlight if one is active (hover temporarily overrides pin)
    if (pinnedNodeId) {
      applyPinHighlight(pinnedNodeId);
    }
  }, [pinnedNodeId, applyPinHighlight]); // eslint-disable-line react-hooks/exhaustive-deps

  // Re-apply pin after graph finishes loading (handles graph re-fetch case)
  useEffect(() => {
    if (!pinnedNodeId || isGraphLoading) return;
    const timer = setTimeout(() => applyPinHighlight(pinnedNodeId), 60);
    return () => clearTimeout(timer);
  }, [pinnedNodeId, isGraphLoading, applyPinHighlight]);

  // Keep nodeDataMapRef in sync for search DOM manipulation (nodeId -> node.data)
  useEffect(() => {
    const map = new Map();
    nodes.forEach((n) => map.set(n.id, n.data));
    nodeDataMapRef.current = map;
  }, [nodes]);

  // Autocomplete suggestions derived from current graph nodes.
  // Two-pass: detect cross-account name collisions first, then build disambiguated labels.
  // Dedup key is (name, accountId) so nodes with the same name in different accounts both appear.
  const searchSuggestions = useMemo(() => {
    // Pass 1: find names that appear in more than one account
    const nameAccounts = new Map();
    for (const node of nodes) {
      const name = node.data?.name || '';
      if (!name) continue;
      if (!nameAccounts.has(name)) nameAccounts.set(name, new Set());
      nameAccounts.get(name).add(node.data?.accountId || '');
    }

    // Pass 2: build suggestions deduped by (name, accountId)
    const seen = new Set();
    const result = [];
    for (const node of nodes) {
      const name = node.data?.name || '';
      if (!name) continue;
      const accountId = node.data?.accountId || '';
      const key = `${name}::${accountId}`;
      if (seen.has(key)) continue;
      seen.add(key);
      const isAmbiguous = (nameAccounts.get(name)?.size || 0) > 1;
      const label = isAmbiguous ? `${name} · ${node.data?.accountName || accountId}` : name;
      result.push({ label, type: node.data?.type || '', id: node.id });
    }
    return result.sort((a, b) => a.label.localeCompare(b.label));
  }, [nodes]);

  // handleNodeSearch: uses ReactFlow className prop so off-screen nodes are correctly
  // styled when they scroll into the viewport (DOM-only approach misses them with
  // onlyRenderVisibleElements={true}).
  const handleNodeSearch = useCallback(
    (query) => {
      if (!query.trim()) {
        setNodes((nds) => nds.map((n) => ({ ...n, className: undefined })));
        return;
      }

      const lq = query.toLowerCase();
      const matchingNodeIds = new Set();

      for (const [nodeId, nodeData] of nodeDataMapRef.current.entries()) {
        const name = (nodeData?.name || '').toLowerCase();
        const type = (nodeData?.type || '').toLowerCase();
        if (name.includes(lq) || type.includes(lq)) {
          matchingNodeIds.add(nodeId);
        }
      }

      // Apply via ReactFlow className prop — works for visible and off-screen nodes
      setNodes((nds) => nds.map((n) => ({ ...n, className: matchingNodeIds.has(n.id) ? 'kg-node-search-match' : undefined })));
    },
    [setNodes]
  );

  // Clear search spotlight when graph re-fetches
  useEffect(() => {
    setSearchResetKey((k) => k + 1);
    setFocusedNodeId(null);
    originalPositionsRef.current = null;
    // Node/edge classNames reset automatically when setNodes/setEdges replaces state with new rawData
  }, [rawData]); // eslint-disable-line react-hooks/exhaustive-deps

  // Focus mode: hide all nodes except the focused node and its direct neighbors, and
  // re-run layout on the subgraph so visible nodes cluster tightly instead of inheriting
  // their scattered positions from the full-graph layout.
  const enterFocusMode = useCallback(
    async (nodeId) => {
      const currentNodes = getNodes();
      const currentEdges = getEdges();

      if (!originalPositionsRef.current) {
        originalPositionsRef.current = new Map(currentNodes.map((n) => [n.id, n.position]));
      }

      const connectedIds = new Set([nodeId, ...(adjacencyMapRef.current.get(nodeId) || [])]);
      const subNodes = currentNodes.filter((n) => connectedIds.has(n.id)).map((n) => ({ ...n, position: { x: 0, y: 0 } }));
      const subEdges = currentEdges.filter((e) => connectedIds.has(e.source) && connectedIds.has(e.target));

      setFocusedNodeId(nodeId);
      clearPinHighlight();
      setPinnedNodeId(null);
      setSearchResetKey((k) => k + 1);

      const currentLayoutId = ++focusLayoutIdRef.current;
      const layouted = await getLayoutedElements(subNodes, subEdges);
      if (currentLayoutId !== focusLayoutIdRef.current) return;

      const newPosById = new Map((layouted.nodes || []).map((n) => [n.id, n.position]));

      setNodes((nds) =>
        nds.map((n) => {
          if (connectedIds.has(n.id)) {
            return {
              ...n,
              hidden: false,
              position: newPosById.get(n.id) || n.position,
              className: undefined,
            };
          }
          return { ...n, hidden: true, className: undefined };
        })
      );
      setEdges((eds) => eds.map((e) => ({ ...e, hidden: !connectedIds.has(e.source) || !connectedIds.has(e.target) })));

      setTimeout(() => fitView({ duration: 300, padding: 0.2 }), 100);
    },
    [getNodes, getEdges, setNodes, setEdges, fitView, clearPinHighlight]
  );

  const exitFocusMode = useCallback(() => {
    const snapshot = originalPositionsRef.current;
    setNodes((nds) =>
      nds.map((n) => ({
        ...n,
        hidden: false,
        position: snapshot?.get(n.id) || n.position,
      }))
    );
    setEdges((eds) => eds.map((e) => ({ ...e, hidden: false })));
    originalPositionsRef.current = null;
    focusLayoutIdRef.current++;
    setFocusedNodeId(null);
    setTimeout(() => fitView({ duration: 300 }), 50);
  }, [setNodes, setEdges, fitView]);

  // Keep the stable ref pointer up to date whenever enterFocusMode changes (adjacency map update)
  useEffect(() => {
    enterFocusModeRef.current = enterFocusMode;
  }, [enterFocusMode]);

  useEffect(() => {
    const currentLayoutId = ++layoutIdRef.current;

    if (isLimitExceeded) {
      setNodes([]);
      setEdges([]);
      setIsGraphLoading(false);
      return;
    }

    if (!initialNodes.length) {
      setNodes([]);
      setEdges([]);
      return;
    }
    const runLayout = async () => {
      try {
        const layouted = await getLayoutedElements(initialNodes, initialEdges);
        if (currentLayoutId !== layoutIdRef.current) return;
        setNodes(layouted.nodes);
        setEdges(layouted.edges);
        setTimeout(() => window.requestAnimationFrame(() => fitView()), 50);
      } finally {
        if (currentLayoutId === layoutIdRef.current) {
          setIsGraphLoading(false);
        }
      }
    };
    runLayout();
  }, [initialNodes, initialEdges, fitView, setNodes, setEdges, isLimitExceeded]);

  // Precompute edge index: nodeId -> Set of edge IDs connected to that node (for O(1) hover lookup)
  useEffect(() => {
    const map = new Map();
    edges.forEach((e) => {
      if (!map.has(e.source)) map.set(e.source, new Set());
      if (!map.has(e.target)) map.set(e.target, new Set());
      map.get(e.source).add(e.id);
      map.get(e.target).add(e.id);
    });
    edgesByNodeRef.current = map;
  }, [edges]);

  const uniqueServiceKeys = useMemo(() => {
    return (
      rawData?.nodes?.map((f) => {
        // slim response: f.kind, f.name, f.account_id, f.unique_key
        const accountName = accountLookup.get(f.account_id) || f.account_id || '';
        const displayName = f.name || f.unique_key;
        const simplifiedLabel = f.name ? `${f.name} (${snakeToTitleCase(f.kind)})` : f.unique_key;

        return {
          label: f.unique_key,
          displayLabel: simplifiedLabel,
          value: f.id,
          name: displayName,
          nodeType: f.kind,
          uniqueKey: f.unique_key,
          accountName: accountName,
          accountId: f.account_id,
        };
      }) || []
    );
  }, [rawData, accountLookup]);

  // O(1) lookup map for uniqueServiceKeys by node ID (replaces multiple .find() calls)
  const uniqueServiceKeysMap = useMemo(() => {
    const map = new Map();
    uniqueServiceKeys.forEach((item) => map.set(item.value, item));
    return map;
  }, [uniqueServiceKeys]);

  // Node dropdown options.
  // When nodes are selected and the graph has loaded, show only neighbors of the selected
  // nodes (cascading). Falls back to the full Stage-1 API list when no nodes are selected
  // or the graph hasn't loaded yet.
  const filterNodeOptions = useMemo(() => {
    if (draftNodes.length > 0 && adjacencyMap.size > 0) {
      const selectedIds = new Set(draftNodes.map((n) => n.value));
      const neighborIds = new Set();
      draftNodes.forEach((node) => {
        const neighbors = adjacencyMap.get(node.value);
        if (neighbors) {
          neighbors.forEach((id) => {
            if (!selectedIds.has(id)) neighborIds.add(id);
          });
        }
      });
      const neighborOptions = [];
      neighborIds.forEach((id) => {
        const node = uniqueServiceKeysMap.get(id);
        if (node) {
          neighborOptions.push({ label: node.label, displayLabel: node.displayLabel, value: node.value });
        }
      });
      return [...neighborOptions, ...draftNodes];
    }

    // Default: full list from Stage-1 filter data.
    // Always include currently drafted nodes even if not returned by the filtered API response.
    const map = kgFilterOptions?.nodeIdMap || {};
    const options = Object.entries(map).map(([uniqueKey, id]) => ({
      label: uniqueKey,
      displayLabel: uniqueKey,
      value: id,
    }));
    const optionValues = new Set(Object.values(map));
    const selectedNotInMap = (draftNodes || []).filter((sel) => !optionValues.has(sel.value));
    return [...options, ...selectedNotInMap];
  }, [kgFilterOptions?.nodeIdMap, draftNodes, adjacencyMap, uniqueServiceKeysMap]);

  // Node type options merged with any currently drafted types not returned by the API.
  const mergedNodeTypeOptions = useMemo(() => {
    const options = kgFilterOptions?.nodeTypes || [];
    const optionValues = new Set(options.map((o) => o.value));
    const selectedNotInOptions = (draftNodeTypes || []).filter((sel) => !optionValues.has(sel.value));
    return [...options, ...selectedNotInOptions];
  }, [kgFilterOptions?.nodeTypes, draftNodeTypes]);

  // Update refs for path computation in handleInfoClick
  useEffect(() => {
    pathComputationRef.current = {
      adjacencyMap,
      edgeTypeLookup,
      uniqueServiceKeysMap,
      selectedNodes,
    };
  }, [adjacencyMap, edgeTypeLookup, uniqueServiceKeysMap, selectedNodes]);

  // True when sidebar draft differs from the currently applied graph state.
  // Uses a Set for order-independent multi-select comparison (deselect+reselect same item
  // changes array index but not effective filter value).
  const hasChanges = useMemo(() => {
    const setsEqual = (a, b) => {
      if (a.length !== b.length) {
        return false;
      }
      const bVals = new Set(b.map((v) => v.value));
      return a.every((v) => bVals.has(v.value));
    };
    // Level only matters when nodes are selected (API ignores it otherwise)
    const levelChanged = draftNodes.length > 0 && draftLevel !== selectedLevel;
    return (
      !setsEqual(draftAccountIds, selectedAccountIds) ||
      !setsEqual(draftNodes, selectedNodes) ||
      !setsEqual(draftNodeTypes, selectedNodeTypes) ||
      levelChanged ||
      draftLabelFilters !== labelFilters ||
      draftAttributeFilters !== attributeFilters
    );
  }, [
    draftAccountIds,
    selectedAccountIds,
    draftNodes,
    selectedNodes,
    draftNodeTypes,
    selectedNodeTypes,
    draftLevel,
    selectedLevel,
    draftLabelFilters,
    labelFilters,
    draftAttributeFilters,
    attributeFilters,
  ]);

  const handleApply = useCallback(() => {
    pushToHistory();
    // Fire the API call immediately with draft values — don't wait for the
    // React render cycle (unmounting a large ReactFlow graph is slow and
    // delays the useEffect that would otherwise trigger the fetch).
    skipNextGraphEffectRef.current = true;
    fetchGraph({
      accountIds: draftAccountIds,
      nodeIds: draftNodes,
      nodeTypes: draftNodeTypes,
      level: draftLevel,
      labelFiltersVal: draftLabelFilters,
      attributeFiltersVal: draftAttributeFilters,
    });
    setSelectedAccountIds(draftAccountIds);
    setSelectedNodes(draftNodes);
    setSelectedNodeTypes(draftNodeTypes);
    setSelectedLevel(draftLevel);
    setLabelFilters(draftLabelFilters);
    setAttributeFilters(draftAttributeFilters);
  }, [draftAccountIds, draftNodes, draftNodeTypes, draftLevel, draftLabelFilters, draftAttributeFilters, fetchGraph, pushToHistory]);

  const handleClear = useCallback(() => {
    pushToHistory();
    const appliedStateWillChange =
      selectedAccountIds.length > 0 ||
      selectedNodes.length > 0 ||
      selectedNodeTypes.length > 0 ||
      selectedLevel !== 1 ||
      !!labelFilters ||
      !!attributeFilters;

    setDraftAccountIds([]);
    setDraftNodes([]);
    setDraftNodeTypes([]);
    setDraftLevel(1);
    setDraftLabelFilters('');
    setDraftAttributeFilters('');
    setQueryItems([]);
    setQueryItemsLabel([]);
    setSelectedAccountIds([]);
    setSelectedNodes([]);
    setSelectedNodeTypes([]);
    setSelectedLevel(1);
    setLabelFilters('');
    setAttributeFilters('');
    if (appliedStateWillChange) {
      skipNextGraphEffectRef.current = true;
      fetchGraph({ accountIds: [], nodeIds: [], nodeTypes: [], level: 1, labelFiltersVal: '', attributeFiltersVal: '' });
    }
  }, [selectedAccountIds, selectedNodes, selectedNodeTypes, selectedLevel, labelFilters, attributeFilters, fetchGraph, pushToHistory]);

  // Compute full breadcrumb path including intermediate nodes and edge types
  const fullBreadcrumbPath = useMemo(() => {
    if (selectedNodes.length === 0) {
      return [];
    }
    if (selectedNodes.length === 1) {
      return [{ ...selectedNodes[0], isUserSelected: true, edgeFromPrev: null }];
    }

    const fullPath = [];
    for (let i = 0; i < selectedNodes.length; i++) {
      if (i === 0) {
        fullPath.push({ ...selectedNodes[0], isUserSelected: true, edgeFromPrev: null });
      } else {
        const prevNode = selectedNodes[i - 1];
        const currNode = selectedNodes[i];
        const pathBetween = findShortestPath(adjacencyMap, prevNode.value, currNode.value);

        if (!pathBetween) {
          // Disconnected subgraphs — no path exists, mark as disconnected
          fullPath.push({ ...currNode, isUserSelected: true, edgeFromPrev: null, isDisconnected: true });
        } else if (pathBetween.length > 2) {
          // Add intermediate nodes (skip first as it's already in path)
          for (let j = 1; j < pathBetween.length - 1; j++) {
            const intermediateId = pathBetween[j];
            const prevId = pathBetween[j - 1];
            const nodeData = uniqueServiceKeysMap.get(intermediateId);
            if (nodeData) {
              const edgeType = edgeTypeLookup.get(`${prevId}->${intermediateId}`) || null;
              fullPath.push({ ...nodeData, isUserSelected: false, edgeFromPrev: edgeType });
            }
          }
          // Edge from last intermediate to current node
          const lastIntermediateId = pathBetween[pathBetween.length - 2];
          const edgeToFinal = edgeTypeLookup.get(`${lastIntermediateId}->${currNode.value}`) || null;
          fullPath.push({ ...currNode, isUserSelected: true, edgeFromPrev: edgeToFinal });
        } else {
          // Direct connection (no intermediate nodes)
          const edgeType = edgeTypeLookup.get(`${prevNode.value}->${currNode.value}`) || null;
          fullPath.push({ ...currNode, isUserSelected: true, edgeFromPrev: edgeType });
        }
      }
    }
    return fullPath;
  }, [selectedNodes, adjacencyMap, uniqueServiceKeysMap, edgeTypeLookup]);

  // Breadcrumb handlers - truncate to clicked node (for both user-selected and intermediate)
  const handleBreadcrumbNavigate = useCallback(
    (nodeId) => {
      const pathIndex = fullBreadcrumbPath.findIndex((n) => n.value === nodeId);

      if (pathIndex >= 0) {
        const newSelectedNodes = [];
        for (let i = 0; i <= pathIndex; i++) {
          const pathNode = fullBreadcrumbPath[i];
          if (pathNode.isUserSelected) {
            newSelectedNodes.push({ label: pathNode.label, value: pathNode.value });
          }
        }
        if (!fullBreadcrumbPath[pathIndex].isUserSelected) {
          const nodeData = uniqueServiceKeysMap.get(nodeId);
          if (nodeData) {
            newSelectedNodes.push(nodeData);
          }
        }
        // Sync both applied and draft state so sidebar stays consistent with graph navigation
        setSelectedNodes(newSelectedNodes);
        setDraftNodes(newSelectedNodes);
      }
    },
    [uniqueServiceKeysMap, fullBreadcrumbPath]
  );

  const handleBreadcrumbRemove = useCallback((nodeId) => {
    setSelectedNodes((prev) => prev.filter((n) => n.value !== nodeId));
    setDraftNodes((prev) => prev.filter((n) => n.value !== nodeId));
  }, []);

  // Handle node click with truncate-if-exists behavior
  // Graph node clicks are immediate navigation — sync both applied and draft state
  const handleNodeClick = useCallback(
    (nodeId) => {
      const nodeOption = uniqueServiceKeysMap.get(nodeId);
      if (!nodeOption) return;

      pushToHistory();

      // Clear other filters so all neighbors of selected nodes are visible
      setSelectedAccountIds([]);
      setDraftAccountIds([]);
      setSelectedNodeTypes([]);
      setDraftNodeTypes([]);
      setLabelFilters('');
      setDraftLabelFilters('');
      setAttributeFilters('');
      setDraftAttributeFilters('');
      setQueryItems([]);
      setQueryItemsLabel([]);

      const existingIndex = fullBreadcrumbPath.findIndex((n) => n.value === nodeId);

      if (existingIndex >= 0) {
        const newSelectedNodes = [];
        for (let i = 0; i <= existingIndex; i++) {
          const pathNode = fullBreadcrumbPath[i];
          if (pathNode.isUserSelected) {
            newSelectedNodes.push({ label: pathNode.label, value: pathNode.value });
          }
        }
        if (!fullBreadcrumbPath[existingIndex].isUserSelected) {
          newSelectedNodes.push(nodeOption);
        }
        setSelectedNodes(newSelectedNodes);
        setDraftNodes(newSelectedNodes);
      } else {
        setSelectedNodes((prev) => {
          if (prev.some((item) => item.value === nodeId)) return prev;
          return [...prev, nodeOption];
        });
        setDraftNodes((prev) => {
          if (prev.some((item) => item.value === nodeId)) return prev;
          return [...prev, nodeOption];
        });
      }
    },
    [
      uniqueServiceKeysMap,
      fullBreadcrumbPath,
      pushToHistory,
      setSelectedAccountIds,
      setDraftAccountIds,
      setSelectedNodeTypes,
      setDraftNodeTypes,
      setLabelFilters,
      setDraftLabelFilters,
      setAttributeFilters,
      setDraftAttributeFilters,
      setQueryItems,
      setQueryItemsLabel,
    ]
  );

  const handleNodeClickWrapper = useCallback(
    (_, n) => {
      const nodeId = n.id;
      if (pinnedNodeId === nodeId) {
        // Clicking the already-pinned node clears the pin
        clearPinHighlight();
        setPinnedNodeId(null);
      } else {
        // Pin the clicked node and run existing breadcrumb/filter logic
        setPinnedNodeId(nodeId);
        handleNodeClick(nodeId);
      }
    },
    [handleNodeClick, pinnedNodeId, clearPinHighlight, applyPinHighlight]
  );

  const handlePaneClick = useCallback(() => {
    clearPinHighlight();
    setPinnedNodeId(null);
  }, [clearPinHighlight]);

  const accountOptions = useMemo(
    () =>
      accounts
        ?.filter((ac) => kgFilterOptions?.accountIds?.includes(ac.id))
        ?.map((acc) => ({
          label: acc.label || acc.account_name,
          value: acc.id || acc.value,
        })) || accounts,
    [accounts, kgFilterOptions?.accountIds]
  );

  return (
    <>
      <Modal
        width='lg'
        open={Object.keys(selectedNodeDetails).length > 0}
        handleClose={() => setSelectedNodeDetails(EMPTY_SELECTED_DETAILS)}
        title={`Node Details: ${selectedNodeDetails?.properties?.name || selectedNodeDetails?.unique_key || selectedNodeDetails?.name || ''}`}
        contentStyles={{ padding: 0, height: '600px' }}
      >
        {isNodeDetailsLoading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}>
            <CircularProgress size={40} />
          </Box>
        ) : (
          <NodeDetails node={selectedNodeDetails} />
        )}
      </Modal>

      <Modal
        width='md'
        open={!!selectedEdgeDetails}
        handleClose={() => setSelectedEdgeDetails(null)}
        title='Edge Details'
        contentStyles={{ padding: '20px' }}
      >
        {isEdgeDetailsLoading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '150px' }}>
            <CircularProgress size={40} />
          </Box>
        ) : (
          <EdgeDetails edge={selectedEdgeDetails} />
        )}
      </Modal>

      <Box display='grid' gridTemplateColumns='280px 1fr' gap='8px' sx={{ height: 'calc(100vh - 148px)', overflow: 'hidden', padding: '8px 0px' }}>
        {/* Left Sidebar - Filters */}
        <WidgetCard
          sx={{
            mt: 0,
            p: '16px',
            display: 'flex',
            flexDirection: 'column',
            gap: '16px',
            overflowY: 'auto',
          }}
        >
          <Box
            sx={{
              padding: '0px 4px',
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            <Typography variant='subtitle1' sx={{ fontSize: '16px', fontFamily: 'Poppins', color: colors.text.secondary }}>
              Filters
            </Typography>

            {kgFilterOptions?.lastSyncTime && (
              <Datetime
                value={kgFilterOptions.lastSyncTime}
                prefix='Last synced: '
                sxPrefix={{ fontSize: '11px', color: '#9E9E9E', fontFamily: 'Poppins', mr: '4px' }}
                sxPrefixSecondary={false}
                sx={{ fontSize: '11px', fontWeight: 600, color: '#757575', fontFamily: 'Poppins' }}
                sxSuffix={{ fontSize: '11px', fontWeight: 600, color: '#757575', fontFamily: 'Poppins' }}
                sxSecondary={false}
                sxSuffixSecondary={false}
              />
            )}
          </Box>

          <BoxLayout2
            sharingOptions={{}}
            rowGap={'12px'}
            marginBottom={0}
            filterOptions={[
              {
                type: 'multi-dropdown',
                enabled: true,
                options: accountOptions,
                onSelect: (e) => setDraftAccountIds(e.target.value),
                label: 'Account',
                value: draftAccountIds,
                width: '100%',
              },
              {
                type: 'multi-dropdown',
                enabled: true,
                options: filterNodeOptions,
                onSelect: (e) => {
                  setDraftNodes(e.target.value);
                  if (e.target.value.length > 0) {
                    setDraftAccountIds([]);
                    setDraftNodeTypes([]);
                    setDraftLabelFilters('');
                    setDraftAttributeFilters('');
                    setQueryItems([]);
                    setQueryItemsLabel([]);
                  }
                },
                label: 'Node',
                value: draftNodes,
                isOptionsLoading: isFilterLoading || isFilterOptionsRefreshing,
                width: '100%',
              },
              {
                type: 'multi-dropdown',
                enabled: true,
                options: mergedNodeTypeOptions,
                onSelect: (e) => setDraftNodeTypes(e.target.value),
                label: 'Node Type',
                value: draftNodeTypes,
                isOptionsLoading: isFilterLoading || isFilterOptionsRefreshing,
                width: '100%',
              },
              {
                type: 'dropdown',
                enabled: true,
                options: levelOptions,
                onSelect: (e) => setDraftLevel(Number(e.target.value)),
                label: 'Level',
                value: draftLevel,
                width: '100%',
              },
            ]}
            sx={{ padding: '0 !important', margin: '0 !important', boxShadow: 'none !important', border: 'none !important' }}
          />

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
            <LogQueryBuilderAutocomplete
              logProvider='knowledge_graph'
              accountId={''}
              onQueryChange={(e) => setDraftLabelFilters(e?.query ?? '')}
              queryItems={queryItemsLabel}
              onQueryItemsChange={setQueryItemsLabel}
              getLabelsFromProps={kgFilterOptions.labelMap}
              allowMultipleQueries={false}
              height='auto'
              width='100%'
            />
            <LogQueryBuilderAutocomplete
              logProvider='knowledge_graph'
              accountId={''}
              onQueryChange={(e) => setDraftAttributeFilters(e?.query ?? '')}
              queryItems={queryItems}
              onQueryItemsChange={setQueryItems}
              getLabelsFromProps={kgFilterOptions.attributeMap}
              allowMultipleQueries={false}
              heading={'Attribute'}
              height='auto'
              width='100%'
            />
          </Box>

          <Box sx={{ display: 'flex', gap: '8px', pt: '4px' }}>
            <CustomButton
              data-testid='kg-apply-filters-btn'
              text='Apply Filters'
              variant='primary'
              size='Small'
              onClick={handleApply}
              disabled={!hasChanges}
              sx={{ flex: 1 }}
            />
            <CustomButton
              data-testid='kg-clear-filters-btn'
              text='Clear All'
              variant='secondary'
              size='Small'
              onClick={handleClear}
              sx={{ flex: 1 }}
            />
          </Box>
        </WidgetCard>

        {/* Right Side - Graph */}
        <Box
          sx={{
            bgcolor: 'white',
            borderRadius: '12px',
            boxShadow: '0px 4px 4px 0px #00000010',
            border: '1px solid #eee',
            position: 'relative',
            height: '97%',
            padding: '8px',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {(filterHistory.length > 0 || filterFuture.length > 0 || selectedNodes.length > 0) && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
              {filterHistory.length > 0 && (
                <CustomButton
                  data-testid='kg-back-btn'
                  text='Back'
                  variant='secondary'
                  size='Small'
                  startIcon={<ArrowBackIcon sx={{ width: '14px' }} />}
                  onClick={handleBack}
                />
              )}
              {filterFuture.length > 0 && (
                <CustomButton
                  data-testid='kg-forward-btn'
                  text='Forward'
                  variant='secondary'
                  size='Small'
                  endIcon={<ArrowForwardIcon sx={{ width: '14px' }} />}
                  onClick={handleForward}
                />
              )}
              {selectedNodes.length > 0 && (
                <NodeBreadcrumbs fullPath={fullBreadcrumbPath} onNavigateToNode={handleBreadcrumbNavigate} onRemoveNode={handleBreadcrumbRemove} />
              )}
            </Box>
          )}

          {/* Top-right toolbar: search + relationships */}
          <Box sx={{ position: 'absolute', top: '12px', right: '12px', zIndex: 10, display: 'flex', alignItems: 'center', gap: 1 }}>
            {/* Node search spotlight */}
            <CustomAutocomplete
              key={searchResetKey}
              id='kg-node-search'
              label='Search nodes…'
              options={searchSuggestions}
              freeSolo
              width={220}
              noOptionsText='No matching nodes'
              renderOption={(props, option) => {
                const { key, ...restProps } = props;
                const label = typeof option === 'string' ? option : option.label || '';
                const type = typeof option === 'string' ? '' : option.type || '';
                return (
                  <Box
                    component='li'
                    key={key}
                    {...restProps}
                    title={label}
                    sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 1 }}
                  >
                    <Typography
                      sx={{
                        fontSize: '13px',
                        color: colors.text.secondary,
                        flex: 1,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {label}
                    </Typography>
                    {type && (
                      <Typography
                        sx={{
                          fontSize: '11px',
                          color: colors.text.secondaryDark,
                          flexShrink: 0,
                          backgroundColor: colors.background.tertiaryLightest,
                          px: '6px',
                          py: '2px',
                          borderRadius: '4px',
                        }}
                      >
                        {type}
                      </Typography>
                    )}
                  </Box>
                );
              }}
              onSelect={(event) => {
                const val = event.target.value;
                // Cleared (× button)
                if (!val) {
                  handleNodeSearch('');
                  if (focusedNodeId) exitFocusMode();
                  return;
                }
                // Option selected from dropdown → enter focus mode
                if (typeof val === 'object' && val.id) {
                  clearTimeout(nodeSearchDebounceRef.current);
                  enterFocusMode(val.id);
                  return;
                }
                // Free text confirmed (Enter key) → highlight matching nodes
                const str = typeof val === 'string' ? val : val?.label || '';
                clearTimeout(nodeSearchDebounceRef.current);
                handleNodeSearch(str);
              }}
              onInputChange={(_, newValue) => {
                clearTimeout(nodeSearchDebounceRef.current);
                nodeSearchDebounceRef.current = setTimeout(() => handleNodeSearch(newValue), 200);
              }}
            />

            {/* Relationships legend */}
            <CustomTooltip
              title={<ServiceMapLegends mode='knowledge_graph' />}
              placement='bottom-end'
              tooltipStyle={{ maxWidth: '400px', padding: '16px' }}
            >
              <span>
                <CustomButton
                  id='relationship-types-btn'
                  text='Relationships'
                  variant='secondary'
                  size='Small'
                  startIcon={<AccountTreeOutlinedIcon sx={{ width: '16px' }} />}
                />
              </span>
            </CustomTooltip>

            {isTenantAdmin() && (
              <CustomButton
                id='kg-settings-btn'
                text='Settings'
                variant='secondary'
                size='Small'
                startIcon={<SettingsOutlinedIcon sx={{ width: '16px' }} />}
                onClick={() => setSettingsOpen(true)}
              />
            )}
          </Box>

          {settingsOpen && (
            <KGSettings
              open={settingsOpen}
              onClose={() => setSettingsOpen(false)}
              onSaved={() => {
                setSettingsOpen(false);
                fetchGraph();
              }}
            />
          )}

          <Box ref={graphWrapperRef} sx={{ flex: 1, position: 'relative' }}>
            {/* Focus mode banner */}
            {focusedNodeId && (
              <Box
                data-testid='kg-focus-banner'
                sx={{
                  position: 'absolute',
                  top: 10,
                  left: '50%',
                  transform: 'translateX(-50%)',
                  zIndex: 100,
                  bgcolor: '#1565c0',
                  color: '#fff',
                  borderRadius: '20px',
                  px: 1.5,
                  py: 0.4,
                  display: 'flex',
                  alignItems: 'center',
                  gap: 0.75,
                  boxShadow: '0 2px 8px rgba(0,0,0,0.2)',
                  cursor: 'default',
                  userSelect: 'none',
                }}
              >
                <FilterCenterFocusIcon sx={{ fontSize: 14 }} />
                <Typography variant='caption' sx={{ color: '#fff', fontWeight: 500, fontSize: 12 }}>
                  Focus: {nodeDataMapRef.current.get(focusedNodeId)?.name || focusedNodeId}
                </Typography>
                <CloseIcon
                  data-testid='kg-focus-exit'
                  sx={{ fontSize: 14, cursor: 'pointer', ml: 0.25, opacity: 0.85, '&:hover': { opacity: 1 } }}
                  onClick={exitFocusMode}
                />
              </Box>
            )}
            {isGraphLoading ? (
              <Loader style={{ width: '100%', height: '100%' }} />
            ) : isLimitExceeded ? (
              <Box display='flex' flexDirection='column' alignItems='center' justifyContent='center' height='100%' width='100%' p={4}>
                <Box
                  sx={{
                    maxWidth: 480,
                    width: '100%',
                    textAlign: 'center',
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    gap: '20px',
                  }}
                >
                  {/* Icon */}
                  <Box
                    sx={{
                      width: 64,
                      height: 64,
                      borderRadius: '16px',
                      background: 'linear-gradient(135deg, #FFF3E0 0%, #FFE0B2 100%)',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                    }}
                  >
                    <WarningAmberRoundedIcon sx={{ fontSize: 32, color: '#E65100' }} />
                  </Box>

                  {/* Title & Description */}
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                    <Typography
                      sx={{
                        fontSize: '18px',
                        fontWeight: 600,
                        fontFamily: 'Poppins',
                        color: colors.text.secondary,
                      }}
                    >
                      Graph Too Large to Render
                    </Typography>
                    <Typography
                      sx={{
                        fontSize: '13px',
                        fontWeight: 400,
                        color: colors.text.tertiary,
                        lineHeight: 1.6,
                      }}
                    >
                      The current selection contains{' '}
                      <Typography component='span' sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '13px' }}>
                        {(hasUserFilters ? rawData.nodes.length : kgNodeCount).toLocaleString()} nodes
                      </Typography>
                      , <br />
                      which exceeds the limit of {MAX_NODE_LIMIT.toLocaleString()}.
                    </Typography>
                    {/* Hint */}
                    <Typography
                      sx={{
                        fontSize: '13px',
                        fontWeight: 400,
                        color: colors.text.tertiary,
                        lineHeight: 1.5,
                      }}
                    >
                      <strong>Use the filters</strong> on the left to narrow down the graph.
                    </Typography>
                  </Box>
                </Box>
              </Box>
            ) : (
              <>
                <ReactFlow
                  nodes={nodes}
                  edges={edges}
                  onNodesChange={onNodesChange}
                  onEdgesChange={onEdgesChange}
                  onNodeClick={handleNodeClickWrapper}
                  onEdgeClick={handleEdgeClick}
                  onEdgeMouseEnter={handleEdgeMouseEnter}
                  onEdgeMouseLeave={handleEdgeMouseLeave}
                  onPaneClick={handlePaneClick}
                  onlyRenderVisibleElements={true}
                  nodeTypes={STABLE_NODE_TYPES}
                  onNodeMouseEnter={handleNodeMouseEnter}
                  onNodeMouseLeave={handleNodeMouseLeave}
                  fitView
                  minZoom={0.1}
                  maxZoom={2}
                  proOptions={REACT_FLOW_PRO_OPTIONS}
                  nodesDraggable={true}
                  nodesConnectable={false}
                  elementsSelectable={true}
                >
                  <Background
                    id='background-1'
                    gap={30}
                    color={colors.background?.reactFlow || 'white'}
                    variant={nodes.length > 500 ? BackgroundVariant.Dots : BackgroundVariant.Lines}
                    size={1}
                  />
                  <Controls position='top-left' />
                  {(rawData?.nodes?.length || 0) <= 1000 && <MiniMap nodeStrokeWidth={0} nodeBorderRadius={0} />}
                </ReactFlow>
                <div
                  ref={edgeTooltipRef}
                  className='kg-edge-tooltip'
                  style={{
                    position: 'fixed',
                    display: 'none',
                    pointerEvents: 'none',
                    background: 'rgba(50, 50, 50, 0.9)',
                    color: '#fff',
                    padding: '4px 8px',
                    borderRadius: '4px',
                    fontSize: '12px',
                    zIndex: 1000,
                    whiteSpace: 'nowrap',
                  }}
                />
              </>
            )}
          </Box>
        </Box>
      </Box>
    </>
  );
};

export default function KnowledgeGraphServiceMapWrapper() {
  return (
    <ReactFlowProvider>
      <ServiceMapContent />
    </ReactFlowProvider>
  );
}
