/**
 * Which `cloud_accounts.cloud_provider` values the Account Overview treats as
 * infrastructure.
 *
 * `accounts_list` also returns integrations as accounts — Slack arrives as
 * cloud_provider='Slack' — so the overview has to allow-list rather than
 * "everything that is not K8s", otherwise every connected chat/ticketing
 * integration would render as an infrastructure card.
 */

/** Provider-API-backed cloud accounts — the ones /cloud-account/details renders. */
export const CLOUD_PROVIDERS = ['AWS', 'Azure', 'GCP', 'CloudFoundry'] as const;

/** Self-hosted VM fleets (account_type = 'vm') — the ones /vm renders. */
export const SELF_HOSTED_PROVIDER = 'SelfHosted';

const normalise = (provider?: string) => (provider || '').toUpperCase();

export const isCloudProvider = (provider?: string) => CLOUD_PROVIDERS.some((known) => known.toUpperCase() === normalise(provider));

export const isSelfHostedProvider = (provider?: string) => normalise(provider) === SELF_HOSTED_PROVIDER.toUpperCase();
