import { titleCaseForAggregationKey } from 'src/utils/common';
import { isCloudProvider, structuredFieldDescription, structuredFieldLabel } from './eventFilter';

/** Filter fields whose options come from an account's own events; priority is a fixed list. */
export const ACCOUNT_SCOPED_FILTER_TYPES = ['event_type', 'namespace', 'source'] as const;
export type AccountScopedFilterType = (typeof ACCOUNT_SCOPED_FILTER_TYPES)[number];

export type AccountFilterValues = Record<AccountScopedFilterType, string[]>;
/** Values seen per account. An account missing here hasn't loaded (or failed), so nothing is claimed about it. */
export type FilterValuesByAccount = Record<string, AccountFilterValues>;

export interface ScopeAccount {
  id: string;
  label: string;
}

// Names stay readable for one or two accounts; past that only the count fits on the row.
const seenInTag = (where: string[], total: number): string => (where.length <= 2 ? where.join(', ') : `in ${where.length} of ${total} accounts`);

/**
 * One option list across every listened account. With several accounts loaded, a value seen in
 * only some of them names where it was seen; a picked value seen in none stays listed as "not seen".
 */
export const mergeFilterOptions = (
  filterType: AccountScopedFilterType,
  accounts: ScopeAccount[],
  byAccount: FilterValuesByAccount,
  picked: string[],
  formatLabel: (value: string) => string = (v) => v
): { label: string; value: string; searchText?: string }[] => {
  // A reformatted label hides the raw value users type (`job_failure` shows as JobFailure).
  const raw = (value: string) => (formatLabel(value) !== value ? { searchText: value } : {});
  const loaded = accounts.filter((a) => byAccount[a.id]);
  const seenIn = new Map<string, string[]>();
  for (const account of loaded) {
    for (const value of byAccount[account.id][filterType]) {
      const where = seenIn.get(value) || [];
      if (!where.includes(account.label)) where.push(account.label);
      seenIn.set(value, where);
    }
  }
  const options = Array.from(seenIn, ([value, where]) => ({
    value,
    label: loaded.length > 1 && where.length < loaded.length ? `${formatLabel(value)} · ${seenInTag(where, loaded.length)}` : formatLabel(value),
    ...raw(value),
  }));
  options.sort((a, b) => a.label.localeCompare(b.label));
  const unseen = picked
    .filter((v) => !seenIn.has(v))
    .map((value) => ({ value, label: loaded.length > 0 ? `${formatLabel(value)} · not seen` : formatLabel(value), ...raw(value) }));
  return [...unseen, ...options];
};

// Account may be empty (S3) or non-numeric (AWS-managed `arn:aws:iam::aws:policy/...`).
const ARN_RE = /^arn:[\w-]+:[\w-]+:[\w-]*:[\w-]*:(.+)$/;

// Short name for an ARN's resource part; load balancers and target groups end in a random id.
const arnResourceName = (resource: string): string => {
  const parts = resource.split(/[/:]/).filter(Boolean);
  if (parts[0] === 'loadbalancer' && ['app', 'net', 'gwy'].includes(parts[1]) && parts[2]) return parts[2];
  if (parts[0] === 'targetgroup' && parts[1]) return parts[1];
  return parts[parts.length - 1] || resource;
};

/**
 * Event Type option label. Cloud alarm keys are `<alert>-<resource ARN>` (one per resource), so
 * they read as `<Alert> · <resource name>`; the value, which matching compares, is unchanged.
 */
export const eventTypeLabel = (value: string): string => {
  const at = value.indexOf('-arn:');
  const resource = at > 0 ? ARN_RE.exec(value.slice(at + 1))?.[1] : undefined;
  if (!resource) return titleCaseForAggregationKey(value);
  return `${titleCaseForAggregationKey(value.slice(0, at))} · ${arnResourceName(resource)}`;
};

export interface SilencedAccount {
  label: string;
  /** The fields none of whose picked values have been seen in this account. */
  fields: AccountScopedFilterType[];
}

/**
 * Loaded accounts whose events can never pass the picks: for some field, none of the picked
 * values has been seen in that account. Values in one field are OR-ed, fields are AND-ed.
 */
export const accountsSilencedByFilter = (
  picks: Partial<Record<AccountScopedFilterType, string[]>>,
  accounts: ScopeAccount[],
  byAccount: FilterValuesByAccount
): SilencedAccount[] =>
  accounts
    .filter((a) => byAccount[a.id])
    .map((a) => ({
      label: a.label,
      fields: ACCOUNT_SCOPED_FILTER_TYPES.filter((type) => {
        const values = picks[type] || [];
        return values.length > 0 && !values.some((v) => byAccount[a.id][type].includes(v));
      }),
    }))
    .filter((a) => a.fields.length > 0);

const NAMED_SILENCED_ACCOUNTS = 3;

/** One sentence naming the silenced accounts (a count past a few) and the fields to blame. */
export const silencedAccountsMessage = (
  silenced: SilencedAccount[],
  totalAccounts: number,
  fieldLabel: (type: AccountScopedFilterType) => string
): string => {
  const named = silenced.slice(0, NAMED_SILENCED_ACCOUNTS).map((a) => a.label);
  const who =
    silenced.length > NAMED_SILENCED_ACCOUNTS
      ? `${silenced.length} of ${totalAccounts} accounts (${named.join(', ')} and ${silenced.length - named.length} more)`
      : named.join(', ');
  const fields = ACCOUNT_SCOPED_FILTER_TYPES.filter((t) => silenced.some((a) => a.fields.includes(t))).map((t) => fieldLabel(t).toLowerCase());
  return `Events from ${who} won't trigger this automation: none of the picked ${fields.join(' or ')} values have been seen there.`;
};

// `subject_namespace` holds a namespace on clusters and a service on cloud accounts, so a
// mixed set of accounts names both.
export const namespaceFieldCopy = (providers: string[]): { label: string; description: string } => {
  const cloud = providers.filter((p) => isCloudProvider(p));
  if (cloud.length === 0 || cloud.length === providers.length) {
    return { label: structuredFieldLabel('namespace', cloud[0]), description: structuredFieldDescription('namespace', cloud[0]) };
  }
  return {
    label: `${structuredFieldLabel('namespace')} / ${structuredFieldLabel('namespace', cloud[0])}`,
    description: 'Namespace on clusters, service on cloud accounts',
  };
};
