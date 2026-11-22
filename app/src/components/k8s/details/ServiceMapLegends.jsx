import { ds } from '@utils/colors';
import { Box, Typography } from '@mui/material';
import { Divider } from '@ui/Divider';
import { snakeToTitleCase } from '@utils/common';

// relationshipTypeCategories mirrors the RelationshipType constants and their grouping in
// api-server/services/knowledge_graph/core/types.go. The graph draws an edge for whichever
// type the backend sends, so a type missing here is an edge a user cannot look up — which is
// how this list drifted into covering 15 of them. Keeping the grouping identical to the Go
// file's makes it obvious where a newly added type belongs; a test asserts the two agree.
export const relationshipTypeCategories = [
  {
    category: 'Service',
    items: [
      { type: 'CALLS', desc: 'Service-to-service communication' },
      { type: 'PUBLISHES_TO', desc: 'Workload publishing to a queue or topic' },
      { type: 'SUBSCRIBES_TO', desc: 'Workload consuming from a queue or topic' },
      // Used for a pod on its node and for a service in its namespace, so the description
      // covers both rather than only the node case.
      { type: 'RUNS_ON', desc: 'Pod on the node, or service in the namespace, that runs it' },
    ],
  },
  {
    category: 'Network flow',
    items: [{ type: 'CONNECTION_REJECTED', desc: 'Connection attempt that was blocked — not a live dependency' }],
  },
  {
    category: 'Deployment & configuration',
    items: [
      { type: 'IS_DEPLOYED_FROM', desc: 'Workload deployed from a source repository' },
      { type: 'IS_CONFIGURED_BY', desc: 'Configuration source (ConfigMap, Secret or Helm chart)' },
      { type: 'REFERENCES_IMAGE', desc: 'Configuration referencing a container image' },
      { type: 'USES_IMAGE', desc: 'Workload running a container image' },
      { type: 'PULLS_FROM', desc: 'Image retrieval source' },
    ],
  },
  {
    category: 'Ownership & grouping',
    items: [
      { type: 'BELONGS_TO', desc: 'Resource grouped under a namespace or cluster' },
      { type: 'RUNS_IN', desc: 'Workload running in a cluster or namespace' },
      { type: 'MANAGES', desc: 'Parent managing a child resource, such as a Deployment and its Pods' },
      { type: 'OWNS', desc: 'Owner of the resource' },
      { type: 'MEMBER_OF', desc: 'User or group belonging to a group or organisation' },
    ],
  },
  {
    category: 'Configuration & secrets',
    items: [
      { type: 'USES_CONFIG', desc: 'Workload reading a ConfigMap' },
      { type: 'USES_SECRET', desc: 'Workload reading a Secret' },
      { type: 'USES_SERVICE_ACCOUNT', desc: 'Workload running under a service account' },
      { type: 'STORES_IN', desc: 'Secret stored in an external vault' },
      { type: 'IS_ENCRYPTED_BY', desc: 'Security encryption provider' },
    ],
  },
  {
    category: 'Storage',
    items: [
      { type: 'MOUNTS', desc: 'Storage volume attachment' },
      { type: 'IS_BOUND_TO', desc: 'Volume claim bound to a volume' },
      { type: 'PROVIDES_STORAGE', desc: 'Storage provisioning source' },
    ],
  },
  {
    category: 'Networking',
    items: [
      { type: 'EXPOSES', desc: 'Service exposing a workload' },
      { type: 'ROUTES_TO_SERVICE', desc: 'Ingress routing to a service' },
      { type: 'ROUTES_TO_BACKEND', desc: 'Load balancer routing to a backend service' },
      { type: 'PROTECTS', desc: 'Network policy protecting a workload' },
      { type: 'IS_ACCESSED_VIA', desc: 'Service reached through DNS or a CDN' },
    ],
  },
  {
    category: 'Cloud resources',
    items: [
      { type: 'ROUTES_THROUGH', desc: 'Network traffic path' },
      { type: 'HOSTED_ON', desc: 'Infrastructure hosting the resource' },
      { type: 'RESOLVES_TO', desc: 'DNS or service discovery resolution' },
      { type: 'ROUTES_TO', desc: 'Traffic routed to this target' },
      { type: 'ASSOCIATED_WITH', desc: 'Cloud resource attached to another, such as a public IP to an instance' },
    ],
  },
  {
    category: 'Observability',
    items: [
      { type: 'EMITS_LOGS_TO', desc: 'Logging destination' },
      { type: 'EMITS_METRICS_TO', desc: 'Metrics destination' },
      { type: 'EMITS_TRACES_TO', desc: 'Tracing destination' },
    ],
  },
  {
    category: 'Source code',
    items: [{ type: 'BUILT_FROM', desc: 'Source image or build origin' }],
  },
  {
    category: 'Identity',
    items: [
      { type: 'RUNS_AS', desc: 'Compute resource using this identity' },
      { type: 'ASSUMES', desc: 'Identity that can assume another identity' },
      { type: 'HAS_ACCESS_TO', desc: 'Identity granted access to a data resource' },
      { type: 'SAME_AS', desc: 'Per-integration identity linked to the same person' },
    ],
  },
];

// relationshipTypeLabel renders a type the way the graph labels its edges, so hovering an
// edge and reading this legend show the same name.
export const relationshipTypeLabel = (type) => snakeToTitleCase(type.toLowerCase());

const ServiceMapLegends = ({ mode = 'service_map' }) => {
  // 1. Existing Visual Legends (Colors & Line Types)
  const visualLegends = [
    {
      id: ds.teal[400],
      description: 'Node is from different selected namespace.',
      type: 'square',
    },
    {
      id: ds.blue[300],
      description: 'Node is from same selected namespaces.',
      type: 'square',
    },
    {
      id: ds.red[500],
      description: 'Error state between two nodes.',
      type: 'line',
    },
    {
      id: ds.gray[700],
      description: 'Standard connection.',
      type: 'line',
    },
    {
      id: ds.blue[500],
      description: 'Active/Selected connection path.',
      type: 'line',
    },
  ];

  const isServiceMap = mode === 'service_map';

  if (isServiceMap) {
    return (
      <aside
        style={{
          backgroundColor: ds.background[100],
          borderRadius: 'var(--ds-radius-lg)',
          padding: 'var(--ds-space-4)',
          boxShadow: `0px ${ds.space[1]} ${ds.space[3]} ${ds.gray.alpha[200]}`,
          maxWidth: ds.space.mul(0, 175),
          marginTop: 'var(--ds-space-2)',
          float: 'right',
          fontSize: 'var(--ds-text-small)',
        }}
      >
        <Typography variant='subtitle2' sx={{ fontWeight: 'bold', mb: ds.space[2], color: 'var(--ds-gray-600)' }}>
          Visual Keys
        </Typography>
        {visualLegends.map((item, index) => (
          <div
            key={`${index}-${item.id}`}
            style={{
              display: 'flex',
              alignItems: 'center',
              marginBottom: 'var(--ds-space-2)',
            }}
          >
            {item.type === 'square' ? (
              <div
                style={{
                  width: ds.space[4],
                  height: ds.space[4],
                  borderRadius: 'var(--ds-radius-sm)',
                  backgroundColor: ds.background[100],
                  border: `2px solid ${item.id}`,
                  marginRight: 'var(--ds-space-2)',
                  flexShrink: 0,
                }}
              />
            ) : (
              <div
                style={{
                  width: ds.space.mul(0, 10),
                  height: '0px',
                  borderBottom: `2px dashed ${item.id}`,
                  marginRight: 'var(--ds-space-2)',
                  flexShrink: 0,
                }}
              />
            )}
            <span style={{ color: ds.blue[500], lineHeight: 1.2 }}>{item.description}</span>
          </div>
        ))}
      </aside>
    );
  }

  return (
    <Box
      sx={{
        width: ds.space.mul(0, 170),
        // Every relationship type is listed, which is taller than most screens — cap the
        // height against the viewport and scroll inside the tooltip instead of overflowing it.
        maxHeight: '60vh',
        overflowY: 'auto',
        pr: 'var(--ds-space-1)',
      }}
    >
      <Typography
        variant='body2'
        sx={{
          fontWeight: 'var(--ds-font-weight-semibold)',
          fontSize: 'var(--ds-text-body)',
          color: ds.gray[700],
          mb: 'var(--ds-space-1)',
        }}
      >
        Relationship Types
      </Typography>
      <Typography
        variant='caption'
        sx={{
          color: 'var(--ds-brand-400)',
          fontSize: 'var(--ds-text-caption)',
          lineHeight: 1.3,
          display: 'block',
          mb: 'var(--ds-space-3)',
        }}
      >
        How resources in your infrastructure are connected to each other.
      </Typography>

      {relationshipTypeCategories.map((cat, catIdx) => (
        <Box key={cat.category} sx={{ mb: catIdx < relationshipTypeCategories.length - 1 ? ds.space.mul(0, 5) : 0 }}>
          <Typography
            variant='caption'
            sx={{
              fontWeight: 'var(--ds-font-weight-semibold)',
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-brand-300)',
              textTransform: 'uppercase',
              letterSpacing: '0.5px',
              display: 'block',
              mb: 'var(--ds-space-1)',
            }}
          >
            {cat.category}
          </Typography>

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
            {cat.items.map((rel) => (
              <Box
                key={rel.type}
                sx={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: 'var(--ds-space-2)',
                  py: 'var(--ds-space-1)',
                }}
              >
                <Typography
                  variant='caption'
                  sx={{
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    fontSize: 'var(--ds-text-caption)',
                    color: ds.gray[700],
                    fontFamily: ds.font.mono,
                    whiteSpace: 'nowrap',
                    minWidth: ds.space.mul(0, 65),
                  }}
                >
                  {relationshipTypeLabel(rel.type)}
                </Typography>
                <Typography
                  variant='caption'
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: 'var(--ds-brand-400)',
                    lineHeight: 1.3,
                  }}
                >
                  {rel.desc}
                </Typography>
              </Box>
            ))}
          </Box>

          {catIdx < relationshipTypeCategories.length - 1 && <Divider color='var(--ds-brand-150)' sx={{ mt: 'var(--ds-space-2)' }} />}
        </Box>
      ))}
    </Box>
  );
};

ServiceMapLegends.displayName = 'ServiceMapLegends';

export default ServiceMapLegends;
