import React, { useState, useMemo } from 'react';
import {
  Box,
  Typography,
  IconButton,
  Button,
  Accordion,
  AccordionSummary,
  AccordionDetails,
  TextField,
  InputAdornment,
  Tooltip,
} from '@mui/material';
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
          borderRadius: '12px',
          border: '3px solid #C5AFFF',
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
              padding: '22px 24px',
              borderBottom: `1px solid ${colors.border.secondaryLight}`,
            }}
          >
            <Typography
              variant='h6'
              sx={{
                fontSize: '18px',
                fontWeight: 600,
                fontFamily: 'poppins',
                color: colors.text.secondary,
                letterSpacing: '-0.025em',
              }}
            >
              Add Node to Automation
            </Typography>
            <IconButton
              onClick={onClose}
              sx={{
                color: '#6b7280',
                padding: '4px',
              }}
            >
              <CloseIcon sx={{ fontSize: '20px' }} />
            </IconButton>
          </Box>

          {/* Search Bar */}
          <Box sx={{ padding: '0 16px 12px 16px', mt: '12px' }}>
            <TextField
              fullWidth
              placeholder='Search actions...'
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              InputProps={{
                startAdornment: (
                  <InputAdornment position='start'>
                    <SearchIcon sx={{ color: '#9ca3af', fontSize: '20px' }} />
                  </InputAdornment>
                ),
              }}
              sx={{
                '& .MuiOutlinedInput-root': {
                  borderRadius: '8px',
                  backgroundColor: '#f9fafb',
                  fontSize: '14px',
                  '& fieldset': {
                    borderColor: '#e5e7eb',
                  },
                  '&:hover fieldset': {
                    borderColor: '#d1d5db',
                  },
                  '&.Mui-focused fieldset': {
                    borderColor: colors.border.primary,
                    borderWidth: '2px',
                  },
                },
                '& .MuiInputBase-input': {
                  padding: '10px 14px',
                  fontSize: '14px',
                  '&::placeholder': {
                    color: '#9ca3af',
                    opacity: 1,
                  },
                },
              }}
            />
          </Box>

          {/* Categories */}
          <Box sx={{ padding: '0 16px 12px 16px' }}>
            {Object.entries(filteredCategories).map(([categoryKey, category]) => (
              <Accordion
                key={categoryKey}
                expanded={searchQuery.trim() !== '' ? true : expandedCategory === categoryKey}
                onChange={() => onToggleCategory(categoryKey)}
                elevation={0}
                disableGutters
                sx={{
                  backgroundColor: expandedCategory === categoryKey ? '#EFF6FF' : 'transparent',
                  borderRadius: '12px',
                  '&:before': {
                    display: 'none',
                  },
                  '&.Mui-expanded': {
                    margin: '0 0 8px 0',
                  },
                }}
              >
                <AccordionSummary
                  expandIcon={<ExpandMoreIcon sx={{ color: '#9ca3af' }} />}
                  sx={{
                    padding: '8px 16px',
                    backgroundColor: expandedCategory === categoryKey ? 'transparent' : 'white',
                    borderRadius: '12px',
                    transition: 'all 0.2s',
                    '&:hover': {
                      backgroundColor: colors.background.primaryLightest,
                    },
                    '&.Mui-expanded': {
                      minHeight: '48px',
                      borderRadius: '12px',
                    },
                    '& .MuiAccordionSummary-content': {
                      margin: 0,
                    },
                  }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center' }}>
                    <Box
                      sx={{
                        marginRight: '14px',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        width: '40px',
                        height: '40px',
                        borderRadius: '10px',
                        backgroundColor: `${category.color}10`,
                        border: `1px solid ${category.color}35`,
                      }}
                    >
                      {typeof category.icon === 'string' && !category.icon.includes('/') && !category.icon.includes('.') ? (
                        // Render emoji as text (doesn't contain path characters)
                        <span style={{ fontSize: '20px' }}>{category.icon}</span>
                      ) : (
                        // Render as Next.js Image for actual image files
                        <SafeIcon src={category.icon} alt={category.label} width={24} height={24} style={{ objectFit: 'contain' }} />
                      )}
                    </Box>
                    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: '0px' }}>
                      <Typography
                        sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, letterSpacing: '-0.015em', fontFamily: 'poppins' }}
                      >
                        {category.label}
                      </Typography>
                      <Typography sx={{ fontSize: '11px', color: '#6b7280', textAlign: 'left', lineHeight: '1.3' }}>
                        {category.description}
                      </Typography>
                    </Box>
                  </Box>
                </AccordionSummary>

                <AccordionDetails sx={{ padding: '8px 16px 8px 16px' }}>
                  {Object.entries(category.subcategories).map(([subKey, sub]) => {
                    const row = (
                      <Button
                        key={subKey}
                        onClick={() => onAddNode(categoryKey, subKey)}
                        fullWidth
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          padding: '8px 16px',
                          backgroundColor: 'white',
                          borderRadius: '8px',
                          marginBottom: '8px',
                          cursor: 'pointer',
                          transition: 'all 0.2s',
                          textTransform: 'none',
                          justifyContent: 'flex-start',
                          opacity: sub.deprecated ? 0.6 : 1,
                          '&:hover': {
                            backgroundColor: '#f8f9fa',
                            borderColor: '#e9ecef',
                          },
                        }}
                      >
                        <Box sx={{ marginRight: '12px', display: 'flex', alignItems: 'center', width: '24px', height: '24px' }}>
                          {typeof sub.icon === 'string' && !sub.icon.includes('/') && !sub.icon.includes('.') ? (
                            // Render emoji as text (doesn't contain path characters)
                            <span style={{ fontSize: '16px' }}>{sub.icon}</span>
                          ) : (
                            // Render as Next.js Image for actual image files
                            <SafeIcon src={sub.icon} alt={sub.label} width={20} height={20} style={{ objectFit: 'contain' }} />
                          )}
                        </Box>
                        <Box sx={{ textAlign: 'left', flexGrow: 1 }}>
                          <Typography
                            sx={{
                              fontSize: '13px',
                              fontWeight: 600,
                              color: '#374151',
                              fontFamily: 'poppins',
                            }}
                          >
                            {sub.label}
                          </Typography>
                          <Typography
                            sx={{
                              fontSize: '11px',
                              color: '#6b7280',
                            }}
                          >
                            {sub.description}
                          </Typography>
                        </Box>
                        {sub.deprecated && (
                          <Box
                            sx={{
                              backgroundColor: '#FEF3C7',
                              color: '#92400E',
                              borderRadius: '999px',
                              padding: '2px 8px',
                              fontSize: '10px',
                              fontWeight: 600,
                              fontFamily: 'poppins',
                              marginLeft: '8px',
                              flexShrink: 0,
                            }}
                          >
                            Deprecated
                          </Box>
                        )}
                      </Button>
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
                  padding: '40px 20px',
                  color: '#6b7280',
                }}
              >
                <Typography sx={{ fontSize: '14px', fontWeight: 500, marginBottom: '8px' }}>No actions found</Typography>
                <Typography sx={{ fontSize: '12px' }}>Try adjusting your search terms</Typography>
              </Box>
            )}
          </Box>
        </Box>
      </Box>
    </>
  );
};

export default NodeCategoriesSidebar;
