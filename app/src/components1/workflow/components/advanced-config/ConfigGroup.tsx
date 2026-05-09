import React from 'react';
import { Box, Typography, Collapse, IconButton } from '@mui/material';
import { ExpandMore } from '@mui/icons-material';
import { colors } from 'src/utils/colors';

interface ConfigGroupProps {
  title: string;
  icon?: React.ReactNode;
  children: React.ReactNode;
  defaultExpanded?: boolean;
  hasConfigured?: boolean;
  id?: string;
}

const ConfigGroup: React.FC<ConfigGroupProps> = ({ title, icon, children, defaultExpanded = true, hasConfigured = false, id }) => {
  const [expanded, setExpanded] = React.useState(defaultExpanded);

  return (
    <Box id={id} sx={{ mb: 2, scrollMarginTop: '16px' }}>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          cursor: 'pointer',
          py: 0.5,
          '&:hover': {
            opacity: 0.8,
          },
        }}
        onClick={() => setExpanded(!expanded)}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {icon}
          <Typography
            sx={{
              fontSize: '12px',
              fontWeight: 600,
              color: colors.text.secondary,
              textTransform: 'uppercase',
              letterSpacing: '0.5px',
            }}
          >
            {title}
          </Typography>
          {hasConfigured && (
            <Box
              sx={{
                width: 6,
                height: 6,
                borderRadius: '50%',
                bgcolor: 'primary.main',
              }}
            />
          )}
        </Box>
        <IconButton
          size='small'
          sx={{
            transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)',
            transition: 'transform 0.2s',
            p: 0.25,
          }}
        >
          <ExpandMore sx={{ fontSize: 16 }} />
        </IconButton>
      </Box>
      <Collapse in={expanded}>
        <Box sx={{ pt: 1.5, display: 'flex', flexDirection: 'column', gap: 2 }}>{children}</Box>
      </Collapse>
    </Box>
  );
};

export default ConfigGroup;
