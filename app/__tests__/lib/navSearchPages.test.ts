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
