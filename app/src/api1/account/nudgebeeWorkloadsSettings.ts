import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { safeJSONParse } from 'src/utils/common';

// Per-account "NudgeBee's own workloads" setting, stored as one JSON value in
// cloud_account_attrs under `nudgebee_workloads`. Alerts and digest emails leave
// out the agent's install namespace plus the namespaces listed here; hiding is
// on unless hide_in_notifications is false. api-server's
// validateNudgebeeWorkloadsAttribute enforces the same rules as build below.
export const NUDGEBEE_WORKLOADS_ATTR = 'nudgebee_workloads';

// Kubernetes namespace names (DNS-1123 labels).
const K8S_NAMESPACE_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export interface NudgebeeWorkloadsForm {
  hideInNotifications: boolean;
  namespaces: string;
}

export const DEFAULT_NUDGEBEE_WORKLOADS_FORM: NudgebeeWorkloadsForm = {
  hideInNotifications: true,
  namespaces: '',
};

export function parseNudgebeeWorkloadsForm(raw: unknown): NudgebeeWorkloadsForm {
  const stored = (typeof raw === 'string' ? safeJSONParse(raw) : null) || {};
  return {
    hideInNotifications: stored.hide_in_notifications !== false,
    namespaces: Array.isArray(stored.namespaces) ? stored.namespaces.join(', ') : '',
  };
}

// buildNudgebeeWorkloadsValue validates the form and returns the JSON to store,
// or the message to show.
export function buildNudgebeeWorkloadsValue(form: NudgebeeWorkloadsForm): { value: string } | { error: string } {
  const namespaces = form.namespaces
    .split(',')
    .map((ns) => ns.trim())
    .filter(Boolean);
  const invalid = namespaces.find((ns) => ns.length > 63 || !K8S_NAMESPACE_PATTERN.test(ns));
  if (invalid) {
    return { error: `"${invalid}" is not a valid Kubernetes namespace` };
  }
  return { value: JSON.stringify({ hide_in_notifications: form.hideInNotifications, namespaces }) };
}

const AGENT_INSTALL_NAMESPACE = `
query AgentInstallNamespace {
  agent: agents_list_health(where: __WHERE__) {
    rows {
      connection_status
    }
  }
}
`;

// getAgentInstallNamespace returns the namespace the account's Kubernetes agent
// reports it runs in, or '' when it has not reported one.
export async function getAgentInstallNamespace(accountId: string): Promise<string> {
  const where = { type: { _eq: 'k8s' }, cloud_account_id: { _eq: accountId } };
  const response = await queryGraphQL(AGENT_INSTALL_NAMESPACE.replace('__WHERE__', gqlStringify(where)), 'AgentInstallNamespace');
  const rows = response?.data?.data?.agent?.rows || [];
  for (const row of rows) {
    const status = typeof row?.connection_status === 'string' ? safeJSONParse(row.connection_status) : row?.connection_status;
    if (typeof status?.installationNamespace === 'string' && status.installationNamespace) {
      return status.installationNamespace;
    }
  }
  return '';
}
