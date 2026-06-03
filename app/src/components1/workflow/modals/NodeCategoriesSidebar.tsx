import React, { useState, useMemo } from 'react';
import { Box, Typography, ButtonBase, Accordion, AccordionSummary, AccordionDetails, Tooltip } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import CloseIcon from '@mui/icons-material/Close';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import SearchIcon from '@mui/icons-material/Search';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';

interface SubCategory {
  label: string;
  description: string;
  icon: string;
  aliases?: string[];
  deprecated?: boolean;
  deprecationMessage?: string;
}

interface Category {
  label: string;
  description: string;
  icon: any;
  color: string; // Category color for consistent theming
  subcategories: Record<string, SubCategory>;
}

interface NodeCategoriesSidebarProps {
  open: boolean;
  onClose: () => void;
  categories: Record<string, Category>;
  expandedCategory: string | null;
  onToggleCategory: (categoryKey: string) => void;
  onAddNode: (categoryKey: string, subcategoryKey: string) => void;
}

const matchesAnyAlias = (aliases: string[] | undefined, query: string): boolean => {
  if (!aliases) return false;
  return aliases.some((alias) => alias.toLowerCase().includes(query));
};

const NodeCategoriesSidebar: React.FC<NodeCategoriesSidebarProps> = ({
  open,
  onClose,
  categories,
  expandedCategory,
  onToggleCategory,
  onAddNode,
}) => {
  const [searchQuery, setSearchQuery] = useState('');

  // Filter categories and subcategories based on search query
  const filteredCategories = useMemo(() => {
    if (!searchQuery.trim()) {
      return categories;
    }

    const filtered: Record<string, Category> = {};
    const query = searchQuery.toLowerCase();

    Object.entries(categories).forEach(([categoryKey, category]) => {
      // Check if category name matches
      const categoryMatches = category.label.toLowerCase().includes(query);

      // Filter subcategories that match the search
      const matchingSubcategories: Record<string, SubCategory> = {};

      Object.entries(category.subcategories).forEach(([subKey, subCategory]) => {
        const aliasMatches = matchesAnyAlias(subCategory.aliases, query);
        if (subCategory.label.toLowerCase().includes(query) || aliasMatches || categoryMatches) {
          matchingSubcategories[subKey] = subCategory;
        }
      });

      // Include category if it has matching subcategories or itself matches
      if (Object.keys(matchingSubcategories).length > 0 || categoryMatches) {
        filtered[categoryKey] = {
          ...category,
          subcategories: matchingSubcategories,
        };
      }
    });

    return filtered;
  }, [categories, searchQuery]);

  if (!open) {
    return null;
  }

  return (
    <>
      {/* Centered Popup */}
      <Box
        sx={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          width: '600px',
          minHeight: '80vh',
          maxHeight: '80vh',
          backgroundColor: 'white',
          zIndex: 200,
          boxShadow: '0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 10px 10px -5px rgba(0, 0, 0, 0.04)',
          overflowY: 'auto',
          borderRadius: 'var(--ds-radius-xl)',
          border: '3px solid var(--ds-purple-300)',
        }}
      >
        <Box sx={{ paddingBottom: '0px' }}>
          {/* Header */}
          <Box
            sx={{
              position: 'sticky',
              top: 0,
              zIndex: 1,
              backgroundColor: 'white',
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              padding: 'var(--ds-space-5) var(--ds-space-5)',
              borderBottom: `1px solid ${colors.border.secondaryLight}`,
            }}
          >
            <Typography
              variant='h6'
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                fontFamily: 'poppins',
                color: colors.text.secondary,
                letterSpacing: '-0.025em',
              }}
            >
              Add Node to Automation
            </Typography>
            <Button
              id='wf-node-categories-close-btn'
              composition='icon-only'
              tone='ghost'
              size='sm'
              aria-label='Close'
              icon={<CloseIcon sx={{ fontSize: 'var(--ds-text-heading)', color: 'var(--ds-gray-600)' }} />}
              onClick={onClose}
            />
          </Box>

          {/* Search Bar */}
          <Box sx={{ padding: '0 var(--ds-space-4) var(--ds-space-3) var(--ds-space-4)', mt: 'var(--ds-space-3)' }}>
            <Input
              id='wf-node-categories-search-input'
              size='md'
              placeholder='Search actions...'
              value={searchQuery}
              onChange={setSearchQuery}
              leadingIcon={<SearchIcon sx={{ fontSize: 'var(--ds-text-heading)' }} />}
            />
          </Box>

          {/* Categories */}
          <Box sx={{ padding: '0 var(--ds-space-4) var(--ds-space-3) var(--ds-space-4)' }}>
            {Object.entries(filteredCategories).map(([categoryKey, category]) => (
              <Accordion
                key={categoryKey}
                id={`wf-node-categories-${categoryKey}-accordion`}
                expanded={searchQuery.trim() !== '' ? true : expandedCategory === categoryKey}
                onChange={() => onToggleCategory(categoryKey)}
                elevation={0}
                disableGutters
                sx={{
                  backgroundColor: expandedCategory === categoryKey ? '#EFF6FF' : 'transparent',
                  borderRadius: 'var(--ds-radius-xl)',
                  '&:before': {
                    display: 'none',
                  },
                  '&.Mui-expanded': {
                    margin: '0 0 var(--ds-space-2) 0',
                  },
                }}
              >
                <AccordionSummary
                  expandIcon={<ExpandMoreIcon sx={{ color: 'var(--ds-gray-400)' }} />}
                  sx={{
                    padding: 'var(--ds-space-2) var(--ds-space-4)',
                    backgroundColor: expandedCategory === categoryKey ? 'transparent' : 'white',
                    borderRadius: 'var(--ds-radius-xl)',
                    transition: 'all 0.2s',
                    '&:hover': {
                      backgroundColor: colors.background.primaryLightest,
                    },
                    '&.Mui-expanded': {
                      minHeight: '48px',
                      borderRadius: 'var(--ds-radius-xl)',
                    },
                    '& .MuiAccordionSummary-content': {
                      margin: 0,
                    },
                  }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center' }}>
                    <Box
                      sx={{
                        marginRight: 'var(--ds-space-3)',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        width: '40px',
                        height: '40px',
                        borderRadius: 'var(--ds-radius-lg)',
                        backgroundColor: `color-mix(in srgb, ${category.color} 10%, transparent)`,
                        border: `1px solid color-mix(in srgb, ${category.color} 35%, transparent)`,
                      }}
                    >
                      {typeof category.icon === 'string' && !category.icon.includes('/') && !category.icon.includes('.') ? (
                        // Render emoji as text (doesn't contain path characters)
                        <span style={{ fontSize: 'var(--ds-text-heading)' }}>{category.icon}</span>
                      ) : (
                        // Render as Next.js Image for actual image files
                        <SafeIcon src={category.icon} alt={category.label} width={24} height={24} style={{ objectFit: 'contain' }} />
                      )}
                    </Box>
                    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: '0px' }}>
                      <Typography
                        sx={{
                          fontSize: 'var(--ds-text-body)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: colors.text.secondary,
                          letterSpacing: '-0.015em',
                          fontFamily: 'poppins',
                        }}
                      >
                        {category.label}
                      </Typography>
                      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', textAlign: 'left', lineHeight: '1.3' }}>
                        {category.description}
                      </Typography>
                    </Box>
                  </Box>
                </AccordionSummary>

                <AccordionDetails sx={{ padding: 'var(--ds-space-2) var(--ds-space-4) var(--ds-space-2) var(--ds-space-4)' }}>
                  {Object.entries(category.subcategories).map(([subKey, sub]) => {
                    const row = (
                      <ButtonBase
                        key={subKey}
                        id={`wf-node-categories-action-${categoryKey}-${subKey}-btn`}
                        onClick={() => onAddNode(categoryKey, subKey)}
                        sx={{
                          width: '100%',
                          display: 'flex',
                          alignItems: 'center',
                          padding: 'var(--ds-space-2) var(--ds-space-4)',
                          backgroundColor: 'white',
                          borderRadius: 'var(--ds-radius-lg)',
                          marginBottom: 'var(--ds-space-2)',
                          cursor: 'pointer',
                          transition: 'all 0.2s',
                          justifyContent: 'flex-start',
                          opacity: sub.deprecated ? 0.6 : 1,
                          '&:hover': {
                            backgroundColor: 'var(--ds-background-200)',
                            borderColor: 'var(--ds-gray-200)',
                          },
                        }}
                      >
                        <Box sx={{ marginRight: 'var(--ds-space-3)', display: 'flex', alignItems: 'center', width: '24px', height: '24px' }}>
                          {typeof sub.icon === 'string' && !sub.icon.includes('/') && !sub.icon.includes('.') ? (
                            // Render emoji as text (doesn't contain path characters)
                            <span style={{ fontSize: 'var(--ds-text-title)' }}>{sub.icon}</span>
                          ) : (
                            // Render as Next.js Image for actual image files
                            <SafeIcon src={sub.icon} alt={sub.label} width={20} height={20} style={{ objectFit: 'contain' }} />
                          )}
                        </Box>
                        <Box sx={{ textAlign: 'left', flexGrow: 1 }}>
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-body)',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              color: 'var(--ds-brand-500)',
                              fontFamily: 'poppins',
                            }}
                          >
                            {sub.label}
                          </Typography>
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-caption)',
                              color: 'var(--ds-gray-600)',
                            }}
                          >
                            {sub.description}
                          </Typography>
                        </Box>
                        {sub.deprecated && (
                          <Box
                            sx={{
                              backgroundColor: 'var(--ds-yellow-200)',
                              color: 'var(--ds-amber-700)',
                              borderRadius: 'var(--ds-radius-pill)',
                              padding: 'var(--ds-space-1) var(--ds-space-2)',
                              fontSize: 'var(--ds-text-caption)',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              fontFamily: 'poppins',
                              marginLeft: 'var(--ds-space-2)',
                              flexShrink: 0,
                            }}
                          >
                            Deprecated
                          </Box>
                        )}
                      </ButtonBase>
                    );
                    return sub.deprecated && sub.deprecationMessage ? (
                      <Tooltip key={subKey} title={sub.deprecationMessage} placement='left' arrow>
                        <span>{row}</span>
                      </Tooltip>
                    ) : (
                      row
                    );
                  })}
                </AccordionDetails>
              </Accordion>
            ))}

            {/* Empty State */}
            {Object.keys(filteredCategories).length === 0 && searchQuery.trim() !== '' && (
              <Box
                sx={{
                  textAlign: 'center',
                  padding: 'var(--ds-space-6) var(--ds-space-4)',
                  color: 'var(--ds-gray-600)',
                }}
              >
                <Typography
                  sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', marginBottom: 'var(--ds-space-2)' }}
                >
                  No actions found
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>Try adjusting your search terms</Typography>
              </Box>
            )}
          </Box>
        </Box>
      </Box>
    </>
  );
};

export default NodeCategoriesSidebar;
