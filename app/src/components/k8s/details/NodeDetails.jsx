import { memo, useState } from 'react';
import dynamic from 'next/dynamic';
import PropTypes from 'prop-types';
import { getTableData4 } from '@components/k8s/investigate/cards/util';
import CustomTable from '@shared/tables/CustomTable2';
import { ListingLayout } from '@ui/ListingLayout';
import { Box, Typography, Chip } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import Tabs from '@shared/navigation/Tabs';
import CopyButton from '@shared/buttons/CopyButton';
import { snakeToTitleCase } from 'src/utils/common';
import { getSpecificTime, getYesterday } from '@lib/datetime';

// Lazy-load the heavy tab bodies so opening a node popup doesn't pull the events /
// charts / observability bundles unless the user switches to those tabs.
const KubernetesEventsTable = dynamic(() => import('@components/events/KubernetesEvents'), { ssr: false });
const KubernetesUtilizationCharts3 = dynamic(() => import('@components/k8s/common/KubernetesTable').then((m) => m.KubernetesUtilizationCharts3), {
  ssr: false,
});
const KubernetesTracesListing = dynamic(() => import('@components/k8s/details/KubernetesTracesListing'), { ssr: false });
const KubernetesLogs = dynamic(() => import('@components/k8s/details/KubernetesLogs'), { ssr: false });
const AppDashboard = dynamic(() => import('@components/dashboards/AppDashboard'), { ssr: false });
const KubernetesLogsPattern = dynamic(() => import('@components/k8s/details/KubernetesLogsPattern'), { ssr: false });

const TAB_DETAILS = 'details';
const TAB_EVENTS = 'events';
const TAB_UTILIZATION = 'utilization';
const TAB_TRACES = 'traces';
const TAB_LOGS = 'logs';
const TAB_APP_DASHBOARD = 'appDashboard';
const TAB_LOG_GROUP = 'logGroup';

// Parse a KG unique key into its parts. Format (see knowledge_graph unique_key_builder):
//   {source}:{cloud_account_id}:{location}:{NodeType}:{hierarchy/namespace}:{name}
// Used only as a fallback when the node's `properties` is missing a field.
const parseKgUniqueKey = (uniqueKey) => {
  if (typeof uniqueKey !== 'string') return {};
  const parts = uniqueKey.split(':');
  if (parts.length < 6) return {};
  // Name is the final segment(s); rejoin in case a name legitimately contains ':'.
  return { source: parts[0], accountId: parts[1], nodeType: parts[3], namespace: parts[4], name: parts.slice(5).join(':') };
};

// KG node types that map to a running workload (pod-backed, so they have
// utilization metrics and events). Deliberately excludes non-runtime k8s
// resources that also carry a namespace — K8sSecret, ConfigMap, K8sService,
// K8sServiceAccount, Ingress, PersistentVolumeClaim, Service (logical), etc.
const WORKLOAD_NODE_TYPES = new Set(['Workload', 'Job', 'CronJob']);

// Resolve the identifiers a K8s resource node needs to scope events / utilization
// queries. Prefers `properties`, falls back to unique_key parsing. Both queries
// are keyed by node type; non-runtime resource types yield null (no tabs).
const getNodeScope = (node) => {
  const props = node?.properties ?? {};
  const parsed = parseKgUniqueKey(node?.unique_key || node?.uniqueKey);
  const source = node?.source || parsed.source;
  const accountId = node?.cloud_account_id || parsed.accountId;
  const namespace = props.namespace || parsed.namespace;
  const name = props.name || parsed.name;
  const nodeType = node?.node_type || parsed.nodeType;
  const kind = props.kind;
  const isK8s = source?.toLowerCase() === 'k8s';

  // Utilization is pod-metric backed (`getK8sPodGroupings2`); events filter the
  // events store. Both are only meaningful for runtime node types, keyed below.
  let utilizationQuery = null;
  let eventsDefaultQuery = null;
  if (isK8s && accountId && name) {
    if (nodeType === 'Namespace') {
      utilizationQuery = { namespace_name: name };
      eventsDefaultQuery = { namespace: name };
    } else if (nodeType === 'Node') {
      utilizationQuery = { nodeName: name };
      eventsDefaultQuery = { subjectName: name };
    } else if (nodeType === 'Pod' && namespace) {
      utilizationQuery = { pod_name: name, namespace_name: namespace };
      eventsDefaultQuery = { namespace, subjectName: name };
    } else if (WORKLOAD_NODE_TYPES.has(nodeType) && namespace) {
      utilizationQuery = { workload_name: name, namespace_name: namespace, workloadType: kind };
      eventsDefaultQuery = { namespace, workloadName: name };
    }
  }

  // Observability tabs (Traces, Logs, App Dashboard, Log Group) mirror the k8s
  // application page and are workload-specific — only offered for Workload/Job/
  // CronJob nodes that have a namespace + name. (Service Map is omitted: the KG
  // graph view is itself the service map.)
  const workloadScope = isK8s && accountId && namespace && name && WORKLOAD_NODE_TYPES.has(nodeType) ? { namespace, name, kind } : null;

  return { accountId, utilizationQuery, eventsDefaultQuery, workloadScope };
};

// Tooltip content for path nodes
const PathNodeTooltipContent = memo(({ pathNode }) => {
  if (!pathNode) return null;

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--ds-space-1)',
        padding: 'var(--ds-space-1)',
        minWidth: '200px',
        maxWidth: '400px',
      }}
    >
      {pathNode.accountName && (
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Account:</Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-background-100)', wordBreak: 'break-word' }}>
            {pathNode.accountName}
          </Typography>
        </Box>
      )}
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Name:</Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-background-100)', wordBreak: 'break-word' }}>
          {pathNode.name || '-'}
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Type:</Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-background-100)' }}>
          {snakeToTitleCase(pathNode.nodeType || '')}
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'flex-start' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', minWidth: '70px' }}>Unique Key:</Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-300)',
            wordBreak: 'break-all',
            fontFamily: 'var(--ds-font-mono)',
          }}
        >
          {pathNode.uniqueKey || pathNode.label}
        </Typography>
      </Box>
    </Box>
  );
});
PathNodeTooltipContent.displayName = 'PathNodeTooltipContent';
PathNodeTooltipContent.propTypes = {
  pathNode: PropTypes.shape({
    accountName: PropTypes.string,
    name: PropTypes.string,
    nodeType: PropTypes.string,
    uniqueKey: PropTypes.string,
    label: PropTypes.string,
  }),
};

const DetailsBody = ({ node }) => {
  const labels = node?.properties ?? node?.labels ?? node?.Labels ?? node;
  const { headers = [], convertedJson2 = [] } = getTableData4([labels] || [{}]);
  const pathToNode = node?.pathToNode || [];
  const uniqueKey = node?.unique_key || node?.uniqueKey;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {uniqueKey && (
        <Box
          sx={{
            padding: 'var(--ds-space-4)',
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            borderBottom: '1px solid var(--ds-gray-200)',
          }}
          data-testid='kg-node-unique-key'
        >
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', minWidth: '90px', flexShrink: 0 }}>Unique Key:</Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontFamily: 'var(--ds-font-mono)',
              color: 'var(--ds-gray-700)',
              wordBreak: 'break-all',
              flex: 1,
            }}
          >
            {uniqueKey}
          </Typography>
          <CopyButton text={uniqueKey} size='sm' />
        </Box>
      )}
      {/* Path section - show whenever there's path data */}
      {pathToNode.length > 0 && (
        <Box sx={{ padding: 'var(--ds-space-4)', bgcolor: 'var(--ds-background-300)', borderBottom: '1px solid var(--ds-gray-200)' }}>
          <Typography variant='subtitle2' sx={{ marginBottom: 'var(--ds-space-2)', color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)' }}>
            Path to this node:
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            {pathToNode.map((pathNode, index) => (
              <Box key={pathNode.id} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                {index > 0 && pathNode.edgeFromPrev && (
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-blue-600)', padding: '0 var(--ds-space-1)' }}>
                    —[{pathNode.edgeFromPrev}]→
                  </Typography>
                )}
                {index > 0 && !pathNode.edgeFromPrev && (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', padding: '0 var(--ds-space-1)' }}>→</Typography>
                )}
                <Tooltip
                  title={<PathNodeTooltipContent pathNode={pathNode} />}
                  placement='bottom'
                  tooltipStyle={{
                    backgroundColor: 'var(--ds-gray-alpha-700)',
                    padding: 'var(--ds-space-2) var(--ds-space-3)',
                    maxWidth: '450px',
                  }}
                >
                  <Chip
                    label={pathNode.name ? `${pathNode.name} (${snakeToTitleCase(pathNode.nodeType || '')})` : pathNode.label}
                    size='small'
                    variant={index === pathToNode.length - 1 ? 'filled' : 'outlined'}
                    color={index === pathToNode.length - 1 ? 'primary' : 'default'}
                    sx={{ fontSize: 'var(--ds-text-caption)', height: '24px', cursor: 'pointer' }}
                  />
                </Tooltip>
              </Box>
            ))}
          </Box>
        </Box>
      )}

      {/* Existing labels table */}
      <Box sx={{ flex: 1, overflow: 'auto', margin: 'var(--ds-space-4)' }}>
        <ListingLayout id='node-details-labels'>
          <ListingLayout.Toolbar title='Labels' />
          <ListingLayout.Body>
            <CustomTable
              tableData={convertedJson2}
              headers={headers}
              rowsPerPage={convertedJson2.length || 0}
              totalRows={convertedJson2.length || 0}
              onPageChange={undefined}
            />
          </ListingLayout.Body>
        </ListingLayout>
      </Box>
    </Box>
  );
};

DetailsBody.propTypes = {
  node: PropTypes.shape({
    properties: PropTypes.object,
    labels: PropTypes.object,
    Labels: PropTypes.object,
    pathToNode: PropTypes.array,
    name: PropTypes.string,
    unique_key: PropTypes.string,
    uniqueKey: PropTypes.string,
  }),
};

const NodeDetails = ({ node }) => {
  const [tab, setTab] = useState(TAB_DETAILS);
  const { accountId, utilizationQuery, eventsDefaultQuery, workloadScope } = getNodeScope(node);

  // Assemble the tab set based on which scoped views the node supports.
  const tabOptions = [{ value: TAB_DETAILS, text: 'Details' }];
  if (utilizationQuery) tabOptions.push({ value: TAB_UTILIZATION, text: 'Utilization Trends' });
  if (eventsDefaultQuery) tabOptions.push({ value: TAB_EVENTS, text: 'Related Events' });
  if (workloadScope) {
    tabOptions.push(
      { value: TAB_TRACES, text: 'Traces' },
      { value: TAB_LOGS, text: 'Logs' },
      { value: TAB_APP_DASHBOARD, text: 'App Dashboard' },
      { value: TAB_LOG_GROUP, text: 'Log Group' }
    );
  }

  // Cloud/infra nodes (no scoped views) keep the original single-view layout.
  if (tabOptions.length === 1) {
    return <DetailsBody node={node} />;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Box
        sx={{
          position: 'sticky',
          top: 0,
          zIndex: 10,
          backgroundColor: 'var(--ds-background-100)',
          borderBottom: '1px solid var(--ds-gray-200)',
          px: 'var(--ds-space-4)',
        }}
      >
        <Tabs options={{ tabOptions }} value={tab} onChange={(next) => setTab(next)} behavior='filter' variant='secondary' ariaLabel='Node details' />
      </Box>
      <Box sx={{ flex: 1, overflow: 'auto' }}>
        {tab === TAB_DETAILS && <DetailsBody node={node} />}
        {tab === TAB_UTILIZATION && utilizationQuery && (
          <Box sx={{ padding: 'var(--ds-space-4)' }}>
            <KubernetesUtilizationCharts3 accountId={accountId} query={utilizationQuery} />
          </Box>
        )}
        {tab === TAB_EVENTS && eventsDefaultQuery && (
          <Box sx={{ padding: 'var(--ds-space-4)' }}>
            <KubernetesEventsTable accountId={accountId} defaultQuery={eventsDefaultQuery} enableFilters={false} recordsPerPage={10} />
          </Box>
        )}
        {workloadScope && tab === TAB_TRACES && (
          <Box sx={{ padding: 'var(--ds-space-4)' }}>
            <KubernetesTracesListing
              showNamespaceFilter={false}
              showWorkloadFilter={false}
              destinationNamespace={workloadScope.namespace}
              destinationWorkload={workloadScope.name}
              namespace={workloadScope.namespace}
              workloadName={workloadScope.name}
              accountId={accountId}
              passedSelectedTimestamp={{ startTimestamp: getYesterday().getTime(), endTimestamp: new Date().getTime() }}
              destinationName={''}
              showTimeFilter={true}
              httpStatus={''}
              duration={null}
              showStatusFilter={false}
              fromWorkload={true}
            />
          </Box>
        )}
        {workloadScope && tab === TAB_LOGS && (
          <Box sx={{ padding: 'var(--ds-space-4)' }}>
            <KubernetesLogs
              accountId={accountId}
              showTrend={false}
              showQueryTextBox={false}
              dateTime={{ startTime: getSpecificTime(60), endTime: new Date().getTime() }}
              queryFromProps={JSON.stringify({ namespaceName: workloadScope.namespace, workloadName: workloadScope.name })}
              showPolling={false}
            />
          </Box>
        )}
        {workloadScope && tab === TAB_APP_DASHBOARD && (
          <Box sx={{ padding: 'var(--ds-space-4)' }}>
            <AppDashboard accountId={accountId} namespaceName={workloadScope.namespace} workloadName={workloadScope.name} />
          </Box>
        )}
        {workloadScope && tab === TAB_LOG_GROUP && (
          <Box sx={{ padding: 'var(--ds-space-4)' }}>
            <KubernetesLogsPattern accountId={accountId} workloadName={workloadScope.name} workloadNamespace={workloadScope.namespace} />
          </Box>
        )}
      </Box>
    </Box>
  );
};

NodeDetails.propTypes = {
  node: PropTypes.shape({
    properties: PropTypes.object,
    labels: PropTypes.object,
    Labels: PropTypes.object,
    pathToNode: PropTypes.array,
    name: PropTypes.string,
    node_type: PropTypes.string,
    source: PropTypes.string,
    cloud_account_id: PropTypes.string,
    unique_key: PropTypes.string,
    uniqueKey: PropTypes.string,
  }),
};

export default NodeDetails;
