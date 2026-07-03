import React, { useState } from 'react';
import { Box, Typography, Collapse, Badge, Tooltip, Chip } from '@mui/material';
import { ExpandMore, Settings, Timer, Storage, GridView, ErrorOutline } from '@mui/icons-material';

// Quick navigation sections configuration
const QUICK_NAV_SECTIONS = [
  { id: 'execution-control', label: 'Execution', icon: <Timer sx={{ fontSize: 'var(--ds-text-small)' }} /> },
  { id: 'data-management', label: 'Data', icon: <Storage sx={{ fontSize: 'var(--ds-text-small)' }} /> },
  { id: 'parallel-execution', label: 'Parallel', icon: <GridView sx={{ fontSize: 'var(--ds-text-small)' }} /> },
  { id: 'error-handling', label: 'Errors', icon: <ErrorOutline sx={{ fontSize: 'var(--ds-text-small)' }} /> },
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
        border: `1px solid var(--ds-green-200)`,
        borderRadius: 'var(--ds-radius-sm)',
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
          bgcolor: expanded ? 'var(--ds-gray-100)' : 'transparent',
          cursor: 'pointer',
          '&:hover': {
            bgcolor: 'var(--ds-gray-100)',
          },
          transition: 'background-color 0.2s',
        }}
        onClick={handleToggle}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {icon || <Settings sx={{ fontSize: 18, color: 'var(--ds-brand-500)' }} />}
          <Typography
            variant='subtitle2'
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-brand-500)',
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
                  ml: 'var(--ds-space-2)',
                  '& .MuiBadge-badge': {
                    fontSize: 'var(--ds-text-caption)',
                    height: 16,
                    minWidth: 16,
                  },
                }}
              />
            </Tooltip>
          )}
        </Box>
        <Box
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)',
            transition: 'transform 0.3s',
            color: 'var(--ds-brand-500)',
          }}
        >
          <ExpandMore fontSize='small' />
        </Box>
      </Box>

      {/* Description (shown when collapsed) */}
      {!expanded && description && (
        <Box sx={{ px: 2, pb: 1.5 }}>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-brand-500)', opacity: 0.7 }}>{description}</Typography>
        </Box>
      )}

      {/* Content */}
      <Collapse in={expanded}>
        <Box
          sx={{
            borderTop: `1px solid var(--ds-green-200)`,
          }}
        >
          {/* Quick Navigation */}
          {showQuickNav && (
            <Box
              sx={{
                px: 2,
                py: 1,
                bgcolor: 'var(--ds-background-200)',
                borderBottom: `1px solid var(--ds-green-200)`,
                display: 'flex',
                alignItems: 'center',
                gap: 0.5,
                flexWrap: 'wrap',
              }}
            >
              <Typography
                sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-brand-500)', mr: 0.5, fontWeight: 'var(--ds-font-weight-medium)' }}
              >
                Jump to:
              </Typography>
              {QUICK_NAV_SECTIONS.map((section) => (
                <Tooltip key={section.id} title={`Go to ${section.label}`}>
                  <Chip
                    size='small'
                    icon={section.icon}
                    label={section.label}
                    onClick={() => handleQuickNavClick(section.id)}
                    sx={{
                      height: 22,
                      fontSize: 'var(--ds-text-caption)',
                      bgcolor: 'white',
                      border: `1px solid var(--ds-green-200)`,
                      '&:hover': {
                        bgcolor: 'primary.light',
                        color: 'primary.contrastText',
                        '& .MuiChip-icon': {
                          color: 'primary.contrastText',
                        },
                      },
                      '& .MuiChip-icon': {
                        color: 'var(--ds-brand-500)',
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
