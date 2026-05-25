import { colors } from 'src/utils/colors';
import { Box, Typography, Divider } from '@mui/material';

const ServiceMapLegends = ({ mode = 'service_map' }) => {
  // 1. Existing Visual Legends (Colors & Line Types)
  const visualLegends = [
    {
      id: colors.text.serviceMapGreen,
      description: 'Node is from different selected namespace.',
      type: 'square',
    },
    {
      id: colors.text.serviceMapSkyblue,
      description: 'Node is from same selected namespaces.',
      type: 'square',
    },
    {
      id: colors.text.red,
      description: 'Error state between two nodes.',
      type: 'line',
    },
    {
      id: colors.text.black,
      description: 'Standard connection.',
      type: 'line',
    },
    {
      id: colors.text.blue,
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
          backgroundColor: colors.background.white,
          borderRadius: '8px',
          padding: '16px',
          boxShadow: '0px 4px 12px rgba(0, 0, 0, 0.1)',
          maxWidth: '350px',
          marginTop: '10px',
          float: 'right',
          fontSize: '12px',
        }}
      >
        <Typography variant='subtitle2' sx={{ fontWeight: 'bold', mb: 1, color: '#555' }}>
          Visual Keys
        </Typography>
        {visualLegends.map((item, index) => (
          <div
            key={`${index}-${item.id}`}
            style={{
              display: 'flex',
              alignItems: 'center',
              marginBottom: '8px',
            }}
          >
            {item.type === 'square' ? (
              <div
                style={{
                  width: '16px',
                  height: '16px',
                  borderRadius: '3px',
                  backgroundColor: 'white',
                  border: `2px solid ${item.id}`,
                  marginRight: '10px',
                  flexShrink: 0,
                }}
              />
            ) : (
              <div
                style={{
                  width: '20px',
                  height: '0px',
                  borderBottom: `2px dashed ${item.id}`,
                  marginRight: '10px',
                  flexShrink: 0,
                }}
              />
            )}
            <span style={{ color: colors.text.primary, lineHeight: 1.2 }}>{item.description}</span>
          </div>
        ))}
      </aside>
    );
  }

  return (
    <Box sx={{ width: '340px' }}>
      <Typography
        variant='body2'
        sx={{
          fontWeight: 600,
          fontSize: '13px',
          color: colors.text.secondary,
          mb: '2px',
        }}
      >
        Relationship Types
      </Typography>
      <Typography
        variant='caption'
        sx={{
          color: '#64748B',
          fontSize: '11px',
          lineHeight: 1.3,
          display: 'block',
          mb: '12px',
        }}
      >
        How resources in your infrastructure are connected to each other.
      </Typography>

      {relationshipCategories.map((cat, catIdx) => (
        <Box key={cat.category} sx={{ mb: catIdx < relationshipCategories.length - 1 ? '10px' : 0 }}>
          <Typography
            variant='caption'
            sx={{
              fontWeight: 600,
              fontSize: '11px',
              color: '#94A3B8',
              textTransform: 'uppercase',
              letterSpacing: '0.5px',
              display: 'block',
              mb: '6px',
            }}
          >
            {cat.category}
          </Typography>

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
            {cat.items.map((rel) => (
              <Box
                key={rel.label}
                sx={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: '8px',
                  py: '2px',
                }}
              >
                <Typography
                  variant='caption'
                  sx={{
                    fontWeight: 600,
                    fontSize: '11px',
                    color: colors.text.secondary,
                    fontFamily: 'monospace',
                    whiteSpace: 'nowrap',
                    minWidth: '130px',
                  }}
                >
                  {rel.label}
                </Typography>
                <Typography
                  variant='caption'
                  sx={{
                    fontSize: '11px',
                    color: '#64748B',
                    lineHeight: 1.3,
                  }}
                >
                  {rel.desc}
                </Typography>
              </Box>
            ))}
          </Box>

          {catIdx < relationshipCategories.length - 1 && <Divider sx={{ mt: '10px', borderColor: '#E2E8F0' }} />}
        </Box>
      ))}
    </Box>
  );
};

ServiceMapLegends.displayName = 'ServiceMapLegends';

export default ServiceMapLegends;
