import { renderHook, act } from '@testing-library/react';

import { getTenantKey } from '@hooks/useTenantBranding';

jest.mock('@lib/auth', () => ({
  getUserSession: jest.fn(() => null),
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: jest.fn((s) => s.replace(/_/g, ' ')),
}));

// Mock fetch for /api/public/app_config
global.fetch = jest.fn();

describe('getTenantKey', () => {
  it('converts tenant name to snake_case key', () => {
    expect(getTenantKey('Acme Corp')).toBe('acme_corp');
  });

  it('returns empty string for empty input', () => {
    expect(getTenantKey('')).toBe('');
    expect(getTenantKey(null)).toBe('');
    expect(getTenantKey(undefined)).toBe('');
  });

  it('strips leading and trailing underscores', () => {
    expect(getTenantKey('  Foo Bar  ')).toBe('foo_bar');
  });

  it('handles special characters', () => {
    expect(getTenantKey('Nudgebee!')).toBe('nudgebee');
  });

  it('handles single-word names', () => {
    expect(getTenantKey('Nudgebee')).toBe('nudgebee');
  });
});

// The branding config is the client's ONLY brand source, and the window before
// it answers is a visible server round trip. These lock the rule that the window
// resolves to EMPTY rather than to the Nudgebee defaults: a partner deployment
// showing "Nudgebee" in the tab and then repainting to its own name is the
// regression this covers.
describe('branding before /api/public/app_config resolves', () => {
  const React = require('react');
  let releaseConfig;

  const loadBranding = (payload) => {
    let release;
    global.fetch = jest.fn(
      () =>
        new Promise((resolveFetch) => {
          release = () => resolveFetch({ json: () => Promise.resolve(payload) });
        })
    );
    // A fresh module registry per case, so the hook's module-level cache and its
    // eager fetch start cold. React is pinned back to the instance Testing
    // Library already holds — a second React instance would leave
    // useSyncExternalStore rendering against a null dispatcher.
    jest.resetModules();
    jest.doMock('react', () => React);
    const mod = require('@hooks/useTenantBranding');
    releaseConfig = async () => {
      release();
      // Two hops: fetch -> .json() -> the .then that fills the cache.
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    };
    return mod;
  };

  it('resolves every brand-visible field to empty while the fetch is in flight', () => {
    const branding = loadBranding({ title: 'Acme' });

    expect(branding.getBrandTitle()).toBe('');
    expect(branding.getAssistantName()).toBe('');
    expect(branding.getNubiIconUrl()).toBe('');
    expect(branding.getNubiIconLightUrl()).toBe('');
    expect(branding.getNubiIconCircleUrl()).toBe('');
    expect(branding.getBrandingAsset('helpbeeIcon')).toBe('');
  });

  it('uses the resolved config once it lands', async () => {
    const branding = loadBranding({ title: 'Acme', assistantName: 'ACE', nubiIconUrl: '/branding/acme/ace-icon-64.svg' });
    await releaseConfig();

    expect(branding.getBrandTitle()).toBe('Acme');
    expect(branding.getAssistantName()).toBe('ACE');
    expect(branding.getNubiIconUrl()).toBe('/branding/acme/ace-icon-64.svg');
  });

  it('falls back to the defaults per-field once resolved, for fields the config omits', async () => {
    // OSS and EE-without-a-branding-file both answer with no branding fields.
    const branding = loadBranding({ watchEnabled: false });
    await releaseConfig();

    expect(branding.getBrandTitle()).toBe('Nudgebee');
    expect(branding.getAssistantName()).toBe('nubi');
    expect(branding.getNubiIconUrl()).toBe('/branding/default/nubi-icon.svg');
    expect(branding.getBrandingAsset('helpbeeIcon')).toBe('/branding/default/helpbee-icon.svg');
  });

  it('never hands a white-label deployment a Nudgebee asset', async () => {
    const branding = loadBranding({
      isWhiteLabel: true,
      helpbeeIconUrl: '/branding/acme/ace-icon-64.svg',
      // k8sBeeUrl deliberately absent — the partner kit does not ship one.
    });
    await releaseConfig();

    // Set slot: the partner URL alone, with no Nudgebee `fallbackSrc` for
    // SafeIcon to swap in when the partner file 404s.
    expect(branding.getBrandingAsset('helpbeeIcon')).toBe('/branding/acme/ace-icon-64.svg');
    // Unset slot: nothing, rather than the house mascot.
    expect(branding.getBrandingAsset('k8sBee')).toBe('');
  });

  it('still supplies the Nudgebee asset and its fallback when not white-label', async () => {
    const branding = loadBranding({ isWhiteLabel: false, helpbeeIconUrl: '/branding/custom/help.svg' });
    await releaseConfig();

    expect(branding.getBrandingAsset('helpbeeIcon')).toEqual({
      src: '/branding/custom/help.svg',
      fallbackSrc: '/branding/default/helpbee-icon.svg',
    });
    expect(branding.getBrandingAsset('k8sBee')).toBe('/branding/default/k8s-bee.svg');
  });

  // The house kit names the home-panel mascots explicitly, so its URLs win over
  // the defaults in BRANDING_ASSETS. It has drifted twice — first to deleted
  // PNGs, then back to the retired bee SVGs — so pin it to the current Nubi art.
  it('ships the current Nubi art for every home-panel mascot in the house kit', async () => {
    const branding = loadBranding(require('../../public/branding/default/theme.json'));
    await releaseConfig();

    const srcOf = (asset) => (typeof asset === 'string' ? asset : asset.src);
    expect(srcOf(branding.getBrandingAsset('troubleshootBee'))).toBe('/branding/default/nubi-investigating.svg');
    expect(srcOf(branding.getBrandingAsset('optimizeBee'))).toBe('/branding/default/nubi-searching.svg');
    expect(srcOf(branding.getBrandingAsset('securityBee'))).toBe('/branding/default/nubi-debugging.svg');
  });

  it('reports loading with an empty title, then the brand title', async () => {
    const branding = loadBranding({ title: 'Acme' });
    const { result, rerender } = renderHook(() => branding.useBrandingConfig());

    expect(result.current.loading).toBe(true);
    expect(result.current.title).toBe('');
    expect(result.current.logoUrl).toBe('');

    await act(async () => {
      await releaseConfig();
    });
    rerender();

    expect(result.current.loading).toBe(false);
    expect(result.current.title).toBe('Acme');
  });
});
