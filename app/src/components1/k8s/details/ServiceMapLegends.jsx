import { ds } from '@utils/colors';
import { Box, Typography } from '@mui/material';
import { Divider } from '@components1/ds/Divider';

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

  // 2. Categorized Relationship Definitions
  const relationshipCategories = [
    {
      category: 'Communication',
      items: [
        { label: 'CALLS', desc: 'Service-to-service communication' },
        { label: 'ROUTES_THROUGH', desc: 'Network traffic path' },
        { label: 'RESOLVES_TO', desc: 'DNS or Service discovery resolution' },
        { label: 'EXPOSES', desc: 'Service exposing a port or endpoint' },
      ],
    },
    {
      category: 'Infrastructure',
      items: [
        { label: 'RUNS_ON', desc: 'Workload running on a specific node' },
        { label: 'HOSTED_ON', desc: 'Infrastructure hosting the resource' },
        { label: 'BELONGS_TO', desc: 'Logical grouping ownership' },
      ],
    },
    {
      category: 'Storage & Config',
      items: [
        { label: 'MOUNTS', desc: 'Storage volume attachment' },
        { label: 'PROVIDES_STORAGE', desc: 'Storage provisioning source' },
        { label: 'IS_CONFIGURED_BY', desc: 'Configuration source (ConfigMap/Secret)' },
        { label: 'IS_BOUND_TO', desc: 'Resource binding configuration' },
      ],
    },
    {
      category: 'Build & Security',
      items: [
        { label: 'PULLS_FROM', desc: 'Image retrieval source' },
        { label: 'BUILT_FROM', desc: 'Source image or build origin' },
        { label: 'IS_ENCRYPTED_BY', desc: 'Security encryption provider' },
        { label: 'EMITS_LOGS_TO', desc: 'Logging destination' },
      ],
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
          boxShadow: '0px 4px 12px rgba(0, 0, 0, 0.1)',
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
    <Box sx={{ width: ds.space.mul(0, 170) }}>
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

      {relationshipCategories.map((cat, catIdx) => (
        <Box key={cat.category} sx={{ mb: catIdx < relationshipCategories.length - 1 ? ds.space.mul(0, 5) : 0 }}>
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
                key={rel.label}
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
                    fontFamily: 'monospace',
                    whiteSpace: 'nowrap',
                    minWidth: ds.space.mul(0, 65),
                  }}
                >
                  {rel.label}
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

          {catIdx < relationshipCategories.length - 1 && <Divider color='var(--ds-brand-150)' sx={{ mt: 'var(--ds-space-2)' }} />}
        </Box>
      ))}
    </Box>
  );
};

ServiceMapLegends.displayName = 'ServiceMapLegends';

export default ServiceMapLegends;
