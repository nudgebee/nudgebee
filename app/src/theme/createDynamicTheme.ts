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
        contrastText: '#ffffff',
      },
      error: {
        main: error,
        contrastText: '#ffffff',
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
            color: '#EEEEEE',
            fontWeight: 400,
            padding: '8px 12px',
            lineHeight: '16px',
            fontSize: '12px',
            maxWidth: '520px',
            borderRadius: '8px',
            '&.large-tooltip': {
              fontSize: '14px',
              lineHeight: '18px',
            },
            '& .MuiChip-root': {
              backgroundColor: 'rgba(255, 255, 255, 0.1)',
              color: '#FFFFFF',
              '& .MuiChip-label': {
                color: 'inherit',
              },
              '& .MuiChip-deleteIcon': {
                color: 'rgba(255, 255, 255, 0.7)',
                '&:hover': {
                  color: '#FFFFFF',
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
              fontSize: '14px',
              color: 'var(--nb-mui-form-label)',
            },
            '& .MuiRadio-root.Mui-checked + .MuiFormControlLabel-label': {
              color: 'var(--nb-mui-form-label-checked)',
              fontWeight: 500,
            },
          },
        },
      },
      MuiRadio: {
        styleOverrides: {
          root: {
            fontSize: '14px',
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
            fontWeight: 500,
            fontSize: '12px',
          },
        },
      },
      ...config?.muiOverrides,
    },
  };

  return createTheme(baseTheme);
}
