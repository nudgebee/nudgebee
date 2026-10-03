interface ProviderAccount {
  value: string;
  cloud_provider: string;
}

/**
 * The accounts whose recommendations decide which rules the Optimization trigger offers
 * for a picked account: every account of the same provider. Rule names are per provider
 * (an AWS account never raises a GCP rule, a Kubernetes cluster only raises Kubernetes
 * rules), while a single account may not have raised every rule of its provider yet.
 *
 * Empty means "don't narrow": nothing is picked, or the pick is not a known account
 * (deleted, or the list has not loaded).
 */
export const sameProviderAccountIds = (accountId: string, accounts: ProviderAccount[]): string[] => {
  const provider = accounts.find((a) => a.value === accountId)?.cloud_provider;
  if (!provider) {
    return [];
  }
  return accounts
    .filter((a) => a.cloud_provider === provider)
    .map((a) => a.value)
    .sort();
};
