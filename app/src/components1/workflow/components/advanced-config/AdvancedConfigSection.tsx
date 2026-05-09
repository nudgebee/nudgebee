import React, { useState } from 'react';
import { Box, Typography, IconButton, Collapse, Badge, Tooltip, Chip } from '@mui/material';
import { ExpandMore, Settings, Timer, Storage, GridView, ErrorOutline } from '@mui/icons-material';
import { colors } from 'src/utils/colors';

// Quick navigation sections configuration
const QUICK_NAV_SECTIONS = [
  { id: 'execution-control', label: 'Execution', icon: <Timer sx={{ fontSize: 12 }} /> },
  { id: 'data-management', label: 'Data', icon: <Storage sx={{ fontSize: 12 }} /> },
  { id: 'parallel-execution', label: 'Parallel', icon: <GridView sx={{ fontSize: 12 }} /> },
  { id: 'error-handling', label: 'Errors', icon: <ErrorOutline sx={{ fontSize: 12 }} /> },
];

interface AdvancedConfigSectionProps {
  title: string;
  children: React.ReactNode;
  configuredCount?: number;
  onExpandChange?: (expanded: boolean) => void;
  icon?: React.ReactNode;
  description?: string;
  showQuickNav?: boolean;
}

const AdvancedConfigSection: React.FC<AdvancedConfigSectionProps> = ({
  title,
  children,
  configuredCount = 0,
  onExpandChange,
  icon,
  description,
  showQuickNav = true,
}) => {
  const [expanded, setExpanded] = useState(configuredCount > 0);

  const handleToggle = () => {
    const newExpanded = !expanded;
    setExpanded(newExpanded);
    onExpandChange?.(newExpanded);
  };

  const handleQuickNavClick = (sectionId: string) => {
    const element = document.getElementById(sectionId);
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  };

  return (
    <Box
      sx={{
        border: `1px solid ${colors.lowestLight}`,
        borderRadius: 1,
        overflow: 'hidden',
      }}
    >
      {/* Header */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          px: 2,
          py: 1.5,
          bgcolor: expanded ? colors.background.tertiaryLightest : 'transparent',
          cursor: 'pointer',
          '&:hover': {
            bgcolor: colors.background.tertiaryLightest,
          },
          transition: 'background-color 0.2s',
        }}
        onClick={handleToggle}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {icon || <Settings sx={{ fontSize: 18, color: colors.text.secondary }} />}
          <Typography
            variant='subtitle2'
            sx={{
              fontSize: '14px',
              fontWeight: 600,
              color: colors.text.secondary,
            }}
          >
            {title}
          </Typography>
          {configuredCount > 0 && (
            <Tooltip title={`${configuredCount} field(s) configured`}>
              <Badge
                badgeContent={configuredCount}
                color='primary'
                sx={{
                  ml: '8px',
                  '& .MuiBadge-badge': {
                    fontSize: '10px',
                    height: 16,
                    minWidth: 16,
                  },
                }}
              />
            </Tooltip>
          )}
        </Box>
        <IconButton
          size='small'
          sx={{
            transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)',
            transition: 'transform 0.3s',
          }}
        >
          <ExpandMore fontSize='small' />
        </IconButton>
      </Box>

      {/* Description (shown when collapsed) */}
      {!expanded && description && (
        <Box sx={{ px: 2, pb: 1.5 }}>
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, opacity: 0.7 }}>{description}</Typography>
        </Box>
      )}

      {/* Content */}
      <Collapse in={expanded}>
        <Box
          sx={{
            borderTop: `1px solid ${colors.lowestLight}`,
          }}
        >
          {/* Quick Navigation */}
          {showQuickNav && (
            <Box
              sx={{
                px: 2,
                py: 1,
                bgcolor: '#f8f9fa',
                borderBottom: `1px solid ${colors.lowestLight}`,
                display: 'flex',
                alignItems: 'center',
                gap: 0.5,
                flexWrap: 'wrap',
              }}
            >
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, mr: 0.5, fontWeight: 500 }}>Jump to:</Typography>
              {QUICK_NAV_SECTIONS.map((section) => (
                <Tooltip key={section.id} title={`Go to ${section.label}`}>
                  <Chip
                    size='small'
                    icon={section.icon}
                    label={section.label}
                    onClick={() => handleQuickNavClick(section.id)}
                    sx={{
                      height: 22,
                      fontSize: '10px',
                      bgcolor: 'white',
                      border: `1px solid ${colors.lowestLight}`,
                      '&:hover': {
                        bgcolor: 'primary.light',
                        color: 'primary.contrastText',
                        '& .MuiChip-icon': {
                          color: 'primary.contrastText',
                        },
                      },
                      '& .MuiChip-icon': {
                        color: colors.text.secondary,
                      },
                    }}
                  />
                </Tooltip>
              ))}
            </Box>
          )}

          {/* Main Content */}
          <Box sx={{ px: 2, py: 2 }}>{children}</Box>
        </Box>
      </Collapse>
    </Box>
  );
};

export default AdvancedConfigSection;
