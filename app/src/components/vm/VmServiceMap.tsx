import React, { useEffect, useMemo, useState } from 'react';
import ReactFlow, { ReactFlowProvider, Controls, Background, BackgroundVariant, useNodesState, useEdgesState, EdgeTypes } from 'reactflow';
import 'reactflow/dist/style.css';
import { Box, Typography } from '@mui/material';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import { Switch } from '@ui/Switch';
import { Toggle } from '@ui/Toggle';
import FilterDropdown from '@ui/FilterDropdown';
import Loader from '@shared/Loader';
import LangTypeIconUntyped from '@shared/icons/LangTypeIcon';
import apiVm from '@api1/vm';
import CustomNode from '@components/k8s/details/CustomNode';
import CustomEdge from '@components/k8s/details/CustomEdge';
import { listHosts, scopeToHost } from './vmServiceMapData';

/**
 * Service map of a self-hosted VM account.
 *
 * The forager builds the calls from the node-agent's metrics and the server
 * names the destinations, so the answer has the same application shape as the
 * K8s map and the K8s node and edge components draw it unchanged. The data is
 * not K8s-specific beyond that: no workloads, namespaces, or trace source.
 */

// The icon is untyped JS; it takes the application kind as appLang.
const LangTypeIcon = LangTypeIconUntyped as React.ComponentType<{ appLang?: string }>;

const WINDOWS = [
  { value: '900', label: '15m' },
  { value: '3600', label: '1h' },
  { value: '21600', label: '6h' },
  { value: '86400', label: '24h' },
];

const NODE_WIDTH = 300;
const NODE_HEIGHT = 100;

let elkPromise: Promise<any> | null = null;

// elkjs is ~1.4MB; load it when the first map mounts, once, like the K8s map.
const layout = async (nodes: any[], edges: any[]) => {
  try {
    if (!elkPromise) {
      elkPromise = import('elkjs/lib/elk.bundled.js')
        .then((M: any) => new M.default())
        .catch((err) => {
          elkPromise = null;
          throw err;
        });
    }
    const elk = await elkPromise;
    const graph = await elk.layout({
      id: 'root',
      layoutOptions: {
        'elk.algorithm': 'layered',
        'elk.direction': 'RIGHT',
        'elk.spacing.nodeNode': '60',
        'elk.spacing.nodeNodeBetweenLayers': '80',
      },
      children: nodes.map((n) => ({ ...n, width: NODE_WIDTH, height: NODE_HEIGHT, targetPosition: 'left', sourcePosition: 'right' })),
      edges,
    });
    return graph.children.map((n: any) => ({ ...n, position: { x: n.x, y: n.y } }));
  } catch (err) {
    console.error('Service map layout failed:', err);
    return nodes;
  }
};

// kind is part of the key: a host can run a systemd unit and a Docker
// container of the same name, and the server keeps them as distinct
// applications (vmservicemap/build.go keys on namespace+kind+name too).
const nodeKey = (id: { name: string; namespace: string; kind: string }) => `${id.name}|${id.namespace}|${id.kind}`;

/** Applications from the API → React Flow nodes and edges. One edge per call, drawn caller → callee. */
const toGraph = (applications: any[], errorsOnly: boolean, showIdle: boolean) => {
  const nodes = new Map<string, any>();
  const edges: any[] = [];

  const addNode = (app: any) => {
    const key = nodeKey(app.Id);
    if (nodes.has(key)) return;
    nodes.set(key, {
      id: key,
      type: 'vm-node',
      position: { x: 0, y: 0 },
      data: {
        label: (
          <div className='sm-node'>
            <div aria-label={app.Id.name} style={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <LangTypeIcon appLang={app.Id.kind} />
              <span className='sm-node-label1'>{app.Id.name}</span>
            </div>
            <div className='sm-node'>
              <span className='sm-node-label2'>{app.Id.namespace}</span>
            </div>
          </div>
        ),
        changeColor: app.Category?.category === 'external',
        entireNodeInstance: { ...app, Status: app.Status || 'ok' },
        requestsPerSecond: false,
        isExternal: app.Category?.category === 'external',
      },
    });
  };

  // A service nobody calls and that calls nobody is idle: drawn only on request,
  // so a host's dozens of system units do not bury the services that talk.
  const connected = (app: any) => (app.Upstreams?.length || 0) + (app.Downstreams?.length || 0) > 0;
  applications.filter((app) => showIdle || connected(app)).forEach(addNode);
  applications.forEach((app) => {
    (app.Upstreams || []).forEach((link: any, i: number) => {
      if (errorsOnly && !(link.FailureCount > 0)) return;
      // Link ids are "namespace:kind:name"; names have no ":" (the server strips them).
      const [namespace, kind, name] = String(link.Id).split(':');
      const target = `${name}|${namespace}|${kind}`;
      if (!nodes.has(target)) return;
      edges.push({
        id: `${nodeKey(app.Id)}->${target}#${i}`,
        source: nodeKey(app.Id),
        target,
        type: 'vm-edge',
        animated: true,
        markerEnd: { type: 'arrow', color: link.FailureCount > 0 ? 'var(--ds-red-500)' : 'var(--ds-gray-600)' },
        label: link.Protocol || '',
        data: { ...link, requestsPerSecond: false },
      });
    });
  });
  return { nodes: Array.from(nodes.values()), edges };
};

interface VmServiceMapProps {
  accountId: string;
}

const VmServiceMapInner = ({ accountId }: VmServiceMapProps) => {
  const [windowSeconds, setWindowSeconds] = useState('900');
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [showIdle, setShowIdle] = useState(false);
  const [host, setHost] = useState<string | null>(null);
  const [applications, setApplications] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);

  const nodeTypes = useMemo(() => ({ 'vm-node': CustomNode }), []);
  const edgeTypes = useMemo(() => ({ 'vm-edge': CustomEdge } as unknown as EdgeTypes), []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError('');
    apiVm
      .getServiceMap(accountId, Number(windowSeconds))
      .then((apps) => {
        if (!cancelled) setApplications(apps);
      })
      .catch((err) => {
        if (cancelled) return;
        console.error('Failed to load the service map:', err);
        setApplications([]);
        setError(err?.message || 'Failed to load the service map');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [accountId, windowSeconds]);

  const hosts = useMemo(() => listHosts(applications), [applications]);
  // A VM that is no longer in the map (window changed, agent stopped) clears itself.
  const selectedHost = host && hosts.some((h) => h.name === host) ? host : null;
  const visible = useMemo(() => (selectedHost ? scopeToHost(applications, selectedHost) : applications), [applications, selectedHost]);
  // Picking a VM is asking what is present on it, so its quiet services are shown too.
  const graph = useMemo(() => toGraph(visible, errorsOnly, showIdle || !!selectedHost), [visible, errorsOnly, showIdle, selectedHost]);
  const hostSummary = hosts.find((h) => h.name === selectedHost);

  useEffect(() => {
    let cancelled = false;
    if (!graph.nodes.length) {
      setNodes([]);
      setEdges([]);
      return undefined;
    }
    layout(graph.nodes, graph.edges).then((laidOut) => {
      if (cancelled) return;
      setNodes(laidOut);
      setEdges(graph.edges);
    });
    return () => {
      cancelled = true;
    };
  }, [graph, setNodes, setEdges]);

  const hasCalls = selectedHost ? visible.length > 0 : applications.some((app) => app.Upstreams?.length);

  return (
    <Box sx={{ p: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-4)', flexWrap: 'wrap' }}>
        <Toggle options={WINDOWS} activeValue={windowSeconds} onChange={setWindowSeconds} size='sm' />
        <FilterDropdown
          label='VM'
          options={hosts.map((h) => h.name)}
          value={selectedHost}
          onSelect={(e: any) => setHost(e?.target?.value || null)}
          size='sm'
        />
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-700)' }}>Red Edges</Typography>
          <Switch checked={errorsOnly} onChange={(_e: unknown, checked: boolean) => setErrorsOnly(checked)} size='sm' />
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-700)' }}>Idle services</Typography>
          <Switch
            checked={showIdle || !!selectedHost}
            disabled={!!selectedHost}
            onChange={(_e: unknown, checked: boolean) => setShowIdle(checked)}
            size='sm'
          />
        </Box>
      </Box>

      {hostSummary && (
        <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-700)' }}>
          {hostSummary.name}: {hostSummary.systemd} systemd {hostSummary.systemd === 1 ? 'service' : 'services'}, {hostSummary.docker}{' '}
          {hostSummary.docker === 1 ? 'container' : 'containers'}
          {hostSummary.other > 0 ? `, ${hostSummary.other} other ${hostSummary.other === 1 ? 'process' : 'processes'}` : ''}
        </Typography>
      )}

      {error && <Banner tone='critical' surface='section' title='Service map unavailable' message={error} />}

      {error ? null : !loading && !hasCalls ? (
        <EmptyState
          title='No calls seen in this window'
          description='The agent on each VM reports the requests that VM makes. Calls appear here once the agents have traffic to report; requests from hosts without an agent are not seen.'
        />
      ) : (
        <div
          style={{
            height: '70vh',
            position: 'relative',
            border: '1px solid var(--ds-gray-300)',
            borderRadius: 'var(--ds-radius-sm)',
          }}
        >
          {loading && (
            <div
              style={{
                position: 'absolute',
                inset: 0,
                zIndex: 10,
                background: 'color-mix(in srgb, var(--ds-background-100) 70%, transparent)',
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
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            fitView
            minZoom={0.1}
            proOptions={{ hideAttribution: true }}
            style={{ background: 'var(--ds-background-100)', width: '100%', borderRadius: 'var(--ds-radius-sm)' }}
          >
            <Background color='var(--ds-gray-200)' variant={BackgroundVariant.Lines} gap={24} />
            <Controls position='top-left' fitViewOptions={{ minZoom: 0 }} />
          </ReactFlow>
        </div>
      )}
    </Box>
  );
};

const VmServiceMap = (props: VmServiceMapProps) => (
  <ReactFlowProvider>
    <VmServiceMapInner {...props} />
  </ReactFlowProvider>
);

export default VmServiceMap;
