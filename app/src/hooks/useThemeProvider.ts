import { useMemo, useEffect, useState } from 'react';
import type { Theme } from '@mui/material';
import { createDynamicTheme } from 'src/theme/createDynamicTheme';
import { DEFAULT_CSS_TOKENS } from 'src/styles/defaultTokens';
import { useBrandingConfig } from './useTenantBranding';

// Server-side only: load branding file color tokens once for SSR critical CSS.
let _ssrBrandingTokens: Record<string, string> | null = null;
let _ssrBrandingLoaded = false;

function loadSSRBrandingTokens(): Record<string, string> | null {
  if (typeof window !== 'undefined') return null;
  if (_ssrBrandingLoaded) return _ssrBrandingTokens;
  _ssrBrandingLoaded = true;

  const filePath = process.env.TENANT_BRANDING_FILE;
  if (!filePath) return null;

  try {
    // Dynamic require to avoid bundling fs into client
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    const fs = require('fs');
    const path = require('path');
    const resolvedPath = filePath.startsWith('/') ? filePath : path.join(process.cwd(), 'public', filePath);
    const raw = fs.readFileSync(resolvedPath, 'utf-8');
    const data = JSON.parse(raw);
    _ssrBrandingTokens = data?.colorTokens || null;
  } catch {
    // Branding file not found or invalid — fall back to defaults
  }
  return _ssrBrandingTokens;
}

/**
 * Hook that provides a dynamic MUI theme and applies CSS variable overrides
 * from the branding config to document.documentElement.
 *
 * Returns { theme, isReady } where isReady is true once branding config has loaded.
 */
export function useThemeProvider(): { theme: Theme; isReady: boolean } {
  const brandingConfig = useBrandingConfig();
  const [cssVarsApplied, setCssVarsApplied] = useState(false);

  // Apply CSS variable overrides to :root
  useEffect(() => {
    if (typeof document === 'undefined') return;

    const colorTokens: Record<string, string> | undefined = (brandingConfig as Record<string, unknown>)?.colorTokens as
      | Record<string, string>
      | undefined;

    if (colorTokens && typeof colorTokens === 'object') {
      const root = document.documentElement;
      for (const [key, value] of Object.entries(colorTokens)) {
        if (key.startsWith('--nb-') && typeof value === 'string') {
          root.style.setProperty(key, value);
        }
      }
    }

    setCssVarsApplied(true);
  }, [brandingConfig]);

  // Build MUI theme from branding config
  const theme = useMemo(() => {
    const themeConfig = (brandingConfig as Record<string, unknown>)?.theme as Record<string, unknown> | undefined;

    if (!themeConfig) return createDynamicTheme();

    return createDynamicTheme({
      palette: themeConfig.palette as { primary?: string; success?: string; error?: string } | undefined,
      typography: themeConfig.typography as { fontFamily?: string } | undefined,
      components: themeConfig.components as { borderRadius?: number } | undefined,
      muiOverrides: themeConfig.muiOverrides as Record<string, unknown> | undefined,
    });
  }, [brandingConfig]);

  return {
    theme,
    isReady: !brandingConfig.loading && cssVarsApplied,
  };
}

/**
 * Generate a CSS string of critical above-the-fold design tokens for SSR.
 * Injected as inline <style> in _document.tsx to prevent FOUC.
 */
export function getCriticalCssTokens(): string {
  // Only include the most commonly used above-the-fold tokens
  const criticalKeys = [
    '--nb-color-primary',
    '--nb-color-white',
    '--nb-color-black',
    '--nb-color-secondary-default',
    '--nb-color-tertiary',
    '--nb-color-success',
    '--nb-color-error',
    '--nb-text-primary',
    '--nb-text-secondary',
    '--nb-text-tertiary',
    '--nb-text-white',
    '--nb-text-black',
    '--nb-text-title',
    '--nb-text-muted',
    '--nb-text-disabled',
    '--nb-bg-primary',
    '--nb-bg-white',
    '--nb-bg-pages',
    '--nb-bg-sidebar',
    '--nb-bg-table-header',
    '--nb-bg-primary-lightest',
    '--nb-btn-primary',
    '--nb-btn-primary-hover',
    '--nb-btn-primary-text',
    '--nb-btn-secondary',
    '--nb-btn-secondary-text',
    '--nb-border-primary',
    '--nb-border-secondary',
    '--nb-border-vertical',
    '--nb-mui-primary',
  ];

  const brandingTokens = loadSSRBrandingTokens();
  const tokens = brandingTokens ? { ...DEFAULT_CSS_TOKENS, ...brandingTokens } : DEFAULT_CSS_TOKENS;

  const declarations = criticalKeys
    .filter((key) => tokens[key])
    .map((key) => `${key}:${tokens[key]}`)
    .join(';');

  return `:root{${declarations}}`;
}
