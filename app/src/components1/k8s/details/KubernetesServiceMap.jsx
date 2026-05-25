import { useEffect, useCallback, useState, useMemo } from 'react';
import PropTypes from 'prop-types';
import dayjs from 'dayjs';
import { useRouter } from 'next/router';
import ReactFlow, { ReactFlowProvider, Controls, Background, BackgroundVariant, MiniMap, useNodesState, useEdgesState } from 'reactflow';
import 'reactflow/dist/style.css';
import ELK from 'elkjs/lib/elk.bundled.js';
import { Box, Typography, Switch } from '@mui/material';
import EmptyData from '@components1/common/EmptyData';
import noDataImg from '@assets/Icon-no-data-available.svg';
import k8sApi from '@api1/kubernetes';
import { useData } from '@context/DataContext';
import { formatDate } from '@lib/formatter';
import { safeJSONParse } from 'src/utils/common';
import { colors } from 'src/utils/colors';
import Loader from '@components1/common/Loader';
import { Modal } from '@components1/common/modal';
import { BoxLayout2 } from '@components1/common';
import KubernetesTracesListing from './KubernetesTracesListing';
import CustomNode from './CustomNode';
import ServiceMapDownloadImage from './ServiceMapDownloadImage';
import CustomizedSlider from '@components1/k8s/common/Slider';
import ServiceMapLegends from './ServiceMapLegends';
import CustomEdge from './CustomEdge';
import NodeDetails from './NodeDetails';
import LogQueryBuilderAutocomplete from '@components1/k8s/common/LogQueryBuilderAutocomplete';
import LangTypeIcon from '@components1/common/LangTypeIcon';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import CustomTooltip from '@components1/common/CustomTooltip';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';

const elk = new ELK();

// Optimized options for a cleaner "Circuit Board" style layout
const ELK_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  // Routes edges with 90-degree turns instead of straight lines
  'elk.edgeRouting': 'ORTHOGONAL',
  // Grouping strategy
  'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
  // Spacing
  'elk.layered.spacing.edgeNodeBetweenLayers': '30',
  'elk.spacing.nodeNode': '60',
  'elk.spacing.nodeNodeBetweenLayers': '80',
  // Strategy: BRANDES_KOEPF usually creates more balanced graphs than SIMPLE
  'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
  // Merge parallel edges to reduce visual clutter
  'elk.layered.mergeEdges': 'true',
};

/**
 * Calculates layout positions using ELK.
 */
const getLayoutedElements = async (nodes, edges, options = {}) => {
  const isHorizontal = options?.['elk.direction'] === 'RIGHT';

  const graph = {
    id: 'root',
    layoutOptions: { ...ELK_OPTIONS, ...options },
    children: nodes.map((node) => ({
      ...node,
      // Target/Source handles adjust based on direction
      targetPosition: isHorizontal ? 'left' : 'top',
      sourcePosition: isHorizontal ? 'right' : 'bottom',
      // Explicit dimensions required for ELK
      width: 300,
      height: 100,
    })),
    edges: edges,
  };

  try {
    const layoutedGraph = await elk.layout(graph);
    return {
      nodes: layoutedGraph.children.map((node) => ({
        ...node,
        // React Flow expects position object, ELK returns x/y directly
        position: { x: node.x, y: node.y },
      })),
      edges: layoutedGraph.edges,
    };
  } catch (err) {
    console.error('ELK Layout Calculation Failed:', err);
    // Return original nodes/edges on failure to prevent crash
    return { nodes, edges };
  }
};

function extractLatencyValue(latency) {
  if (typeof latency !== 'string') {
    return latency || 0;
  }
  const match = /(\d+)(ms|s)/.exec(latency);
  if (!match) {
    return 0;
  }
  const val = parseInt(match[1], 10);
  return match[2] === 's' ? val * 1000 : val;
}

function extractLabelValues(applications, labelKeys) {
  const result = {};
  labelKeys.forEach((label) => {
    const values = [];
    applications.forEach((app) => {
      const appLabels = app.Labels;
      if (appLabels && appLabels[label] !== undefined) {
        values.push({ value: appLabels[label], attributes: {} });
      }
    });
    if (values.length > 0) {
      const uniqueData = Array.from(new Map(values.map((item) => [item.value, item])).values());
      uniqueData.sort((a, b) => a.value.localeCompare(b.value));
      result[label] = uniqueData;
    }
  });
  return result;
}

// Hook to debounce expensive operations (like slider changes)
function useDebounce(value, delay) {
  const [debouncedValue, setDebouncedValue] = useState(value);
  useEffect(() => {
    const handler = setTimeout(() => setDebouncedValue(value), delay);
    return () => clearTimeout(handler);
  }, [value, delay]);
  return debouncedValue;
}

const getType = (node) => {
  if (node?.Type?.length > 0) {
    return node?.Type;
  } else if (node?.Id?.kind) {
    return node?.Id?.kind;
  }
  return node?.Type || '';
};

/**
 * Transforms flat API data into Graph Nodes and Edges.
 * RESTORED: Now correctly builds the visual 'label' JSX for the nodes.
 */
const useGraphTransformation = (serviceMapData, selectedNamespace, duration, showOnlyErrorEdges, selectedWorkloads, appName, selectedSourceType) => {
  return useMemo(() => {
    // 1. Filtering Logic (Same as before)
    let filteredData = serviceMapData;

    if (selectedNamespace?.length > 0 && selectedWorkloads.length === 0) {
      filteredData = filteredData.filter((b) => selectedNamespace.includes(b?.Id?.namespace));
    } else if (selectedWorkloads?.length > 0) {
      filteredData = filteredData.filter((b) => (selectedSourceType !== 'otel' ? selectedNamespace.includes(b?.Id?.namespace) : true));
      if (!appName) {
        filteredData = filteredData.filter((b) => selectedWorkloads.includes(b?.Id?.name));
      }
    }

    if (duration > 0 || showOnlyErrorEdges) {
      const durationVal = parseInt(duration);
      filteredData = filteredData
        .map((item) => ({
          ...item,
          Upstreams: item?.Upstreams?.filter((u) => (durationVal > 0 ? extractLatencyValue(u?.Latency) >= durationVal : true)),
          Downstreams: item?.Downstreams.filter((d) => (durationVal > 0 ? extractLatencyValue(d?.Latency) >= durationVal : true)),
        }))
        .filter((g) => {
          if (showOnlyErrorEdges) {
            return g?.Downstreams?.some((d) => d?.FailureCount > 0) || g?.Upstreams?.some((u) => u?.FailureCount > 0);
          }
          return g?.Upstreams?.length > 0 || g?.Downstreams?.length > 0;
        });
    }

    // 2. Graph Construction (Map-based Optimization + JSX Restoration)
    const nodeMap = new Map();
    const links = [];
    let edgeIndex = 1;

    const createId = (name, ns) => `${name}|${ns}`;

    // --- RESTORED LOGIC START ---
    const addNode = (nodeData, isExternal = false) => {
      const id = createId(nodeData.Id.name, nodeData.Id.namespace);

      if (!nodeMap.has(id)) {
        // Recreate the JSX Label exactly as in the original code
        const labelJSX = (
          <div className='sm-node'>
            <div aria-label={nodeData.Id.name} style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
              <LangTypeIcon appLang={getType(nodeData)} />
              <span className='sm-node-label1'>{nodeData.Id.name}</span>
            </div>
            <div className='sm-node'>
              <span className='sm-node-label2'>{nodeData.Id.namespace}</span>
            </div>
          </div>
        );

        nodeMap.set(id, {
          id,
          type: 'node-with-toolbar', // Maps to CustomNode
          position: { x: 0, y: 0 },
          data: {
            label: labelJSX,
            changeColor: isExternal,
            entireNodeInstance: nodeData,
            selectedNamespace: selectedNamespace,
            // Keep extra props if needed for other logic
            isExternal,
          },
        });
      }
    };

    filteredData.forEach((f) => {
      addNode(f);
      const sourceId = createId(f.Id.name, f.Id.namespace);

      f?.Upstreams?.forEach((u) => {
        const parts = u.Id.split(':');
        if (parts.length >= 3) {
          const targetNs = parts[0];
          const targetName = parts[2];
          const targetId = createId(targetName, targetNs);

          if (!nodeMap.has(targetId)) {
            // Create phantom node for upstream dependency
            addNode(
              {
                Id: { name: targetName, namespace: targetNs, kind: parts[1] },
                Type: parts[1],
              },
              true
            );
          }

          links.push({
            id: `edge-${edgeIndex++}`,
            source: sourceId,
            target: targetId,
            type: 'custom-edge',
            animated: true,
            markerEnd: { type: 'arrow', color: colors.text.serviceMap },
            label: u.Protocol || '',
            data: { ...u },
          });
        }
      });

      f.Downstreams?.forEach((d) => {
        if (d.Id.name) {
          const targetId = createId(d.Id.name, d.Id.namespace);
          // Downstream flow: Target -> Source
          const edgeSource = targetId;
          const edgeTarget = sourceId;

          if (!nodeMap.has(edgeSource)) {
            addNode(
              {
                Id: { name: d.Id.name, namespace: d.Id.namespace, kind: d.Id.kind },
              },
              true
            );
          }

          links.push({
            id: `edge-${edgeIndex++}`,
            source: edgeSource,
            target: edgeTarget,
            type: 'custom-edge',
            animated: true,
            markerEnd: { type: 'arrow', color: colors.text.serviceMap },
            data: { ...d },
          });
        }
      });
    });

    return { nodes: Array.from(nodeMap.values()), edges: links };
  }, [serviceMapData, selectedNamespace, duration, showOnlyErrorEdges, selectedWorkloads, appName, selectedSourceType]);
};

const KubernetesServiceMap = ({ accountId, appName, namespaceName, dateRange, showSourceType = true, dataForServiceMap = [] }) => {
  const router = useRouter();

  // Parse the query parameter from URL (contains r_start_time, r_end_time, workload_filter)
  const urlQueryParam = useMemo(() => {
    if (router.query.query) {
      try {
        return safeJSONParse(decodeURIComponent(router.query.query));
      } catch (e) {
        console.error('Failed to parse query parameter:', e);
        return null;
      }
    }
    return null;
  }, [router.query.query]);

  const initialDates = useMemo(
    () => ({
      start: urlQueryParam?.r_start_time
        ? dayjs(urlQueryParam.r_start_time).valueOf()
        : dateRange?.startDateInMilli || dayjs().subtract(1, 'hour').valueOf(),
      end: urlQueryParam?.r_end_time ? dayjs(urlQueryParam.r_end_time).valueOf() : dateRange?.endDateInMilli || dayjs().valueOf(),
      shortcut: dateRange?.shortcutClickTime || 1 * 60 * 60 * 1000,
    }),
    [urlQueryParam, dateRange]
  );
  const [startDateInMilli, setStartDateInMilli] = useState(initialDates.start);
  const [endDateInMilli, setEndDateInMilli] = useState(initialDates.end);
  const [shortcutClickTime, setShortcutClickTime] = useState(initialDates.shortcut);
  const [serviceMapData, setServiceMapData] = useState([]);
  const [namespaces, setNamespaces] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(() => {
    if (urlQueryParam?.workload_filter?.workload_namespace) {
      return [urlQueryParam.workload_filter.workload_namespace];
    }
    if (router.query.namespace) {
      return [router.query.namespace];
    }
    return namespaceName ? [namespaceName] : [];
  });

  const [selectedWorkloads, setSelectedWorkloads] = useState(() => {
    if (urlQueryParam?.workload_filter?.workload_name) {
      return [urlQueryParam.workload_filter.workload_name];
    }
    if (router.query.app) {
      return [router.query.app];
    }
    return appName ? [appName] : [];
  });
  const [allWorkloads, setAllWorkloads] = useState([]);
  const [selectedNamespaceWorkloads, setSelectedNamespaceWorkloads] = useState([]);
  // Auto-set to 'ebpf' if URL has query param (eBPF API format)
  const [selectedSourceType, setSelectedSourceType] = useState(() => {
    if (urlQueryParam?.workload_filter) {
      return 'ebpf';
    }
    return 'otel';
  });
  const [minDateBySource, setMinDateBySource] = useState(new Date());
  const [isLoading, setIsLoading] = useState(false);
  const [isLayoutLoading, setIsLayoutLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');
  const [showOnlyErrorEdges, setShowOnlyErrorEdges] = useState(false);
  const [duration, setDuration] = useState(0);
  const [labelFilters, setLabelFilters] = useState('');
  const [queryItems, setQueryItems] = useState([]);
  const [labelMap, setLabelMap] = useState([]);
  const [valueMap, setValueMap] = useState([]);
  const [selectedNode, setSelectedNode] = useState({});
  const [selectedEdge, setSelectedEdge] = useState({});
  const [showTraces, setShowTraces] = useState(false);
  const [highlightedEdges, setHighlightedEdges] = useState([]);
  const { providerCapabilities } = useData();
  const tracesProvider = providerCapabilities.find((e) => e.provider_type === 'traces');
  const tracesCaps = tracesProvider?.capabilities;
  const tracesProviderName = tracesProvider?.provider;
  const supportsServiceMap = tracesCaps?.supports_service_map ?? null;

  const debouncedDuration = useDebounce(duration, 300);

  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);

  // Memoize Node Types (Performance)
  const nodeTypes = useMemo(() => ({ 'node-with-toolbar': CustomNode }), []);
  const edgeTypes = useMemo(() => ({ 'custom-edge': CustomEdge }), []);

  // Update state when URL query parameter changes (handles hydration)
  useEffect(() => {
    if (urlQueryParam) {
      // Auto-set to 'ebpf' since URL query format matches eBPF API
      if (urlQueryParam.workload_filter) {
        setSelectedSourceType('ebpf');
      }
      if (urlQueryParam.r_start_time) {
        setStartDateInMilli(dayjs(urlQueryParam.r_start_time).valueOf());
      }
      if (urlQueryParam.r_end_time) {
        setEndDateInMilli(dayjs(urlQueryParam.r_end_time).valueOf());
      }
      if (urlQueryParam.workload_filter?.workload_namespace) {
        setSelectedNamespace([urlQueryParam.workload_filter.workload_namespace]);
      }
      if (urlQueryParam.workload_filter?.workload_name) {
        setSelectedWorkloads([urlQueryParam.workload_filter.workload_name]);
      }
    }
  }, [urlQueryParam]);

  // 1. Reset filters when context changes
  useEffect(() => {
    if (selectedSourceType === 'otel') {
      setMinDateBySource(new Date(Date.now() - 24 * 60 * 60 * 1000));
    } else {
      // eBPF usually has longer retention
      setMinDateBySource(new Date(Date.now() - 7 * 24 * 60 * 60 * 1000));
    }
  }, [selectedSourceType]);

  // 1. Fetch Logic for OTel (Hasura Action)
  const getServiceMapFromHasuraAction = async () => {
    try {
      const reqBody = {
        accountId,
        start_time: formatDate(startDateInMilli),
        end_time: formatDate(endDateInMilli),
        ...(selectedWorkloads[0] && { workloadName: selectedWorkloads[0] }),
      };

      if (labelFilters?.length > 0) {
        const parsed = safeJSONParse(labelFilters);
        if (parsed) {
          reqBody['label_filter'] = parsed;
        }
      }

      const res = await k8sApi.tracesServiceMap(reqBody);
      const rawData = res?.data?.data?.traces_service_map?.data?.applications || [];
      const labels = res?.data?.data?.traces_service_map?.data?.labels || [];

      processServiceMapData(rawData, labels);
    } catch (err) {
      console.error(err);
      setErrorMessage('Failed to fetch OTel Service Map');
      setServiceMapData([]);
    } finally {
      setIsLoading(false);
    }
  };

  // 2. Fetch Logic for eBPF (Relay Server)
  const getServiceMapFromRelay = async () => {
    try {
      const payload = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'service_map',
          action_params: {
            r_start_time: formatDate(startDateInMilli),
            r_end_time: formatDate(endDateInMilli),
            workload_filter: {
              ...((selectedWorkloads[0] || appName) && { workload_name: selectedWorkloads[0] || appName }),
              ...((selectedNamespace[0] || namespaceName) && { workload_namespace: selectedNamespace[0] || namespaceName }),
            },
          },
        },
        cache: false,
      };

      const res = await k8sApi.relayForwardRequest(payload);
      const rawData = res?.data?.data || [];

      processServiceMapData(rawData, []); // eBPF usually infers labels from data
    } catch (err) {
      console.error(err);
      setErrorMessage('Failed to fetch eBPF Service Map');
      setServiceMapData([]);
    } finally {
      setIsLoading(false);
    }
  };

  // 3. Shared Data Processing Helper
  const processServiceMapData = (rawData, labels) => {
    if (rawData.length) {
      const cleaned = rawData
        .filter((d) => !['monitoring', 'control-plane'].includes(d.Category?.category))
        .map((e) => ({ ...e, Status: e.Status || 'ok' }));

      setServiceMapData(cleaned);

      // Extract Labels
      if (labels.length || cleaned.length) {
        const derivedLabels = labels.length ? labels : Object.keys(cleaned[0]?.Labels || {});
        const labelValues = extractLabelValues(cleaned, derivedLabels);
        setValueMap(labelValues);
        setLabelMap(derivedLabels.map((l) => ({ label: l })));
      }

      // Populate Filters if needed
      if (!appName && !namespaceName && selectedSourceType !== 'otel') {
        const distinctNs = [...new Set(cleaned.map((x) => x.Labels?.ns))].filter(Boolean).sort((a, b) => a.localeCompare(b));
        setNamespaces(distinctNs);
      }

      if (!appName) {
        setAllWorkloads(rawData.map((g) => g.Id));
      }

      if (selectedSourceType === 'otel' && !selectedNamespaceWorkloads.length) {
        const distinctApps = [...new Set(cleaned.map((x) => x.Id.name))].filter(Boolean).sort((a, b) => a.localeCompare(b));
        setSelectedNamespaceWorkloads(distinctApps);
      }
    } else {
      setEdges([]);
      setNodes([]);
    }
  };

  useEffect(() => {
    if (dataForServiceMap.length > 0) {
      setServiceMapData(dataForServiceMap);
      return;
    }
  }, [dataForServiceMap]);

  // 4. Main useEffect
  useEffect(() => {
    if (!accountId) {
      return;
    }
    // Skip API call if we already have data from evidence
    if (dataForServiceMap.length > 0) {
      return;
    }
    if (selectedSourceType === 'otel') {
      setIsLoading(true);
      setErrorMessage('');
      getServiceMapFromHasuraAction();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, startDateInMilli, endDateInMilli, selectedSourceType, selectedWorkloads, labelFilters, dataForServiceMap.length]);

  useEffect(() => {
    if (!accountId) {
      return;
    }

    // Skip API call if we already have data from evidence
    if (dataForServiceMap.length > 0) {
      return;
    }

    setIsLoading(true);
    setErrorMessage('');

    if (selectedSourceType === 'ebpf') {
      getServiceMapFromRelay();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, startDateInMilli, endDateInMilli, selectedSourceType, selectedWorkloads, selectedNamespace, dataForServiceMap.length]);

  // 3. Graph Calculation (Uses debounced slider value)
  const { nodes: rawNodes, edges: rawEdges } = useGraphTransformation(
    serviceMapData,
    selectedNamespace,
    debouncedDuration,
    showOnlyErrorEdges,
    selectedWorkloads,
    appName,
    selectedSourceType
  );

  useEffect(() => {
    if (!rawNodes.length) {
      setNodes([]);
      setEdges([]);
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
  }, [rawNodes, rawEdges, setNodes, setEdges]); // Dependency on raw structures

  // -- Handlers --

  const onNodeMouseEnter = useCallback(
    (_, node) => {
      const connectedIds = edges.filter((e) => e.source === node.id || e.target === node.id).map((e) => e.id);

      setHighlightedEdges(connectedIds);
      setNodes((nds) =>
        nds.map((n) => {
          if (n.id === node.id) {
            return { ...n, data: { ...n.data, isHovered: true } };
          }
          return n;
        })
      );
    },
    [edges, setNodes]
  );

  const onNodeMouseLeave = useCallback(() => {
    setHighlightedEdges([]);
    setNodes((nds) => nds.map((n) => ({ ...n, data: { ...n.data, isHovered: false } })));
  }, [setNodes]);

  const handleStartDateEndDate = (obj) => {
    setStartDateInMilli(obj.startTime);
    setEndDateInMilli(obj.endTime);
    setShortcutClickTime(obj.shortcutClickTime || 0);
  };

  function handleWorkloadFilterChange(event) {
    setErrorMessage('');
    let selectedTarget = event.target.value;
    setSelectedWorkloads(selectedTarget ? [selectedTarget] : []);
  }

  const filterWorkloadOnNamespace = (namespacesOverride) => {
    // FIX: Use the passed argument if available, otherwise use state
    const nsToCheck = namespacesOverride || selectedNamespace;

    const filterWorkloads = Array.from(
      new Set(
        allWorkloads
          .filter((w) => nsToCheck.includes(w.namespace))
          .map((m) => m.name?.trim())
          .filter((name) => name)
      )
    ).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));

    setSelectedNamespaceWorkloads(filterWorkloads);
  };

  function handleNamespaceFilterChange(event) {
    setErrorMessage('');
    let selectedTarget = event.target.value;
    setSelectedWorkloads([]);
    setSelectedNamespaceWorkloads([]);
    const newSelectedNamespaces = selectedTarget ? [selectedTarget] : [];
    setSelectedNamespace(newSelectedNamespaces);
    filterWorkloadOnNamespace(newSelectedNamespaces);
  }

  const resetStates = () => {
    if (!namespaceName) {
      setSelectedNamespace([]);
    }
    setSelectedNamespaceWorkloads([]);
    setNamespaces([]);
    if (!appName) {
      setSelectedWorkloads([]);
    }
    setServiceMapData([]);
    setNodes([]);
    setEdges([]);
    setShowOnlyErrorEdges(false);
    setDuration();
    setErrorMessage('');
    setSelectedEdge({});
    setShowTraces(false);
    setShortcutClickTime(1 * 60 * 60 * 1000);
  };

  // Pre-process edges for rendering (inject highlight prop)
  const edgesWithHighlight = useMemo(() => {
    return edges.map((e) => ({
      ...e,
      data: { ...e.data, isHighlighted: highlightedEdges.includes(e.id) },
    }));
  }, [edges, highlightedEdges]);

  // -- Render --

  if (supportsServiceMap !== null && !supportsServiceMap) {
    return (
      <Box
        sx={{
          border: '1px solid',
          borderColor: 'divider',
          borderRadius: '8px',
          bgcolor: colors.background.white,
        }}
      >
        <EmptyData
          id='service-map-unsupported'
          img={noDataImg}
          heading='Service Map not supported'
          subHeading={`Your current trace provider ${tracesProviderName ? `(${tracesProviderName}) ` : ''}does not support the service map view.`}
          height='400px'
          sx={{ flexDirection: 'column', gap: '16px', textAlign: 'center' }}
        />
      </Box>
    );
  }

  if (errorMessage) {
    return (
      <Box p={2}>
        <Typography>{errorMessage}</Typography>
      </Box>
    );
  }

  return (
    <div>
      {/* Node Details Modal */}
      <Modal
        width='lg'
        open={Object.keys(selectedNode).length > 0}
        handleClose={() => setSelectedNode({})}
        title={`Node Details: ${selectedNode?.Id?.name || ''}`}
        contentStyles={{ padding: 0, height: '600px' }}
      >
        <NodeDetails node={selectedNode} />
      </Modal>
      <Modal
        width='lg'
        open={showTraces}
        handleClose={() => setShowTraces(false)}
        title={`Traces: ${selectedEdge.source} -> ${selectedEdge.target}`}
        contentStyles={{ padding: 0, height: '600px' }}
      >
        <KubernetesTracesListing
          namespace={selectedEdge.source?.split('|')[1]}
          workloadName={selectedEdge.source?.split('|')[0]}
          destinationNamespace={selectedEdge.target?.split('|')[1]}
          destinationWorkload={selectedEdge.target?.split('|')[0]}
          passedSelectedTimestamp={{ startTimestamp: startDateInMilli, endTimestamp: endDateInMilli }}
          fixedTrace
          accountId={accountId}
          traceIds={selectedEdge?.data?.DrillDown?.sample_trace_ids || []}
        />
      </Modal>

      <BoxLayout2
        id='service-map-content'
        dateTimeRange={{
          enabled: !appName,
          onChange: handleStartDateEndDate,
          passedSelectedDateTime: { startTime: startDateInMilli, endTime: endDateInMilli, shortcutClickTime },
          shortCuts: ['Last 5 Minutes', 'Last 1 Hour', 'Last 24 Hours'],
        }}
        sharingOptions={{ sharing: { enabled: false }, download: { enabled: false } }}
        minDate={minDateBySource}
        filterOptions={[
          ...(showSourceType
            ? [
                {
                  type: 'dropdown',
                  label: 'Source Type',
                  options: ['otel', 'ebpf'],
                  value: selectedSourceType,
                  onSelect: (e) => {
                    setSelectedSourceType(e.target.value);
                    resetStates();
                  },
                },
              ]
            : []),

          ...(!appName
            ? [
                ...(selectedSourceType !== 'otel'
                  ? [
                      {
                        type: 'dropdown',
                        label: 'Namespaces',
                        options: namespaces,
                        value: selectedNamespace[0] ?? null,
                        onSelect: handleNamespaceFilterChange,
                        isOptionsLoading: isLoading,
                      },
                    ]
                  : []),
                {
                  type: 'dropdown',
                  label: 'Workloads',
                  options: selectedNamespaceWorkloads,
                  value: selectedWorkloads[0] ?? null,
                  onSelect: handleWorkloadFilterChange,
                  isOptionsLoading: isLoading,
                },
              ]
            : []),
        ]}
        extraOptions={
          !appName
            ? [
                <Box key='duration-slider-wrapper'>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                    <AccessTimeOutlinedIcon sx={{ fontSize: 18, color: '#6B7280' }} />

                    <Typography
                      sx={{
                        fontSize: '14px',
                        color: '#111827',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      Duration
                    </Typography>
                  </Box>
                  <CustomizedSlider
                    key='duration-slider'
                    name='Latency Threshold'
                    width={160}
                    min={0}
                    max={60}
                    onChange={(val) => setDuration(val)}
                    value={duration}
                    paddingTop='0px'
                    paddingLeft='2px'
                    enableTooltip={false}
                    unit={'s'}
                  />
                </Box>,
                <Box
                  key='error-switch'
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '8px',
                    px: '12px',
                    py: '6px',
                    ml: '6px',
                    borderRadius: '20px',
                    border: '1px solid #E0E0E0',
                    backgroundColor: '#F9FAFB',
                    minWidth: 140,
                  }}
                >
                  <VisibilityOutlinedIcon sx={{ fontSize: 18, color: '#6B7280' }} />
                  <Typography
                    sx={{
                      fontSize: '14px',
                      color: '#111827',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    Red Edges
                  </Typography>
                  <Switch
                    checked={showOnlyErrorEdges}
                    onChange={(e) => setShowOnlyErrorEdges(e.target.checked)}
                    size='small'
                    sx={{
                      ml: '4px',
                      '& .MuiSwitch-switchBase.Mui-checked': {
                        color: '#fff',
                      },
                      '& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track': {
                        backgroundColor: '#3B82F6', // blue
                        opacity: 1,
                      },
                    }}
                  />
                </Box>,
                <CustomTooltip
                  title={<ServiceMapLegends mode='service_map' />}
                  placement='right'
                  tooltipStyle={{ maxWidth: '400px', padding: '16px' }}
                >
                  <InfoOutlinedIcon fontSize='small' color='action' sx={{ ml: 0.5, cursor: 'pointer' }} />
                </CustomTooltip>,
              ]
            : []
        }
      >
        {selectedSourceType === 'otel' && !appName && (
          <LogQueryBuilderAutocomplete
            logProvider='service_map'
            accountId={accountId}
            onQueryChange={(e) => {
              setLabelFilters(e?.query ?? '');
            }}
            queryItems={queryItems}
            onQueryItemsChange={setQueryItems}
            getLabelsFromProps={labelMap}
            getLabelValuesFromProps={valueMap}
            allowMultipleQueries={false}
            height='10vh'
            width='100%'
          />
        )}
        <div style={{ height: 800, position: 'relative', border: '1px solid #ddd', borderRadius: 4, marginTop: '10px' }}>
          {(isLoading || isLayoutLoading) && (
            <div
              style={{
                position: 'absolute',
                top: 0,
                left: 0,
                right: 0,
                bottom: 0,
                zIndex: 10,
                background: 'rgba(255,255,255,0.7)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <Loader />
            </div>
          )}

          <ReactFlow
            nodes={nodes}
            edges={edgesWithHighlight}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={(_, n) => setSelectedNode(n.data?.entireNodeInstance)}
            onEdgeClick={(_, e) => {
              setSelectedEdge(e);
              setShowTraces(true);
            }}
            onNodeMouseEnter={onNodeMouseEnter}
            onNodeMouseLeave={onNodeMouseLeave}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            fitView
            minZoom={0.1}
            proOptions={{ hideAttribution: true }}
            style={{
              background: 'white',
              width: '100%',
              border: '1px solid blue',
              borderRadius: '2px',
            }}
          >
            <Background color={colors.background.reactFlow} variant={BackgroundVariant.Lines} gap={24} />
            <Controls
              position={'top-left'}
              fitViewOptions={{
                minZoom: 0,
              }}
            />
            <MiniMap />
            <ServiceMapDownloadImage />
          </ReactFlow>
        </div>
      </BoxLayout2>
    </div>
  );
};

KubernetesServiceMap.propTypes = {
  accountId: PropTypes.string.isRequired,
  appName: PropTypes.string,
  dateRange: PropTypes.object,
  namespaceName: PropTypes.string,
  showSourceType: PropTypes.bool,
  dataForServiceMap: PropTypes.array,
};

const KubernetesServiceMapWrapper = (props) => (
  <ReactFlowProvider>
    <KubernetesServiceMap {...props} />
  </ReactFlowProvider>
);

export default KubernetesServiceMapWrapper;
