import { createTheme, type ThemeOptions, type Theme } from '@mui/material';

export interface TenantThemeConfig {
  palette?: {
    primary?: string;
    success?: string;
    error?: string;
  };
  typography?: {
    fontFamily?: string;
  };
  components?: {
    borderRadius?: number;
  };
  muiOverrides?: Partial<ThemeOptions['components']>;
}

export function createDynamicTheme(config?: TenantThemeConfig): Theme {
  // MUI palette requires actual color values (not CSS vars) to compute light/dark variants
  const primary = config?.palette?.primary || '#27429C';
  const success = config?.palette?.success || '#00C853';
  const error = config?.palette?.error || '#FF1744';
  const fontFamily = config?.typography?.fontFamily || ['Roboto', 'Poppins', 'sans-serif'].join(',');

  const baseTheme: ThemeOptions = {
    palette: {
      primary: {
        main: primary,
      },
      success: {
        main: success,
        contrastText: 'var(--ds-background-100)',
      },
      error: {
        main: error,
        contrastText: 'var(--ds-background-100)',
      },
    },
    typography: {
      fontFamily,
    },
    components: {
      MuiContainer: {
        styleOverrides: {
          root: {
            paddingInline: 20,
            '@media (min-width:600px)': {
              paddingInline: 20,
            },
          },
        },
      },
      MuiTooltip: {
        styleOverrides: {
          tooltip: {
            backgroundColor: 'rgba(0, 0, 0, 0.85)',
            color: 'var(--ds-gray-200)',
            fontWeight: 'var(--ds-font-weight-regular)',
            padding: 'var(--ds-space-2) var(--ds-space-3)',
            lineHeight: '16px',
            fontSize: 'var(--ds-text-small)',
            maxWidth: '520px',
            borderRadius: 'var(--ds-radius-lg)',
            '&.large-tooltip': {
              fontSize: 'var(--ds-text-body-lg)',
              lineHeight: '18px',
            },
            '& .MuiChip-root': {
              backgroundColor: 'rgba(255, 255, 255, 0.1)',
              color: 'var(--ds-background-100)',
              '& .MuiChip-label': {
                color: 'inherit',
              },
              '& .MuiChip-deleteIcon': {
                color: 'rgba(255, 255, 255, 0.7)',
                '&:hover': {
                  color: 'var(--ds-background-100)',
                },
              },
            },
          },
          arrow: {
            color: 'rgba(0, 0, 0, 0.85)',
          },
        },
      },
      MuiPagination: {
        styleOverrides: {
          root: {
            '& .MuiPaginationItem-root': {
              borderRadius: config?.components?.borderRadius ?? 10,
            },
          },
        },
      },
      MuiMenu: {
        styleOverrides: {
          root: {
            maxHeight: '365px',
          },
          paper: {
            boxShadow: '0px 4px 12px rgba(0, 0, 0, 0.15)',
          },
        },
      },
      MuiFormControlLabel: {
        styleOverrides: {
          root: {
            '& .MuiFormControlLabel-label': {
              fontSize: 'var(--ds-text-body-lg)',
              color: 'var(--nb-mui-form-label)',
            },
            '& .MuiRadio-root.Mui-checked + .MuiFormControlLabel-label': {
              color: 'var(--nb-mui-form-label-checked)',
              fontWeight: 'var(--ds-font-weight-medium)',
            },
          },
        },
      },
      MuiRadio: {
        styleOverrides: {
          root: {
            fontSize: 'var(--ds-text-body-lg)',
            color: 'var(--nb-mui-radio-unchecked)',
            '&.Mui-checked': {
              color: 'var(--nb-mui-radio-checked)',
            },
            '& svg': {
              width: '18px',
              height: '18px',
            },
          },
        },
      },
      MuiButton: {
        styleOverrides: {
          containedPrimary: {
            backgroundColor: 'var(--nb-btn-primary)',
            color: 'var(--nb-btn-primary-text)',
            '&:hover': {
              backgroundColor: 'var(--nb-btn-primary-hover)',
            },
            '&.Mui-disabled': {
              backgroundColor: 'var(--nb-btn-primary-disabled)',
              color: 'var(--nb-btn-primary-disabled-text)',
            },
          },
          outlinedSecondary: {
            color: 'var(--nb-btn-secondary-text)',
            borderColor: 'var(--nb-btn-secondary-border)',
            '&:hover': {
              backgroundColor: 'var(--nb-btn-secondary-hover)',
              borderColor: 'var(--nb-btn-secondary-hover-border)',
            },
          },
        },
      },
      MuiCard: {
        styleOverrides: {
          root: {
            boxShadow: 'var(--nb-shadow-card)',
            borderRadius: config?.components?.borderRadius ?? 12,
          },
        },
      },
      MuiDialog: {
        styleOverrides: {
          paper: {
            borderRadius: config?.components?.borderRadius ?? 12,
          },
        },
      },
      MuiChip: {
        styleOverrides: {
          root: {
            fontWeight: 'var(--ds-font-weight-medium)',
            fontSize: 'var(--ds-text-small)',
          },
        },
      },
      ...config?.muiOverrides,
    },
  };

  return createTheme(baseTheme);
}
