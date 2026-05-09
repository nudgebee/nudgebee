import { useState, useEffect, useRef } from 'react';
import apiUser from '@api1/user';
import apiCloudAccount from '@api1/cloud-account';
import apiKubernetes from '@api1/kubernetes';
import apiWorkflow from '@api1/workflow';
import apiTickets from '@api1/tickets';
import observability from '@api1/observability';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';

type OptionItem = { label: string; value: string; description?: string };
type FetcherFn = (formValues: Record<string, any>) => Promise<OptionItem[]>;

/**
 * Registry of fetcher functions keyed by options_source type.
 * To add a new source type, just add an entry here — no other files need to change.
 */
const OPTIONS_SOURCE_FETCHERS: Record<string, FetcherFn> = {
  onboarded_users: async () => {
    const response = await apiUser.listUsers({ status: 'active' });
    const users = response?.data?.users ?? response?.data ?? [];
    return users.map((user: any) => ({
      label: user.display_name || user.username || user.email || user.id,
      value: user.username || user.email || user.id,
    }));
  },

  cloud_services: async (formValues) => {
    const accountId = formValues.account_id;
    if (!accountId) return [];
    const resp = await apiCloudAccount.getCloudResource({ account_id: accountId, status: 'Active' }, 1000);
    const resources = resp?.data?.data?.cloud_resourses || [];
    const serviceSet = new Set<string>();
    for (const r of resources) {
      if (r.service_name) serviceSet.add(r.service_name);
    }
    return [...serviceSet].sort((a, b) => a.localeCompare(b)).map((s) => ({ label: s, value: s }));
  },

  cloud_regions: async (formValues) => {
    const accountId = formValues.account_id;
    if (!accountId) return [];
    const query: any = { account_id: accountId, status: 'Active' };
    if (formValues.service_name) query.serviceName = formValues.service_name;
    const resp = await apiCloudAccount.getCloudResource(query, 1000);
    const resources = resp?.data?.data?.cloud_resourses || [];
    const regionSet = new Set<string>();
    for (const r of resources) {
      if (r.region) regionSet.add(r.region);
    }
    return [...regionSet].sort((a, b) => a.localeCompare(b)).map((r) => ({ label: r, value: r }));
  },

  cloud_resources: async (formValues) => {
    const accountId = formValues.account_id;
    if (!accountId) return [];
    const query: any = { account_id: accountId, status: 'Active' };
    if (formValues.service_name) query.serviceName = formValues.service_name;
    if (formValues.region) query.region = formValues.region;
    const resp = await apiCloudAccount.getCloudResource(query, 1000);
    const resources = resp?.data?.data?.cloud_resourses || [];
    return resources.map((r: any) => ({
      label: r.name || r.resourse_id,
      value: r.resourse_id,
    }));
  },

  cloud_log_groups: async (formValues) => {
    const accountId = formValues.account_id;
    if (!accountId) return [];
    const query: any = { account_id: accountId, type: 'log-group', status: 'Active' };
    if (formValues.region) query.region = formValues.region;
    const resp = await apiCloudAccount.getCloudResource(query, 1000);
    const resources = resp?.data?.data?.cloud_resourses || [];
    return resources.map((r: any) => ({
      label: r.name || r.resourse_id,
      value: r.name || r.resourse_id,
    }));
  },

  // Azure Log Analytics workspaces for the selected cloud account.
  // Mirrors CloudLogsQueryPanel.tsx — the value is the workspace ARM resource
  // ID, which the cloud-collector backend dispatches on (substring match
  // microsoft.operationalinsights/workspaces, see api-server cloud/actions.go).
  azure_log_analytics_workspaces: async (formValues) => {
    const accountId = formValues.account_id;
    if (!accountId) return [];
    const resp = await apiCloudAccount.getCloudResource({ account_id: accountId, type: 'workspaces', status: 'Active' }, 1000);
    const resources = resp?.data?.data?.cloud_resourses || [];
    return resources.map((r: any) => ({
      label: r.region ? `${r.name} (${r.region})` : r.name || r.resourse_id,
      value: r.resourse_id,
    }));
  },

  cloud_metrics: async (formValues) => {
    const accountId = formValues.account_id;
    if (!accountId) return [];
    const resp = await observability.metricsList(accountId, {
      metricProvider: formValues.metric_provider,
      metricProviderSource: formValues.metric_provider_source,
      serviceName: formValues.service_name,
    });
    const metrics = resp?.data?.data?.metrics_list || [];
    return metrics.map((m: any) => ({ label: m.metric, value: m.metric }));
  },

  // Projects from a ticketing integration (Jira projects, GitHub/GitLab repos, etc.).
  // Reads the already-cached list of ticket configurations and finds the matching
  // integration by id, so no extra round-trip to the backend.
  ticket_projects: async (formValues) => {
    const integrationId = formValues.integration_id;
    if (!integrationId) return [];
    const response: any = await apiTickets.listTicketConfigurations();
    const configs = response?.data || [];
    const config = configs.find((c: any) => c.id === integrationId);
    if (!config) return [];
    // ServiceNow uses a fixed 'incident' table, no project concept.
    if (config.tool === 'servicenow') {
      return [{ label: 'incident', value: 'incident' }];
    }
    const projects = config.projects || [];
    // For GitHub/GitLab, `key` is the full `owner/repo` path and `name` is just
    // the repo. Surface the full path in the label so the user sees what gets
    // saved — otherwise picking a dropdown option labeled "repo" stores
    // "owner/repo", and freeSolo typing of "repo" silently saves the wrong value.
    const useKeyAsLabel = config.tool === 'github' || config.tool === 'gitlab';
    return projects.map((p: any) => ({
      label: useKeyAsLabel ? p.key : p.name || p.key,
      value: p.key,
    }));
  },

  // Kubernetes workload names scoped by account + namespace (optionally kind).
  // Mirrors the cascading fetch in useTaskFormData.ts so multi-select fields
  // like `vertical_rightsize_generate.workload_names` get the same dropdown
  // experience as the single-name dropdown on `workload_restart`.
  k8s_workload_names: async (formValues) => {
    const accountId = formValues.account_id;
    const namespace = formValues.namespace;
    if (!accountId || !namespace) return [];
    const query: Record<string, any> = { accountId, namespace, is_active: true };
    if (formValues.kind) query.kind = formValues.kind;
    const response: any = await apiKubernetes.getAllK8sWorkload(query);
    const workloads = response?.data || [];
    return workloads.map((w: any) => ({ label: w.name, value: w.name }));
  },

  mcp_tools: async (formValues) => {
    const accountId = formValues._accountId;
    if (!accountId) return [];

    const mode = formValues.connection_mode || 'integration';
    let params: Record<string, any>;

    if (mode === 'integration') {
      const integrationId = formValues.integration_id;
      if (!integrationId) return [];
      params = { integration_id: integrationId };
    } else {
      const url = formValues.url;
      if (!url) return [];
      params = { url };

      // Forward arbitrary headers (e.g. user-supplied bearer token)
      if (formValues.headers && typeof formValues.headers === 'object' && Object.keys(formValues.headers).length > 0) {
        params.headers = formValues.headers;
      }

      // Forward OAuth2 client_credentials fields when configured
      if (formValues.auth_type) {
        params.auth_type = formValues.auth_type;
        if (formValues.oauth_token_url) params.oauth_token_url = formValues.oauth_token_url;
        if (formValues.oauth_client_id) params.oauth_client_id = formValues.oauth_client_id;
        if (formValues.oauth_client_secret) params.oauth_client_secret = formValues.oauth_client_secret;
        if (formValues.oauth_scope) params.oauth_scope = formValues.oauth_scope;
        if (formValues.oauth_audience) params.oauth_audience = formValues.oauth_audience;
      }
    }

    const resp: any = await apiWorkflow.listMCPTools(accountId, params);

    // Surface failures to the user — a 401/500 from the upstream MCP server
    // arrives here as either a populated `errors` array or null `data`.
    const errors = resp?.errors;
    if (Array.isArray(errors) && errors.length > 0) {
      const message = errors[0]?.message || errors[0]?.toString?.() || 'Failed to list MCP tools';
      snackbar.error(`Failed to list MCP tools: ${message}`);
      return [];
    }

    const tools = resp?.data?.workflow_list_mcp_tools?.tools;
    if (!Array.isArray(tools)) {
      snackbar.error('Failed to list MCP tools: empty response from server');
      return [];
    }

    return tools.map((t: any) => ({
      label: t.name,
      value: t.name,
      description: t.description || '',
    }));
  },

  llm_tools: async (formValues) => {
    const accountId = formValues._accountId;
    if (!accountId) return [];
    const resp: any = await apiAskNudgebee.listTools({ accountId });
    const errors = resp?.data?.errors || resp?.errors;
    if (Array.isArray(errors) && errors.length > 0) {
      const message = errors[0]?.message || errors[0]?.toString?.() || 'Failed to list LLM tools';
      snackbar.error(`Failed to list LLM tools: ${message}`);
      return [];
    }
    const raw = resp?.data?.data?.ai_list_tools?.data;
    let tools: any[] = [];
    try {
      const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
      tools = Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
    return tools
      .filter((t: any) => t?.name && t?.status !== 'disabled')
      .map((t: any) => ({ label: t.name, value: t.name, description: t.description || '' }))
      .sort((a: any, b: any) => a.label.localeCompare(b.label));
  },
};

/**
 * Source types handled exclusively by useTicketDynamicFields (the dedicated
 * `tickets.create` cascading hook). These are always skipped here because the
 * create task's bespoke rendering path reads from that hook directly, and we
 * don't currently expose them on any other task. `ticket_projects` is NOT in
 * this list — other ticket tasks (update, assign, transition, get, …) rely on
 * the generic fetcher below, and for `tickets.create` we skip it explicitly
 * via the task-name check so we don't duplicate the dedicated hook's work.
 */
const TICKET_SOURCE_TYPES = new Set(['ticket_issue_types', 'ticket_field_options']);

interface OptionsSourceResult {
  options: OptionItem[];
  loading: boolean;
}

interface FieldToFetch {
  fieldName: string;
  sourceType: string;
  dependencyMapping?: Record<string, string>;
}

// Resolve whether a field with `visible_when` is currently shown. Hidden fields
// are skipped so we don't waste API calls on unreachable UI.
const isFieldVisible = (field: any, schema: any, formValues: Record<string, any>): boolean => {
  if (!field.visible_when) return true;
  const controllingValue = formValues[field.visible_when.field] ?? schema[field.visible_when.field]?.default;
  return !!controllingValue && field.visible_when.value.includes(controllingValue);
};

// Decide if a schema field should be fetched by this generic hook. Returns the
// FieldToFetch descriptor, or null when the field is owned by another hook
// (create's dedicated cascading hook) or has no registered fetcher.
const resolveFieldToFetch = (
  fieldName: string,
  field: any,
  schema: any,
  formValues: Record<string, any>,
  isTicketCreate: boolean
): FieldToFetch | null => {
  const source = field.options_source;
  if (!source?.type) return null;
  if (TICKET_SOURCE_TYPES.has(source.type)) return null;
  // tickets.create owns `ticket_projects` via useTicketDynamicFields — skip to avoid duplicate fetches.
  if (isTicketCreate && source.type === 'ticket_projects') return null;
  if (!OPTIONS_SOURCE_FETCHERS[source.type]) return null;
  if (!isFieldVisible(field, schema, formValues)) return null;
  return {
    fieldName,
    sourceType: source.type,
    dependencyMapping: source.dependency_mapping,
  };
};

const buildDepKey = (fieldsToFetch: FieldToFetch[], formValues: Record<string, any>): string =>
  fieldsToFetch
    .map((f) => {
      const depValues = f.dependencyMapping
        ? Object.values(f.dependencyMapping)
            .map((formField) => formValues[formField] ?? '')
            .join(',')
        : '';
      return `${f.fieldName}:${f.sourceType}:${depValues}`;
    })
    .join('|');

/**
 * Generic hook that scans a task definition's input_schema for fields with
 * options_source and fetches the corresponding data using the fetcher registry.
 *
 * Supports dependency_mapping — re-fetches when dependency values in formValues change.
 */
export const useOptionsSource = (currentTaskDefinition: any, formValues: Record<string, any>): Record<string, OptionsSourceResult> => {
  const [data, setData] = useState<Record<string, OptionsSourceResult>>({});
  const prevDepsRef = useRef<string>('');

  useEffect(() => {
    const schema = currentTaskDefinition?.input_schema;
    if (!schema) {
      setData({});
      // Reset depKey so the next run with a valid schema re-fetches; otherwise
      // a transient empty render (e.g. taskDefinitions briefly unloaded on
      // sidebar reopen) leaves stale data and the depKey early-return below
      // would skip the recovery fetch.
      prevDepsRef.current = '';
      return;
    }

    const isTicketCreate = currentTaskDefinition?.name === 'tickets.create';
    const fieldsToFetch: FieldToFetch[] = [];
    for (const [fieldName, fieldDef] of Object.entries(schema)) {
      const resolved = resolveFieldToFetch(fieldName, fieldDef as any, schema, formValues, isTicketCreate);
      if (resolved) fieldsToFetch.push(resolved);
    }

    if (fieldsToFetch.length === 0) {
      setData({});
      // Same reason as above: a visible_when-gated field (e.g. ticket_tasks'
      // project_key) can briefly drop out of the visible set during reopen
      // while a synthetic field is being re-derived. Resetting depKey lets us
      // re-fetch once the field becomes visible again.
      prevDepsRef.current = '';
      return;
    }

    const depKey = buildDepKey(fieldsToFetch, formValues);

    if (depKey === prevDepsRef.current) return;
    prevDepsRef.current = depKey;

    // Mark all as loading
    const loading: Record<string, OptionsSourceResult> = {};
    for (const f of fieldsToFetch) {
      loading[f.fieldName] = { options: data[f.fieldName]?.options ?? [], loading: true };
    }
    setData(loading);

    // Fetch all in parallel
    Promise.all(
      fieldsToFetch.map(async (f) => {
        try {
          const options = await OPTIONS_SOURCE_FETCHERS[f.sourceType](formValues);
          return { fieldName: f.fieldName, options, loading: false };
        } catch (err) {
          console.error(`Failed to fetch options for ${f.fieldName} (source: ${f.sourceType}):`, err);
          return { fieldName: f.fieldName, options: [], loading: false };
        }
      })
    ).then((results) => {
      const newData: Record<string, OptionsSourceResult> = {};
      for (const r of results) {
        newData[r.fieldName] = { options: r.options, loading: r.loading };
      }
      setData(newData);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentTaskDefinition, formValues]);

  return data;
};
