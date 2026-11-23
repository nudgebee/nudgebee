import { integrationProviders } from '@lib/navSearchPages';
import { SECTIONS_CONFIG, DISABLED_PROVIDERS } from '@components/accounts/integration';

describe('integrationProviders (navSearchPages.ts)', () => {
  const enabledProviders = new Set(
    SECTIONS_CONFIG.flatMap((section: { providers: string[] }) => section.providers).filter((provider) => !DISABLED_PROVIDERS.has(provider))
  );

  it('has a row for every enabled provider in integration.jsx SECTIONS_CONFIG', () => {
    const missing = [...enabledProviders].filter((provider) => !integrationProviders.includes(provider));
    expect(missing).toEqual([]);
  });

  it('has no row for a provider that no longer exists (or is disabled) in SECTIONS_CONFIG', () => {
    const stale = integrationProviders.filter((provider) => !enabledProviders.has(provider));
    expect(stale).toEqual([]);
  });
});

describe('navSearchPages (navSearchPages.ts) on OSS', () => {
  const EE_ONLY_PATHS = [
    '/user-management#ai-tools/egress-filter',
    '/user-management#ai-tools/egress-filter/tool-approvals',
    '/user-management#ai-tools/memory-policy',
  ];

  const loadWithOSSMode = (oss: boolean) => {
    let pages: { path: string }[] = [];
    jest.isolateModules(() => {
      jest.doMock('@hooks/useBCortexEnabled', () => ({ isOSSDeploymentMode: () => oss }));
      pages = require('@lib/navSearchPages').navSearchPages;
    });
    return pages.map((p) => p.path);
  };

  it('drops the EE-only Admin sub-tabs that the Admin page itself hides on OSS', () => {
    const paths = loadWithOSSMode(true);
    EE_ONLY_PATHS.forEach((path) => expect(paths).not.toContain(path));
    expect(paths).toContain('/user-management#ai-tools/budgets-limits');
  });

  it('keeps them on an enterprise build', () => {
    const paths = loadWithOSSMode(false);
    EE_ONLY_PATHS.forEach((path) => expect(paths).toContain(path));
  });
});
