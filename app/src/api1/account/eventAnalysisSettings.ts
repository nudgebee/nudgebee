import { queryGraphQL } from '@lib/HttpService';
import { INSERT_ACC_ATTRIBUTE } from '@api1/account';
import { safeJSONParse } from 'src/utils/common';

// Per-account AI event analysis settings, stored as one JSON value in
// cloud_account_attrs under `event_analysis`. api-server's
// validateEventAnalysisAttribute enforces the same rules as validate() below.
export const EVENT_ANALYSIS_ATTR = 'event_analysis';

const MAX_REUSE_WINDOW_HOURS = 720;
// Kubernetes namespace names (DNS-1123 labels).
const K8S_NAMESPACE_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

// Form state: namespace lists are comma-separated text, the window is text so
// blank can mean "platform default".
export interface EventAnalysisForm {
  reuseRelated: boolean;
  reuseWindowHours: string;
  alwaysFreshNamespaces: string;
  excludedNamespaces: string;
}

export const DEFAULT_EVENT_ANALYSIS_FORM: EventAnalysisForm = {
  reuseRelated: true,
  reuseWindowHours: '',
  alwaysFreshNamespaces: '',
  excludedNamespaces: '',
};

// parseEventAnalysisForm turns the stored value (or its absence) into form state.
export function parseEventAnalysisForm(raw: unknown): EventAnalysisForm {
  const stored = (typeof raw === 'string' ? safeJSONParse(raw) : null) || {};
  const list = (v: unknown) => (Array.isArray(v) ? v.join(', ') : '');
  return {
    reuseRelated: stored.reuse_related !== false,
    reuseWindowHours: stored.reuse_window_hours ? String(stored.reuse_window_hours) : '',
    alwaysFreshNamespaces: list(stored.always_fresh_namespaces),
    excludedNamespaces: list(stored.excluded_namespaces),
  };
}

const toNamespaces = (text: string) =>
  text
    .split(',')
    .map((ns) => ns.trim())
    .filter(Boolean);

// buildEventAnalysisValue validates the form and returns the JSON to store, or
// the message to show. Namespace fields hidden for non-Kubernetes accounts keep
// whatever was loaded, so saving never drops them.
export function buildEventAnalysisValue(form: EventAnalysisForm): { value: string } | { error: string } {
  const alwaysFresh = toNamespaces(form.alwaysFreshNamespaces);
  const excluded = toNamespaces(form.excludedNamespaces);
  const invalid = [...alwaysFresh, ...excluded].find((ns) => ns.length > 63 || !K8S_NAMESPACE_PATTERN.test(ns));
  if (invalid) {
    return { error: `"${invalid}" is not a valid Kubernetes namespace` };
  }
  let reuseWindow: number | null = null;
  if (form.reuseWindowHours.trim() !== '') {
    reuseWindow = Number(form.reuseWindowHours);
    if (!Number.isInteger(reuseWindow) || reuseWindow < 1 || reuseWindow > MAX_REUSE_WINDOW_HOURS) {
      return { error: `Reuse window must be a whole number of hours between 1 and ${MAX_REUSE_WINDOW_HOURS}` };
    }
  }
  return {
    value: JSON.stringify({
      reuse_related: form.reuseRelated,
      reuse_window_hours: reuseWindow,
      always_fresh_namespaces: alwaysFresh,
      excluded_namespaces: excluded,
    }),
  };
}

const GET_SETTINGS = `query EventAnalysisSettings($accountId: String!) {
  cloud_account_attrs_v2(where: {cloud_account_id: {_eq: $accountId}, name: {_eq: "${EVENT_ANALYSIS_ATTR}"}}) {
    rows { value }
  }
}`;

export async function getEventAnalysisForm(accountId: string): Promise<EventAnalysisForm> {
  const response = await queryGraphQL(GET_SETTINGS, 'EventAnalysisSettings', { accountId });
  const rows = response?.data?.data?.cloud_account_attrs_v2?.rows;
  if (response?.data?.errors?.length || !Array.isArray(rows)) {
    throw new Error('Could not load the AI event analysis settings. Retry before making changes.');
  }
  return parseEventAnalysisForm(rows[0]?.value);
}

export async function saveEventAnalysisValue(accountId: string, value: string): Promise<void> {
  const response = await queryGraphQL(INSERT_ACC_ATTRIBUTE, 'InsertAccountAttribute', {
    object: { objects: [{ cloud_account_id: accountId, name: EVENT_ANALYSIS_ATTR, value }] },
  });
  if (response?.data?.errors?.length || response?.data?.data?.cloud_account_attrs_upsert?.affected_rows !== 1) {
    throw new Error('Could not save the AI event analysis settings. Please retry.');
  }
}
