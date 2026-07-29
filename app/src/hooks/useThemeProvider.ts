import { useMemo, useEffect, useState } from 'react';
import type { Theme } from '@mui/material';
import { createDynamicTheme } from 'src/theme/createDynamicTheme';
import { DEFAULT_CSS_TOKENS } from 'src/styles/defaultTokens';
import { resolveServerBranding } from '@lib/serverBranding';
import { useBrandingConfig } from './useTenantBranding';

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
        // Apply both legacy (--nb-*) and new design-system (--ds-*) tokens.
        // The UI has migrated most call sites to --ds-*, so a branding kit that
        // only set --nb-* would no longer retone the app.
        if ((key.startsWith('--nb-') || key.startsWith('--ds-')) && typeof value === 'string') {
          root.style.setProperty(key, value);
        }
      }
    }

    // Brand font remap: many components hardcode `fontFamily: 'Poppins' | 'Roboto'`
    // inline, so theme/token font changes never reach them. A tenant can supply a
    // `fontRemap` list that re-points those family NAMES at a brand font via
    // injected @font-face rules — every hardcode then renders in the brand font
    // with zero component edits. Default theme has no `fontRemap` ⇒ nothing injected.
    const fontRemap = (brandingConfig as Record<string, unknown>)?.fontRemap as
      | Array<{ family: string; src: string; weight?: string; style?: string }>
      | undefined;
    const STYLE_ID = 'tenant-font-remap';
    const existing = document.getElementById(STYLE_ID);
    // Reject any value containing characters that could break out of the
    // @font-face declaration and inject arbitrary CSS (defacement / data
    // exfiltration via background images). None of these values legitimately
    // contain { } ; " or backslash, so we forbid those outright.
    const isSafeFontValue = (v: string) => !/[{};"\\]/.test(v);
    // family is emitted INSIDE single quotes (font-family:'X'), so a stray single
    // quote would close the string literal and let the rest of the value inject
    // arbitrary declarations. src is NOT single-quoted (it is a url(...) format(...)
    // expression that legitimately contains single quotes), so it keeps the looser
    // isSafeFontValue. weight/style are unquoted too, but a single quote is never
    // valid in them, so we forbid it as cheap defense-in-depth.
    const isSafeStringValue = (v: string) => isSafeFontValue(v) && !/'/.test(v);
    if (Array.isArray(fontRemap) && fontRemap.length > 0) {
      const css = fontRemap
        .filter(
          (f) =>
            f &&
            typeof f.family === 'string' &&
            isSafeStringValue(f.family) &&
            typeof f.src === 'string' &&
            isSafeFontValue(f.src) &&
            (f.weight == null || (typeof f.weight === 'string' && isSafeStringValue(f.weight))) &&
            (f.style == null || (typeof f.style === 'string' && isSafeStringValue(f.style)))
        )
        .map(
          (f) =>
            `@font-face{font-family:'${f.family}';src:${f.src};font-weight:${f.weight || '100 900'};` +
            `font-style:${f.style || 'normal'};font-display:swap;}`
        )
        .join('');
      if (existing) existing.textContent = css;
      else {
        const el = document.createElement('style');
        el.id = STYLE_ID;
        el.textContent = css;
        document.head.appendChild(el);
      }
    } else if (existing) {
      // Branding switched to a kit without a remap — remove the stale override.
      existing.remove();
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
    // New design-system (--ds-*) tokens — the families most call sites read for
    // above-the-fold chrome (primary buttons, sidebar, surfaces, base text/status).
    // Keeps a branded deploy from flashing the default palette before hydration.
    '--ds-brand-100',
    '--ds-brand-200',
    '--ds-brand-300',
    '--ds-brand-500',
    '--ds-brand-600',
    '--ds-brand-700',
    '--ds-foreground',
    '--ds-background-100',
    '--ds-background-200',
    '--ds-background-300',
    '--ds-gray-100',
    '--ds-gray-300',
    '--ds-gray-600',
    '--ds-gray-700',
    '--ds-blue-100',
    '--ds-blue-500',
    '--ds-blue-600',
    '--ds-blue-700',
    '--ds-red-100',
    '--ds-red-500',
    '--ds-red-600',
    '--ds-green-100',
    '--ds-green-500',
  ];

  // SSR branding tokens come from the optional server branding provider
  // (EE-only). In OSS this is null, so critical CSS is the neutral defaults.
  const brandingTokens = resolveServerBranding()?.colorTokens || null;
  const tokens = brandingTokens ? { ...DEFAULT_CSS_TOKENS, ...brandingTokens } : DEFAULT_CSS_TOKENS;

  const declarations = criticalKeys
    .filter((key) => tokens[key])
    .map((key) => `${key}:${tokens[key]}`)
    .join(';');

  return `:root{${declarations}}`;
}
